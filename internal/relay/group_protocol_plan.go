package relay

import (
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

type groupProtocolPlanInput struct {
	Enabled        bool
	Group          model.Group
	Channel        *model.Channel
	Key            model.ChannelKey
	RequestedModel string
	UpstreamModel  string
	Request        *transformerModel.InternalLLMRequest
	Features       protocolroute.RequestFeatureFlags
	LegacyEligible bool
}

func buildGroupProtocolPlans(input groupProtocolPlanInput) []*protocolroute.AttemptPlan {
	if input.Channel == nil || input.Request == nil {
		return nil
	}
	features := mergeRequestFeatures(protocolroute.CaptureFeatures(input.Request), input.Features)
	channelProtocol := protocol.FromOutboundType(input.Channel.Type)
	selection := protocolroute.ResolveProtocolSelection(protocolroute.ProtocolSelectionInput{
		Enabled:            input.Enabled,
		Mode:               groupProtocolMode(input.Group.ProtocolMode),
		ChannelProtocol:    channelProtocol,
		IngressProtocol:    protocol.FromAPIFormat(input.Request.RawAPIFormat),
		PreferredProtocols: protocolValues(input.Group.PreferredProtocols),
		Features:           features,
	})
	if selection.Mode == protocolroute.GroupProtocolFollow && !input.LegacyEligible {
		return nil
	}

	plans := make([]*protocolroute.AttemptPlan, 0, len(selection.Protocols))
	for index, selected := range selection.Protocols {
		plans = append(plans, groupAttemptPlan(input, selection.Mode, features, selected, index))
	}
	return plans
}

func groupAttemptPlan(
	input groupProtocolPlanInput,
	mode protocolroute.GroupProtocolMode,
	features protocolroute.RequestFeatureFlags,
	selected protocol.Protocol,
	index int,
) *protocolroute.AttemptPlan {
	ingress := protocol.FromAPIFormat(input.Request.RawAPIFormat)
	channelProtocol := protocol.FromOutboundType(input.Channel.Type)
	spec := protocolroute.PlanSpec{
		ChannelID:         input.Channel.ID,
		ChannelKeyID:      input.Key.ID,
		RequestedModel:    input.RequestedModel,
		UpstreamModel:     input.UpstreamModel,
		IngressProtocol:   ingress,
		UpstreamProtocol:  selected,
		Features:          features,
		BaseURL:           input.Channel.GetBaseUrl(),
		HeaderPolicy:      protocolroute.HeaderPolicy{Set: customHeaderMap(input.Channel.CustomHeader)},
		PolicySource:      "group_" + string(mode),
		GroupProtocolMode: mode,
		AttemptKind:       protocolroute.KindCandidatePrimary,
	}
	if index > 0 {
		spec.AttemptKind = protocolroute.KindSameCandidateProtocolFallback
	}
	if selected == channelProtocol {
		spec.ParamOverride = rawParamOverride(input.Channel.ParamOverride)
		return protocolroute.NewLegacyFixedAttempt(spec)
	}
	verdict := protocolroute.EvaluateConversion(ingress, selected, features)
	spec.ConversionMode = protocolroute.ModeFor(ingress, selected, verdict)
	return protocolroute.NewAttemptPlan(spec)
}

func groupProtocolMode(mode model.ProtocolPolicyMode) protocolroute.GroupProtocolMode {
	switch mode {
	case model.ProtocolPolicyModeOverride:
		return protocolroute.GroupProtocolOverride
	case model.ProtocolPolicyModeAuto:
		return protocolroute.GroupProtocolAuto
	default:
		return protocolroute.GroupProtocolFollow
	}
}

func protocolValues(values []string) []protocol.Protocol {
	protocols := make([]protocol.Protocol, 0, len(values))
	for _, value := range values {
		protocols = append(protocols, protocol.Protocol(value))
	}
	return protocols
}

func mergeRequestFeatures(left, right protocolroute.RequestFeatureFlags) protocolroute.RequestFeatureFlags {
	return protocolroute.RequestFeatureFlags{
		PlainText:          left.PlainText || right.PlainText,
		FunctionTools:      left.FunctionTools || right.FunctionTools,
		ResponsesNative:    left.ResponsesNative || right.ResponsesNative,
		Continuation:       left.Continuation || right.Continuation,
		Multimodal:         left.Multimodal || right.Multimodal,
		Reasoning:          left.Reasoning || right.Reasoning,
		AnthropicSignature: left.AnthropicSignature || right.AnthropicSignature,
		AnthropicCache:     left.AnthropicCache || right.AnthropicCache,
		Streaming:          left.Streaming || right.Streaming,
		Embedding:          left.Embedding || right.Embedding,
		PassthroughPinned:  left.PassthroughPinned || right.PassthroughPinned,
	}
}
