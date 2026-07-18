package relay

import (
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestBuildGroupProtocolPlansUsesOnlyGroupAndChannelPolicy(t *testing.T) {
	paramOverride := `{"temperature":0.2}`
	channel := &model.Channel{
		ID:            9,
		Type:          outbound.OutboundTypeOpenAIChat,
		BaseUrls:      []model.BaseUrl{{URL: "https://upstream.example.test/v1"}},
		CustomHeader:  []model.CustomHeader{{HeaderKey: "X-Route", HeaderValue: "shared"}},
		ParamOverride: &paramOverride,
	}
	key := model.ChannelKey{ID: 12, ChannelKey: "same-secret"}
	request := &transformerModel.InternalLLMRequest{
		Model:        "upstream-model",
		RawAPIFormat: transformerModel.APIFormatOpenAIChatCompletion,
	}

	tests := []struct {
		name            string
		enabled         bool
		mode            model.ProtocolPolicyMode
		preferred       []string
		features        protocolroute.RequestFeatureFlags
		wantMode        protocolroute.GroupProtocolMode
		wantProtocols   []protocol.Protocol
		wantLegacyFirst bool
	}{
		{
			name:            "global switch disabled",
			mode:            model.ProtocolPolicyModeAuto,
			preferred:       []string{string(protocol.OpenAIResponse)},
			wantMode:        protocolroute.GroupProtocolFollow,
			wantProtocols:   []protocol.Protocol{protocol.OpenAIChat},
			wantLegacyFirst: true,
		},
		{
			name:            "group follows channel",
			enabled:         true,
			mode:            model.ProtocolPolicyModeFollow,
			preferred:       []string{string(protocol.Anthropic)},
			wantMode:        protocolroute.GroupProtocolFollow,
			wantProtocols:   []protocol.Protocol{protocol.OpenAIChat},
			wantLegacyFirst: true,
		},
		{
			name:            "automatic fallback",
			enabled:         true,
			mode:            model.ProtocolPolicyModeAuto,
			preferred:       []string{string(protocol.OpenAIChat), string(protocol.OpenAIResponse)},
			wantMode:        protocolroute.GroupProtocolAuto,
			wantProtocols:   []protocol.Protocol{protocol.OpenAIChat, protocol.OpenAIResponse},
			wantLegacyFirst: true,
		},
		{
			name:          "group override",
			enabled:       true,
			mode:          model.ProtocolPolicyModeOverride,
			preferred:     []string{string(protocol.Anthropic), string(protocol.OpenAIResponse), string(protocol.OpenAIChat)},
			wantMode:      protocolroute.GroupProtocolOverride,
			wantProtocols: []protocol.Protocol{protocol.Anthropic, protocol.OpenAIResponse},
		},
		{
			name:            "reasoning stays on channel",
			enabled:         true,
			mode:            model.ProtocolPolicyModeOverride,
			preferred:       []string{string(protocol.Anthropic), string(protocol.OpenAIResponse)},
			features:        protocolroute.RequestFeatureFlags{Reasoning: true},
			wantMode:        protocolroute.GroupProtocolFollow,
			wantProtocols:   []protocol.Protocol{protocol.OpenAIChat},
			wantLegacyFirst: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group := model.Group{ID: 4, ProtocolMode: test.mode, PreferredProtocols: test.preferred}
			plans := buildGroupProtocolPlans(groupProtocolPlanInput{
				Enabled:        test.enabled,
				Group:          group,
				Channel:        channel,
				Key:            key,
				RequestedModel: "alias-model",
				UpstreamModel:  "upstream-model",
				Request:        request,
				Features:       test.features,
				LegacyEligible: true,
			})

			gotProtocols := make([]protocol.Protocol, 0, len(plans))
			for _, plan := range plans {
				gotProtocols = append(gotProtocols, plan.UpstreamProtocol())
				if plan.GroupProtocolMode() != test.wantMode {
					t.Fatalf("plan mode = %q, want %q", plan.GroupProtocolMode(), test.wantMode)
				}
			}
			if !reflect.DeepEqual(gotProtocols, test.wantProtocols) {
				t.Fatalf("protocols = %v, want %v", gotProtocols, test.wantProtocols)
			}
			if len(plans) > 0 && plans[0].IsLegacyFixed() != test.wantLegacyFirst {
				t.Fatalf("first legacy = %t, want %t", plans[0].IsLegacyFixed(), test.wantLegacyFirst)
			}
			if len(plans) > 1 && len(plans[1].ParamOverride()) != 0 {
				t.Fatalf("cross-protocol fallback inherited ParamOverride: %s", plans[1].ParamOverride())
			}
		})
	}
}
