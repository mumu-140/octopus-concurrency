package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/bestruirui/octopus/internal/helper"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay/bodycache"
	"github.com/gin-gonic/gin"
)

func parseJSONModelAndStream(bc *bodycache.BodyCache) (payload map[string]any, modelName string, stream bool, err error) {
	r, err := bc.NewReader()
	if err != nil {
		return nil, "", false, err
	}
	defer r.Close()

	body, err := io.ReadAll(r)
	if err != nil {
		return nil, "", false, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, "", false, errors.New("empty body")
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, "", false, errors.New("invalid json")
	}

	rawModel, ok := m["model"]
	if !ok {
		return nil, "", false, errors.New("model is required")
	}
	modelStr, ok := rawModel.(string)
	if !ok || strings.TrimSpace(modelStr) == "" {
		return nil, "", false, errors.New("model is required")
	}

	stream = false
	if v, ok := m["stream"]; ok {
		switch vv := v.(type) {
		case bool:
			stream = vv
		case string:
			stream = strings.EqualFold(strings.TrimSpace(vv), "true")
		case float64:
			stream = vv != 0
		}
	}

	return m, strings.TrimSpace(modelStr), stream, nil
}

func parseMultipartModelAndStream(bc *bodycache.BodyCache, boundary string) (modelName string, stream bool, err error) {
	r, err := bc.NewReader()
	if err != nil {
		return "", false, err
	}
	defer r.Close()

	mr := multipart.NewReader(r, boundary)

	stream = false
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", false, err
		}

		name := part.FormName()
		if name == "" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}

		switch name {
		case "model":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			modelName = strings.TrimSpace(string(b))
		case "stream":
			b, _ := io.ReadAll(io.LimitReader(part, 16))
			stream = strings.EqualFold(strings.TrimSpace(string(b)), "true")
		default:
			_, _ = io.Copy(io.Discard, part)
		}
		_ = part.Close()
	}

	if strings.TrimSpace(modelName) == "" {
		return "", false, errors.New("model is required")
	}
	return modelName, stream, nil
}

func imagesAttempt(
	ctx context.Context,
	endpoint string,
	c *gin.Context,
	bc *bodycache.BodyCache,
	isMultipart bool,
	boundary string,
	jsonPayload map[string]any,
	stream bool,
	channel *model.Channel,
	channelKey string,
	firstTokenTimeOutSec int,
	metrics *imagesRelayMetrics,
	actualModel string,
	hb *earlyHeartbeat,
) (statusCode int, written bool, usage *imagesUsage, upstreamCT string, err error) {
	// 构建 URL（baseUrl.Path 后追加 endpoint）
	baseURL := channel.GetBaseUrl()
	parsedURL, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return 0, false, nil, "", fmt.Errorf("failed to parse base url: %w", err)
	}
	parsedURL.Path = parsedURL.Path + endpoint

	var bodyReader io.Reader
	var contentType string

	if isMultipart {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		contentType = mw.FormDataContentType()
		bodyReader = pr

		go func() {
			src, err := bc.NewReader()
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			defer src.Close()

			if err := copyMultipartReplaceModel(src, boundary, mw, actualModel); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			// 先关闭 multipart.Writer 写入结束 boundary，再关闭 pipe writer
			if err := mw.Close(); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			_ = pw.Close()
		}()
	} else {
		// JSON：仅改写 model 字段，其余保持不变
		// 注意：每次尝试都重新 marshal 生成 body，确保可重试重建
		if jsonPayload == nil {
			return 0, false, nil, "", errors.New("nil json payload")
		}
		jsonPayload["model"] = actualModel
		b, err := json.Marshal(jsonPayload)
		if err != nil {
			return 0, false, nil, "", fmt.Errorf("failed to marshal json: %w", err)
		}
		bodyReader = bytes.NewReader(b)
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bodyReader)
	if err != nil {
		return 0, false, nil, "", fmt.Errorf("failed to create request: %w", err)
	}
	req.URL = parsedURL
	req.Method = http.MethodPost

	// Header 透传：复制下游 header，过滤 hop-by-hop 与鉴权相关
	copyHeadersToUpstream(req, c, channel, channelKey, contentType, stream)

	// 发送请求
	httpClient, err := helper.ChannelHTTPClientWithContext(ctx, channel)
	if err != nil {
		return 0, false, nil, "", err
	}

	respUp, err := httpClient.Do(req)
	if err != nil {
		return 0, false, nil, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer respUp.Body.Close()

	upstreamCT = respUp.Header.Get("Content-Type")

	// stream=true：逐行解析 event/data/空行边界透传
	if stream {
		if respUp.StatusCode < 200 || respUp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(respUp.Body, imagesUpstreamErrorBodyLimit))
			return respUp.StatusCode, false, nil, upstreamCT, newImagesUpstreamError(respUp.StatusCode, respUp.Header.Get("Retry-After"), b)
		}
		u, w, err := proxySSE(ctx, c, respUp, firstTokenTimeOutSec, metrics, hb)
		return respUp.StatusCode, w, u, upstreamCT, err
	}

	// 非流式：2xx 透传，否则读取限长错误体用于错误信息与重试判定
	if respUp.StatusCode < 200 || respUp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(respUp.Body, imagesUpstreamErrorBodyLimit))
		return respUp.StatusCode, false, nil, upstreamCT, newImagesUpstreamError(respUp.StatusCode, respUp.Header.Get("Retry-After"), b)
	}

	u, w, err := proxyNonStream(c, respUp)
	return respUp.StatusCode, w, u, upstreamCT, err
}

func copyHeadersToUpstream(req *http.Request, c *gin.Context, channel *model.Channel, channelKey string, contentType string, stream bool) {
	for k, values := range c.Request.Header {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+channelKey)

	// 防止 Go 默认 User-Agent 泄露到上游
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "")
	}

	if len(channel.CustomHeader) > 0 {
		for _, h := range channel.CustomHeader {
			req.Header.Set(h.HeaderKey, h.HeaderValue)
		}
	}
}

func copyMultipartReplaceModel(src io.Reader, boundary string, dst *multipart.Writer, newModel string) error {
	mr := multipart.NewReader(src, boundary)

	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		hdr := make(textproto.MIMEHeader, len(part.Header))
		for k, vv := range part.Header {
			cp := make([]string, len(vv))
			copy(cp, vv)
			hdr[k] = cp
		}

		pw, err := dst.CreatePart(hdr)
		if err != nil {
			_ = part.Close()
			return err
		}

		if part.FormName() == "model" && part.FileName() == "" {
			// 丢弃原值，写入替换后的 model（继续复制后续 part）
			_, _ = io.Copy(io.Discard, part)
			_, werr := io.WriteString(pw, newModel)
			_ = part.Close()
			if werr != nil {
				return werr
			}
			continue
		}

		_, err = io.Copy(pw, part)
		_ = part.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
