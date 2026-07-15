package relay

import (
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

// observeProtocolDecision 是 legacy relay 中的 observe 影子决策接入点（T08）。
//
// 约束：
//   - 只记录，不改变请求或候选选择；调用后 relay 控制流与调用前完全一致；
//   - observe 关闭（默认）时零成本返回；
//   - 影子解析 panic 由 protocolroute 内部吞掉，不影响请求。
func observeProtocolDecision(
	channelType outbound.OutboundType,
	channelID, channelKeyID int,
	requestedModel, upstreamModel string,
	internalRequest *model.InternalLLMRequest,
	legacyEligible bool,
) {
	if !protocolroute.ObserveEnabled() {
		return
	}
	in := protocolroute.ShadowInputFromLegacy(
		channelID, channelKeyID,
		protocol.FromOutboundType(channelType),
		requestedModel, upstreamModel,
		protocol.FromAPIFormat(internalRequest.RawAPIFormat),
		protocolroute.CaptureFeatures(internalRequest),
		legacyEligible,
	)
	protocolroute.ObserveShadowDecision(in)
}
