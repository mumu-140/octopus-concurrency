package protocolroute

import "github.com/bestruirui/octopus/internal/protocol"

// GroupProtocolMode is the complete group-level protocol policy surface.
type GroupProtocolMode string

const (
	GroupProtocolFollow   GroupProtocolMode = "follow"
	GroupProtocolOverride GroupProtocolMode = "override"
	GroupProtocolAuto     GroupProtocolMode = "auto"
)

// ProtocolSelectionInput contains only the immutable values needed to select
// protocols for one physical channel candidate.
type ProtocolSelectionInput struct {
	Enabled            bool
	Mode               GroupProtocolMode
	ChannelProtocol    protocol.Protocol
	IngressProtocol    protocol.Protocol
	PreferredProtocols []protocol.Protocol
	Features           RequestFeatureFlags
}

// ProtocolSelection is an ordered primary and optional fallback protocol pair.
type ProtocolSelection struct {
	Mode      GroupProtocolMode
	Protocols []protocol.Protocol
}

// ResolveProtocolSelection returns at most two deterministic protocols without
// mutating the input or reading channel-level policy.
func ResolveProtocolSelection(in ProtocolSelectionInput) ProtocolSelection {
	if !in.Enabled || in.Mode == GroupProtocolFollow || fallbackUnsafe(in.Features) {
		return followSelection(in.ChannelProtocol)
	}

	switch in.Mode {
	case GroupProtocolOverride:
		protocols := executableProtocols(in, "", 2)
		if len(protocols) == 0 {
			return followSelection(in.ChannelProtocol)
		}
		return ProtocolSelection{Mode: GroupProtocolOverride, Protocols: protocols}
	case GroupProtocolAuto:
		fallbacks := executableProtocols(in, in.ChannelProtocol, 1)
		protocols := []protocol.Protocol{in.ChannelProtocol}
		protocols = append(protocols, fallbacks...)
		return ProtocolSelection{Mode: GroupProtocolAuto, Protocols: protocols}
	default:
		return followSelection(in.ChannelProtocol)
	}
}

func followSelection(channelProtocol protocol.Protocol) ProtocolSelection {
	return ProtocolSelection{
		Mode:      GroupProtocolFollow,
		Protocols: []protocol.Protocol{channelProtocol},
	}
}

func executableProtocols(in ProtocolSelectionInput, excluded protocol.Protocol, limit int) []protocol.Protocol {
	result := make([]protocol.Protocol, 0, limit)
	seen := make(map[protocol.Protocol]struct{}, len(in.PreferredProtocols))
	for _, candidate := range in.PreferredProtocols {
		if candidate == excluded || !protocolExecutable(candidate, in) {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
		if len(result) == limit {
			break
		}
	}
	return result
}

func protocolExecutable(candidate protocol.Protocol, in ProtocolSelectionInput) bool {
	if !candidate.IsAdaptive() {
		return false
	}
	if _, ok := candidate.ToOutboundType(); !ok {
		return false
	}
	verdict := EvaluateConversion(in.IngressProtocol, candidate, in.Features)
	return verdict != VerdictForbidden && verdict != VerdictLegacyFixed
}

func fallbackUnsafe(features RequestFeatureFlags) bool {
	return features.Multimodal ||
		features.Embedding ||
		features.Reasoning ||
		features.ResponsesNative ||
		features.Continuation ||
		features.AnthropicSignature ||
		features.AnthropicCache ||
		features.PassthroughPinned
}
