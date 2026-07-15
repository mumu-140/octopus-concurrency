package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

// proxyNonStream 将上游非流式响应原样透传到下游，同时尽量提取 usage（避免解析巨大 b64_json）。
func proxyNonStream(c *gin.Context, respUp *http.Response) (*imagesUsage, bool, error) {
	ct := respUp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Header("Content-Type", ct)
	c.Status(respUp.StatusCode)

	scanner := newUsageScanner()

	buf := make([]byte, 32*1024)
	for {
		n, rerr := respUp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			scanner.Feed(chunk)
			if _, werr := c.Writer.Write(chunk); werr != nil {
				return scanner.Usage(), true, werr
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return scanner.Usage(), c.Writer.Written(), rerr
		}
	}

	return scanner.Usage(), c.Writer.Written(), nil
}

// proxySSE 将上游 SSE 逐行解析 event/data/空行并透传到下游；首事件计为 FirstTokenTime；支持 FirstTokenTimeOut 切换。
func proxySSE(ctx context.Context, c *gin.Context, respUp *http.Response, firstTokenTimeOutSec int, metrics *imagesRelayMetrics, hb *earlyHeartbeat) (*imagesUsage, bool, error) {
	if ct := respUp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "text/event-stream") {
		b, _ := io.ReadAll(io.LimitReader(respUp.Body, imagesUpstreamErrorBodyLimit))
		return nil, false, fmt.Errorf("upstream returned non-SSE content-type %q for stream request: %s", ct, string(b))
	}

	// 交接早期心跳给本函数内层 ticker
	hb.Hand()

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	heartbeatTicker, heartbeatC := newStreamHeartbeatTicker()
	if heartbeatTicker != nil {
		defer heartbeatTicker.Stop()
	}

	type lineResult struct {
		line []byte
		err  error
		eof  bool
	}

	results := make(chan lineResult, 1)
	go func() {
		defer close(results)
		br := bufio.NewReaderSize(respUp.Body, 64*1024)
		for {
			line, err := readLineLimited(br, maxSSEEventSize)
			if err != nil {
				if errors.Is(err, io.EOF) {
					results <- lineResult{eof: true}
					return
				}
				results <- lineResult{err: err}
				return
			}
			results <- lineResult{line: line}
		}
	}()

	var firstTokenTimer *time.Timer
	var firstTokenC <-chan time.Time
	if firstTokenTimeOutSec > 0 {
		firstTokenTimer = time.NewTimer(time.Duration(firstTokenTimeOutSec) * time.Second)
		firstTokenC = firstTokenTimer.C
		defer func() {
			if firstTokenTimer != nil {
				firstTokenTimer.Stop()
			}
		}()
	}

	var (
		firstWrite       = true
		currentEvent     string
		completedScanner = newUsageScanner()
	)

	for {
		select {
		case <-ctx.Done():
			log.Infof("client disconnected, stopping stream")
			cancelErr := contextError(ctx)
			if cancelErr == nil {
				cancelErr = context.Canceled
			}
			_ = respUp.Body.Close()
			return completedScanner.Usage(), !firstWrite, cancelErr

		case <-firstTokenC:
			log.Warnf("first token timeout (%ds), switching channel", firstTokenTimeOutSec)
			_ = respUp.Body.Close()
			return completedScanner.Usage(), !firstWrite, fmt.Errorf("first token timeout (%ds)", firstTokenTimeOutSec)

		case <-heartbeatC:
			if err := writeSSEHeartbeat(c.Writer); err != nil {
				return completedScanner.Usage(), c.Writer.Written(), err
			}

		case r, ok := <-results:
			if !ok {
				return completedScanner.Usage(), !firstWrite, errors.New("upstream SSE ended before image_generation.completed")
			}
			if r.eof {
				return completedScanner.Usage(), !firstWrite, errors.New("upstream SSE ended before image_generation.completed")
			}
			if r.err != nil {
				return completedScanner.Usage(), !firstWrite, fmt.Errorf("failed to read stream line: %w", r.err)
			}

			line := r.line
			trimmed := bytes.TrimRight(line, "\r\n")
			terminalEvent := ""
			if len(trimmed) == 0 {
				// 空行：事件边界
				terminalEvent = currentEvent
				currentEvent = ""
			} else if bytes.HasPrefix(trimmed, []byte("event:")) {
				currentEvent = strings.TrimSpace(string(trimmed[len("event:"):]))
			} else if bytes.HasPrefix(trimmed, []byte("data:")) {
				// 仅在 completed 事件上尝试提取 usage（避免解析/分配巨大 b64_json）
				payload := bytes.TrimSpace(trimmed[len("data:"):])
				switch {
				case currentEvent == "image_generation.completed" || bytes.Contains(payload, []byte(`"type":"image_generation.completed"`)):
					currentEvent = "image_generation.completed"
					completedScanner.Feed(payload)
				case currentEvent == "image_generation.failed" || bytes.Contains(payload, []byte(`"type":"image_generation.failed"`)):
					currentEvent = "image_generation.failed"
				}
			}

			if _, werr := c.Writer.Write(line); werr != nil {
				return completedScanner.Usage(), true, werr
			}
			c.Writer.Flush()

			if firstWrite {
				metrics.SetFirstTokenTime(time.Now())
				firstWrite = false
				if firstTokenTimer != nil {
					if !firstTokenTimer.Stop() {
						select {
						case <-firstTokenTimer.C:
						default:
						}
					}
					firstTokenTimer = nil
					firstTokenC = nil
				}
			}

			switch terminalEvent {
			case "image_generation.completed":
				return completedScanner.Usage(), true, nil
			case "image_generation.failed":
				return completedScanner.Usage(), true, errors.New("upstream image generation failed")
			}
		}
	}
}

func readLineLimited(br *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		part, err := br.ReadSlice('\n')
		out = append(out, part...)
		if len(out) > limit {
			return nil, fmt.Errorf("sse line exceeds limit %d bytes", limit)
		}
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		// 允许返回已读部分 + err（调用方按 err 处理）
		return out, err
	}
}

type usageScanner struct {
	matchIdx       int
	waitForObject  bool
	collecting     bool
	braceDepth     int
	inString       bool
	escape         bool
	buf            bytes.Buffer
	usage          *imagesUsage
	done           bool
	maxCollectSize int
}

func newUsageScanner() *usageScanner {
	return &usageScanner{maxCollectSize: 64 * 1024}
}

// Feed 逐字节扫描输入，定位 "usage":{...} 并仅解析 usage 子对象。
// 该实现用于避免整体 json.Unmarshal 造成 b64_json 巨大内存分配。
func (s *usageScanner) Feed(p []byte) {
	if s.done || len(p) == 0 {
		return
	}
	const pat = `"usage":`

	for _, b := range p {
		if s.done {
			return
		}

		if s.collecting {
			if s.buf.Len() >= s.maxCollectSize {
				s.collecting = false
				s.done = true
				return
			}
			s.buf.WriteByte(b)

			if s.inString {
				if s.escape {
					s.escape = false
				} else if b == '\\' {
					s.escape = true
				} else if b == '"' {
					s.inString = false
				}
				continue
			}

			if b == '"' {
				s.inString = true
				continue
			}

			switch b {
			case '{':
				s.braceDepth++
			case '}':
				s.braceDepth--
				if s.braceDepth == 0 {
					var u imagesUsage
					if err := json.Unmarshal(s.buf.Bytes(), &u); err == nil {
						s.usage = &u
					}
					s.done = true
					s.collecting = false
					return
				}
			}
			continue
		}

		if s.waitForObject {
			if b == '{' {
				s.collecting = true
				s.braceDepth = 1
				s.buf.Reset()
				s.buf.WriteByte('{')
				s.inString = false
				s.escape = false
				s.waitForObject = false
				continue
			}
			// 跳过空白，遇到其他字符则放弃
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
				continue
			}
			s.waitForObject = false
			continue
		}

		// 匹配 "usage":
		if b == pat[s.matchIdx] {
			s.matchIdx++
			if s.matchIdx == len(pat) {
				s.waitForObject = true
				s.matchIdx = 0
			}
			continue
		}

		// 失败回退：若当前字符可能是 pat[0]，则 matchIdx=1
		if b == pat[0] {
			s.matchIdx = 1
		} else {
			s.matchIdx = 0
		}
	}
}

func (s *usageScanner) Usage() *imagesUsage {
	return s.usage
}
