// Package protocol 定义稳定的通用协议枚举，以及与现有整数 OutboundType、
// SiteModelRouteType 和 APIFormat 的显式映射。
//
// 设计约束（fwq57ys-octopus-model-protocol-routing-design.md §4.1）：
//   - 协议持久化和策略配置只使用本包的字符串枚举；
//   - 不能直接把整数 Channel.Type 当作持久化协议值；
//   - 首版 Adaptive resolver 只推理 openai_chat / openai_response / anthropic，
//     其余类型固定走 LegacyFixedAttempt。
package protocol

import (
	"github.com/bestruirui/octopus/internal/model"
	tmodel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

// Protocol 是稳定的通用协议字符串枚举。
type Protocol string

const (
	OpenAIChat      Protocol = "openai_chat"
	OpenAIResponse  Protocol = "openai_response"
	Anthropic       Protocol = "anthropic"
	Gemini          Protocol = "gemini"
	Volcengine      Protocol = "volcengine"
	OpenAIEmbedding Protocol = "openai_embedding"
	Unknown         Protocol = "unknown"
)

// adaptiveProtocols 是首版 Adaptive resolver 允许推理的三协议集合。
var adaptiveProtocols = map[Protocol]bool{
	OpenAIChat:     true,
	OpenAIResponse: true,
	Anthropic:      true,
}

// IsAdaptive 报告协议是否属于首版 Adaptive 三协议。
func (p Protocol) IsAdaptive() bool {
	return adaptiveProtocols[p]
}

// Valid 报告协议是否是已知枚举值（不含 Unknown）。
func (p Protocol) Valid() bool {
	switch p {
	case OpenAIChat, OpenAIResponse, Anthropic, Gemini, Volcengine, OpenAIEmbedding:
		return true
	}
	return false
}

// ---- OutboundType 映射 ----

var outboundToProtocol = map[outbound.OutboundType]Protocol{
	outbound.OutboundTypeOpenAIChat:      OpenAIChat,
	outbound.OutboundTypeOpenAIResponse:  OpenAIResponse,
	outbound.OutboundTypeAnthropic:       Anthropic,
	outbound.OutboundTypeGemini:          Gemini,
	outbound.OutboundTypeVolcengine:      Volcengine,
	outbound.OutboundTypeOpenAIEmbedding: OpenAIEmbedding,
}

var protocolToOutbound = map[Protocol]outbound.OutboundType{
	OpenAIChat:      outbound.OutboundTypeOpenAIChat,
	OpenAIResponse:  outbound.OutboundTypeOpenAIResponse,
	Anthropic:       outbound.OutboundTypeAnthropic,
	Gemini:          outbound.OutboundTypeGemini,
	Volcengine:      outbound.OutboundTypeVolcengine,
	OpenAIEmbedding: outbound.OutboundTypeOpenAIEmbedding,
}

// FromOutboundType 把整数 Channel.Type 映射为协议枚举。
func FromOutboundType(t outbound.OutboundType) Protocol {
	if p, ok := outboundToProtocol[t]; ok {
		return p
	}
	return Unknown
}

// ToOutboundType 把协议枚举映射回适配器注册所需的 OutboundType。
// 第二返回值为 false 表示该协议没有对应的出站适配器类型。
func (p Protocol) ToOutboundType() (outbound.OutboundType, bool) {
	t, ok := protocolToOutbound[p]
	return t, ok
}

// ---- SiteModelRouteType 映射 ----

var routeTypeToProtocol = map[model.SiteModelRouteType]Protocol{
	model.SiteModelRouteTypeOpenAIChat:      OpenAIChat,
	model.SiteModelRouteTypeOpenAIResponse:  OpenAIResponse,
	model.SiteModelRouteTypeAnthropic:       Anthropic,
	model.SiteModelRouteTypeGemini:          Gemini,
	model.SiteModelRouteTypeVolcengine:      Volcengine,
	model.SiteModelRouteTypeOpenAIEmbedding: OpenAIEmbedding,
}

// FromSiteModelRouteType 把站点模型路由类型映射为协议枚举。
func FromSiteModelRouteType(t model.SiteModelRouteType) Protocol {
	if p, ok := routeTypeToProtocol[t]; ok {
		return p
	}
	return Unknown
}

// ToSiteModelRouteType 把协议映射回站点模型路由类型。
func (p Protocol) ToSiteModelRouteType() model.SiteModelRouteType {
	for rt, pp := range routeTypeToProtocol {
		if pp == p {
			return rt
		}
	}
	return model.SiteModelRouteTypeUnknown
}

// ---- APIFormat（入口协议）映射 ----

var apiFormatToProtocol = map[tmodel.APIFormat]Protocol{
	tmodel.APIFormatOpenAIChatCompletion: OpenAIChat,
	tmodel.APIFormatOpenAIResponse:       OpenAIResponse,
	tmodel.APIFormatAnthropicMessage:     Anthropic,
	tmodel.APIFormatGeminiContents:       Gemini,
	tmodel.APIFormatOpenAIEmbedding:      OpenAIEmbedding,
}

// FromAPIFormat 把入站 RawAPIFormat 映射为 ingress 协议。
// 图片生成、AiSDK 等非三协议入口返回 Unknown，由调用方走 legacy。
func FromAPIFormat(f tmodel.APIFormat) Protocol {
	if p, ok := apiFormatToProtocol[f]; ok {
		return p
	}
	return Unknown
}
