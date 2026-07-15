package protocolroute

import "github.com/bestruirui/octopus/internal/protocol"

// ConversionVerdict 是转换矩阵输出（设计 §5.4）。
type ConversionVerdict string

const (
	VerdictLossless            ConversionVerdict = "lossless"
	VerdictSupportedWithLimits ConversionVerdict = "supported_with_limits"
	VerdictForbidden           ConversionVerdict = "forbidden"
	VerdictLegacyFixed         ConversionVerdict = "legacy_fixed"
)

// ConversionMode 描述 Attempt 的执行方式（设计 §4.1）。
type ConversionMode string

const (
	ModeRawPassthrough ConversionMode = "raw_passthrough"
	ModeNormalized     ConversionMode = "normalized"
	ModeTranslated     ConversionMode = "translated"
	ModeLegacy         ConversionMode = "legacy"
)

// passthroughCapable 记录已实现原始直通的同协议组合。
// Chat→Chat 当前没有 TransformRequestRaw，首版走 normalized（设计 §3）。
var passthroughCapable = map[protocol.Protocol]bool{
	protocol.OpenAIResponse: true,
	protocol.Anthropic:      true,
}

// EvaluateConversion 依据首版能力矩阵判定 ingress→upstream 是否允许进入
// Adaptive 转换（设计 §5.4）。
//
// 前置：调用方已确认 features.RequiresLegacyOnly()==false 且双方协议均属
// Adaptive 三协议；未满足前置时的候选应直接走 LegacyFixedAttempt，
// 不进入本矩阵。
func EvaluateConversion(ingress, upstream protocol.Protocol, features RequestFeatureFlags) ConversionVerdict {
	if !ingress.IsAdaptive() || !upstream.IsAdaptive() {
		return VerdictLegacyFixed
	}

	samePair := ingress == upstream

	// Responses 原生 tools / continuation / OpenAI reasoning 专有字段：
	// 只允许 Responses 同协议原始直通。
	if features.ResponsesNative || features.Continuation || features.PassthroughPinned {
		if samePair && ingress == protocol.OpenAIResponse {
			return VerdictLossless
		}
		return VerdictForbidden
	}

	// Anthropic thinking/signature 与 cache control/beta：
	// 只允许 Anthropic 同协议原始直通。
	if features.AnthropicSignature || features.AnthropicCache {
		if samePair && ingress == protocol.Anthropic {
			return VerdictLossless
		}
		return VerdictForbidden
	}

	// 通用 function/tools：Responses↔Anthropic 首版 forbidden。
	if features.FunctionTools {
		if samePair {
			if passthroughCapable[ingress] {
				return VerdictLossless
			}
			return VerdictSupportedWithLimits // Chat→Chat 标准转换
		}
		if (ingress == protocol.OpenAIResponse && upstream == protocol.Anthropic) ||
			(ingress == protocol.Anthropic && upstream == protocol.OpenAIResponse) {
			return VerdictForbidden
		}
		return VerdictSupportedWithLimits
	}

	// 纯文本：全组合可转换。
	if samePair && passthroughCapable[ingress] {
		return VerdictLossless
	}
	return VerdictSupportedWithLimits
}

// ModeFor 由判定结果与协议组合导出 ConversionMode。
// 仅当 EvaluateConversion 返回 lossless / supported_with_limits 时有意义。
func ModeFor(ingress, upstream protocol.Protocol, verdict ConversionVerdict) ConversionMode {
	switch verdict {
	case VerdictLegacyFixed:
		return ModeLegacy
	case VerdictForbidden:
		return ModeLegacy // 调用方不应使用；返回 legacy 保守值
	}
	if ingress == upstream {
		if passthroughCapable[ingress] {
			return ModeRawPassthrough
		}
		return ModeNormalized
	}
	return ModeTranslated
}
