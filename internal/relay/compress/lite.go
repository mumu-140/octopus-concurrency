package compress

import (
	"fmt"
	"strings"
	"unicode/utf8"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

const (
	liteMinTextChars = 500  // 短于该长度的文本段不处理
	toolMaxChars     = 3000 // tool 结果超过该长度才截断
	toolHeadChars    = 2000 // 截断保留头部
	toolTailChars    = 800  // 截断保留尾部(修正 OmniRoute 只保头导致尾部 summary 丢失的缺陷)
)

type liteEngine struct{}

func (liteEngine) Name() string { return "lite" }

func (liteEngine) Apply(msgs []transformerModel.Message, cfg *dbmodel.GroupCompressConfig) []transformerModel.Message {
	if !cfg.Lite {
		return msgs
	}
	msgs = dedupSystemMessages(msgs)
	forEachText(msgs, func(role, text string) string {
		if len(text) < liteMinTextChars {
			return text
		}
		out := collapseWhitespace(text)
		if role == "tool" {
			out = truncateKeepHeadTail(out)
		}
		// 保真兜底: 只在更短时替换
		if len(out) < len(text) {
			return out
		}
		return text
	})
	return msgs
}

// dedupSystemMessages 移除内容完全重复的 system 消息（保留首次出现）。
func dedupSystemMessages(msgs []transformerModel.Message) []transformerModel.Message {
	seen := make(map[string]struct{}, 2)
	out := msgs[:0]
	for _, m := range msgs {
		if m.Role == "system" && m.Content.Content != nil {
			key := *m.Content.Content
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, m)
	}
	// 避免原地复用导致尾部悬垂引用
	for i := len(out); i < len(msgs); i++ {
		msgs[i] = transformerModel.Message{}
	}
	return out
}

// collapseWhitespace 折叠连续空行为最多 1 行、清除行尾空白。代码围栏内部不处理。
func collapseWhitespace(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	fenced := false
	blankRun := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			fenced = !fenced
			blankRun = 0
			out = append(out, line)
			continue
		}
		if !fenced {
			line = strings.TrimRight(line, " \t")
			if line == "" {
				blankRun++
				if blankRun >= 2 {
					continue
				}
			} else {
				blankRun = 0
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// truncateKeepHeadTail 超长 tool 结果保头保尾截断，中间以标记替换。
// 尾部常含关键结论行（如 "found 0 vulnerabilities"），必须保留。
func truncateKeepHeadTail(text string) string {
	if len(text) <= toolMaxChars {
		return text
	}
	head := safePrefix(text, toolHeadChars)
	tail := safeSuffix(text, toolTailChars)
	omitted := len(text) - len(head) - len(tail)
	return head + fmt.Sprintf("\n...[truncated %d chars]...\n", omitted) + tail
}

// safePrefix 取前 n 字节，回退到 UTF-8 rune 边界。
func safePrefix(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// safeSuffix 取后 n 字节，前移到 UTF-8 rune 边界。
func safeSuffix(s string, n int) string {
	start := len(s) - n
	if start <= 0 {
		return s
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
