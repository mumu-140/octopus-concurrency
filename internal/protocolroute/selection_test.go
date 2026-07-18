package protocolroute

import (
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/protocol"
)

func TestResolveProtocolSelectionFollowsChannelWhenDisabledOrFollow(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		mode    GroupProtocolMode
	}{
		{name: "kill switch disabled", enabled: false, mode: GroupProtocolOverride},
		{name: "group follows channel", enabled: true, mode: GroupProtocolFollow},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveProtocolSelection(ProtocolSelectionInput{
				Enabled:            test.enabled,
				Mode:               test.mode,
				ChannelProtocol:    protocol.OpenAIChat,
				IngressProtocol:    protocol.OpenAIResponse,
				PreferredProtocols: []protocol.Protocol{protocol.Anthropic},
			})
			assertProtocolSelection(t, got, GroupProtocolFollow, protocol.OpenAIChat)
		})
	}
}

func TestResolveProtocolSelectionOverrideUsesFirstTwoExecutableProtocols(t *testing.T) {
	preferred := []protocol.Protocol{
		protocol.Unknown,
		protocol.Anthropic,
		protocol.Anthropic,
		protocol.OpenAIResponse,
		protocol.OpenAIChat,
	}
	wantInput := append([]protocol.Protocol(nil), preferred...)

	got := ResolveProtocolSelection(ProtocolSelectionInput{
		Enabled:            true,
		Mode:               GroupProtocolOverride,
		ChannelProtocol:    protocol.OpenAIChat,
		IngressProtocol:    protocol.OpenAIChat,
		PreferredProtocols: preferred,
		Features:           RequestFeatureFlags{PlainText: true},
	})

	assertProtocolSelection(t, got, GroupProtocolOverride, protocol.Anthropic, protocol.OpenAIResponse)
	if !reflect.DeepEqual(preferred, wantInput) {
		t.Fatalf("preferred protocols mutated: got %v, want %v", preferred, wantInput)
	}
}

func TestResolveProtocolSelectionAutoKeepsChannelAndUsesFirstDifferentFallback(t *testing.T) {
	got := ResolveProtocolSelection(ProtocolSelectionInput{
		Enabled:         true,
		Mode:            GroupProtocolAuto,
		ChannelProtocol: protocol.OpenAIChat,
		IngressProtocol: protocol.OpenAIChat,
		PreferredProtocols: []protocol.Protocol{
			protocol.OpenAIChat,
			protocol.Unknown,
			protocol.OpenAIResponse,
			protocol.Anthropic,
		},
		Features: RequestFeatureFlags{PlainText: true},
	})

	assertProtocolSelection(t, got, GroupProtocolAuto, protocol.OpenAIChat, protocol.OpenAIResponse)
}

func TestResolveProtocolSelectionUnsupportedFeaturesAreFollowOnly(t *testing.T) {
	tests := []struct {
		name     string
		features RequestFeatureFlags
	}{
		{name: "multimodal", features: RequestFeatureFlags{Multimodal: true}},
		{name: "embedding", features: RequestFeatureFlags{Embedding: true}},
		{name: "reasoning", features: RequestFeatureFlags{Reasoning: true}},
		{name: "responses continuation", features: RequestFeatureFlags{Continuation: true}},
		{name: "anthropic signature", features: RequestFeatureFlags{AnthropicSignature: true}},
		{name: "passthrough pinned", features: RequestFeatureFlags{PassthroughPinned: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveProtocolSelection(ProtocolSelectionInput{
				Enabled:            true,
				Mode:               GroupProtocolOverride,
				ChannelProtocol:    protocol.OpenAIChat,
				IngressProtocol:    protocol.OpenAIChat,
				PreferredProtocols: []protocol.Protocol{protocol.Anthropic, protocol.OpenAIResponse},
				Features:           test.features,
			})
			assertProtocolSelection(t, got, GroupProtocolFollow, protocol.OpenAIChat)
		})
	}
}

func TestResolveProtocolSelectionFiltersUnsafeToolConversion(t *testing.T) {
	got := ResolveProtocolSelection(ProtocolSelectionInput{
		Enabled:         true,
		Mode:            GroupProtocolAuto,
		ChannelProtocol: protocol.OpenAIResponse,
		IngressProtocol: protocol.OpenAIResponse,
		PreferredProtocols: []protocol.Protocol{
			protocol.Anthropic,
			protocol.OpenAIChat,
		},
		Features: RequestFeatureFlags{FunctionTools: true},
	})

	assertProtocolSelection(t, got, GroupProtocolAuto, protocol.OpenAIResponse, protocol.OpenAIChat)
}

func TestResolveProtocolSelectionInvalidModeFollowsChannel(t *testing.T) {
	got := ResolveProtocolSelection(ProtocolSelectionInput{
		Enabled:            true,
		Mode:               GroupProtocolMode("invalid"),
		ChannelProtocol:    protocol.Gemini,
		IngressProtocol:    protocol.OpenAIChat,
		PreferredProtocols: []protocol.Protocol{protocol.Anthropic},
	})

	assertProtocolSelection(t, got, GroupProtocolFollow, protocol.Gemini)
}

func assertProtocolSelection(t *testing.T, got ProtocolSelection, mode GroupProtocolMode, protocols ...protocol.Protocol) {
	t.Helper()
	if got.Mode != mode {
		t.Fatalf("mode = %q, want %q", got.Mode, mode)
	}
	if !reflect.DeepEqual(got.Protocols, protocols) {
		t.Fatalf("protocols = %v, want %v", got.Protocols, protocols)
	}
}
