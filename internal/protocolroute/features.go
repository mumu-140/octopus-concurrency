// Package protocolroute 实现协议路由阶段 A 核心：请求特性捕获、
// 转换安全门禁、不可变 AttemptPlan 与 LegacyFixedAttempt。
//
// 本包不产生运行副作用：不读写数据库、不修改共享 Channel 对象、
// 不影响未接入调用方的现有 relay 行为。
package protocolroute

import (
	"github.com/bestruirui/octopus/internal/protocol"
	tmodel "github.com/bestruirui/octopus/internal/transformer/model"
)

// RequestFeatureFlags 保存请求解析时捕获的特性（设计 §5.3）。
// 门禁根据特性桶而不是协议名称判断可转换性。
type RequestFeatureFlags struct {
	PlainText          bool // 普通文本消息
	FunctionTools      bool // 通用 function tools
	ResponsesNative    bool // Responses 原生 tools / raw input items
	Continuation       bool // previous_response_id / conversation / continuation
	Multimodal         bool // 图片或其他多模态输入
	Reasoning          bool // reasoning / thinking 请求参数
	AnthropicSignature bool // Anthropic thinking signature / redacted blocks
	AnthropicCache     bool // Anthropic cache control / beta
	Streaming          bool // 流式
	Embedding          bool // Embedding 请求
	PassthroughPinned  bool // responsesPassthroughRequired 等强制直通标记
}

// CaptureFeatures 从内部请求快照提取特性标志。
// 只读，不修改请求。
func CaptureFeatures(req *tmodel.InternalLLMRequest) RequestFeatureFlags {
	var f RequestFeatureFlags
	if req == nil {
		return f
	}

	f.Streaming = req.Stream != nil && *req.Stream
	f.Embedding = req.IsEmbeddingRequest()
	f.FunctionTools = len(req.Tools) > 0
	f.Reasoning = req.Thinking != nil || req.ReasoningEffort != ""
	f.PassthroughPinned = req.OpenAIResponsesPassthroughRequired

	if len(req.RawInputItems) > 0 {
		f.ResponsesNative = true
	}
	if req.PreviousResponseID != nil || len(req.Conversation) > 0 {
		f.Continuation = true
	}

	for i := range req.Messages {
		msg := &req.Messages[i]
		if len(msg.RedactedThinkingBlocks) > 0 || len(msg.ReasoningBlocks) > 0 {
			f.AnthropicSignature = true
		}
		if msg.CacheControl != nil {
			f.AnthropicCache = true
		}
		if msg.Content.Content != nil && *msg.Content.Content != "" {
			f.PlainText = true
		}
		for j := range msg.Content.MultipleContent {
			switch msg.Content.MultipleContent[j].Type {
			case "text":
				f.PlainText = true
			case "image_url", "input_audio", "file", "document":
				f.Multimodal = true
			}
		}
	}

	if ext := req.ProviderExtensions; ext != nil && ext.Anthropic != nil {
		if len(ext.Anthropic.Beta) > 0 || ext.Anthropic.CacheControl != nil {
			f.AnthropicCache = true
		}
	}

	return f
}

// RequiresLegacyOnly 报告首版未支持的特性桶是否命中：
// 多模态、Embedding（设计 §5.3 Request adaptive eligibility）。
// WS、compact、图片生成走独立 Handler，不经过本判定。
func (f RequestFeatureFlags) RequiresLegacyOnly() bool {
	return f.Multimodal || f.Embedding
}

// IsAdaptiveCandidateType 报告候选 Channel 协议是否属于首版 Adaptive 三协议。
func IsAdaptiveCandidateType(p protocol.Protocol) bool {
	return p.IsAdaptive()
}
