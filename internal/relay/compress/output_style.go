package compress

import (
	"strings"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

// styleMarker 幂等标记: 已注入过的请求不重复注入。
// 借鉴 OmniRoute output styles(静态确定性文本, 对 prompt cache 友好)。
const styleMarker = "[Octopus Output Style]"

// styleCatalog 输出风格目录。值为注入 system prompt 的静态指令。
var styleCatalog = map[string]string{
	"terse-prose": "Drop filler, articles, and hedging; keep technical substance exact. Prefer short declarative sentences. No pleasantries.",
	"terse-cjk":   "回复简洁：省略客套话与冗余修饰，技术内容保持精确完整，优先短句直述。",
}

// outputStyleEngine 向 system prompt 注入输出风格指令。
// 注意: 这不是压缩(会增加少量字符), 定位是响应行为塑形。
type outputStyleEngine struct{}

func (outputStyleEngine) Name() string { return "output-style" }

func (outputStyleEngine) Apply(msgs []transformerModel.Message, cfg *dbmodel.GroupCompressConfig) []transformerModel.Message {
	style, ok := styleCatalog[cfg.OutputStyle]
	if cfg.OutputStyle == "" || !ok {
		return msgs
	}
	line := styleMarker + " " + style

	// 幂等: 任一消息已含标记则跳过
	hasMarker := false
	forEachText(msgs, func(_, text string) string {
		if strings.Contains(text, styleMarker) {
			hasMarker = true
		}
		return text
	})
	if hasMarker {
		return msgs
	}

	// 优先并入首个 system 消息, 否则头部插入新 system 消息
	for i := range msgs {
		if msgs[i].Role == "system" && msgs[i].Content.Content != nil {
			*msgs[i].Content.Content += "\n\n" + line
			return msgs
		}
	}
	content := line
	return append([]transformerModel.Message{{
		Role:    "system",
		Content: transformerModel.MessageContent{Content: &content},
	}}, msgs...)
}
