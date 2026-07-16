package relay

import (
	"context"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestResolveRelayAttemptPlanKeepsObserveOnLegacyExecution(t *testing.T) {
	state := &dbmodel.ProtocolPolicyState{
		ActiveRevision: 4,
		ProtocolPolicyPayload: dbmodel.ProtocolPolicyPayload{
			Config: dbmodel.ProtocolRoutingConfigPolicy{ProtocolRoutingEnabled: true, Mode: dbmodel.ProtocolRoutingModeObserve},
		},
	}
	protocolroute.SetObserveEnabled(true)
	t.Cleanup(func() { protocolroute.SetObserveEnabled(false) })
	snapshot, mode := buildProtocolPolicySnapshot(state, 1)
	request := &transformerModel.InternalLLMRequest{Model: "upstream", RawAPIFormat: transformerModel.APIFormatOpenAIResponse}
	legacy := protocolroute.AttemptConfig{BaseURL: "https://legacy.example.test"}
	profiles := map[protocol.Protocol]protocolroute.AttemptConfig{
		protocol.OpenAIChat:     legacy,
		protocol.OpenAIResponse: {BaseURL: "https://responses.example.test"},
	}
	input := relayPlanInput{
		Snapshot:       snapshot,
		Mode:           mode,
		ChannelID:      3,
		ChannelKeyID:   11,
		ChannelType:    outbound.OutboundTypeOpenAIChat,
		RequestedModel: "alias",
		UpstreamModel:  "upstream",
		Request:        request,
		LegacyConfig:   legacy,
		Profiles:       profiles,
		LegacyEligible: true,
	}

	decision := resolveRelayAttemptPlan(input)
	if decision.Incompatible || decision.Plan == nil {
		t.Fatalf("decision = %+v", decision)
	}
	if !decision.Plan.IsLegacyFixed() || decision.Plan.UpstreamProtocol() != protocol.OpenAIChat {
		t.Fatalf("observe execution plan = %+v", decision.Plan)
	}
}

func TestResolveRelayAttemptPlanAdaptiveUsesSelectedProfile(t *testing.T) {
	snapshot := protocolroute.PolicySnapshot{
		ConfigRevision: 5,
		Mode:           protocolroute.RoutingAdaptive,
		Group:          protocolroute.ScopedRule{Mode: protocolroute.ModeInherit},
		EnabledProfiles: map[int]map[protocol.Protocol]bool{
			3: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true},
		},
	}
	request := &transformerModel.InternalLLMRequest{Model: "upstream", RawAPIFormat: transformerModel.APIFormatOpenAIResponse}
	decision := resolveRelayAttemptPlan(relayPlanInput{
		Snapshot:       snapshot,
		Mode:           protocolroute.RoutingAdaptive,
		ChannelID:      3,
		ChannelKeyID:   11,
		ChannelType:    outbound.OutboundTypeOpenAIChat,
		RequestedModel: "alias",
		UpstreamModel:  "upstream",
		Request:        request,
		LegacyConfig:   protocolroute.AttemptConfig{BaseURL: "https://legacy.example.test"},
		Profiles: map[protocol.Protocol]protocolroute.AttemptConfig{
			protocol.OpenAIChat:     {BaseURL: "https://legacy.example.test"},
			protocol.OpenAIResponse: {BaseURL: "https://responses.example.test"},
		},
		LegacyEligible: true,
	})
	if decision.Incompatible || decision.Plan == nil {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Plan.UpstreamProtocol() != protocol.OpenAIResponse || decision.Plan.BaseURL() != "https://responses.example.test" {
		t.Fatalf("adaptive plan protocol=%q base=%q", decision.Plan.UpstreamProtocol(), decision.Plan.BaseURL())
	}
}

func TestNewRelayAttemptClonesRequestAndUsesPlanAdapter(t *testing.T) {
	request := &transformerModel.InternalLLMRequest{Model: "original", RawAPIFormat: transformerModel.APIFormatOpenAIChatCompletion}
	relayRequest := &relayRequest{ctx: context.Background(), internalRequest: request}
	plan := protocolroute.NewAttemptPlan(protocolroute.PlanSpec{
		ChannelID:        3,
		ChannelKeyID:     11,
		UpstreamModel:    "rewritten",
		UpstreamProtocol: protocol.Anthropic,
	})
	attempt, err := newRelayAttempt(relayRequest, &dbmodel.Channel{ID: 3}, dbmodel.ChannelKey{ID: 11}, plan, 9)
	if err != nil {
		t.Fatalf("newRelayAttempt() error = %v", err)
	}
	if attempt.internalRequest == request || attempt.internalRequest.Model != "rewritten" || request.Model != "original" {
		t.Fatalf("request clone original=%q attempt=%q same=%t", request.Model, attempt.internalRequest.Model, attempt.internalRequest == request)
	}
	if attempt.upstreamProtocol != protocol.Anthropic || attempt.outAdapter == nil {
		t.Fatalf("attempt protocol=%q adapter=%T", attempt.upstreamProtocol, attempt.outAdapter)
	}
}

func TestBuildProtocolPolicySnapshotMapsAllowlistedGroupAndOverrides(t *testing.T) {
	state := &dbmodel.ProtocolPolicyState{
		ActiveRevision: 12,
		ProtocolPolicyPayload: dbmodel.ProtocolPolicyPayload{
			Config: dbmodel.ProtocolRoutingConfigPolicy{
				ProtocolRoutingEnabled: true,
				Mode:                   dbmodel.ProtocolRoutingModeAdaptive,
				AdaptiveGroupAllowlist: []int{7},
			},
			Groups: []dbmodel.GroupProtocolPolicy{{
				GroupID:            7,
				Mode:               dbmodel.ProtocolPolicyModePrefer,
				PreferredProtocols: []string{string(protocol.Anthropic)},
			}},
			Channels: []dbmodel.ChannelProtocolPolicy{{
				ChannelID: 3,
				Profiles: []dbmodel.ProtocolProfilePolicy{{
					Protocol: string(protocol.Anthropic),
					Enabled:  true,
				}},
				Overrides: []dbmodel.ModelProtocolOverridePolicy{
					{ChannelKeyID: 0, UpstreamModel: "model-a", Mode: dbmodel.ProtocolPolicyModePrefer, PreferredProtocols: []string{string(protocol.OpenAIResponse)}, Enabled: true},
					{ChannelKeyID: 11, UpstreamModel: "model-a", Mode: dbmodel.ProtocolPolicyModeForce, PreferredProtocols: []string{string(protocol.Anthropic)}, Enabled: true},
				},
			}},
		},
	}

	snapshot, mode := buildProtocolPolicySnapshot(state, 7)
	if mode != protocolroute.RoutingAdaptive {
		t.Fatalf("mode = %q", mode)
	}
	if snapshot.ConfigRevision != 12 {
		t.Fatalf("revision = %d", snapshot.ConfigRevision)
	}
	if snapshot.Group.Mode != protocolroute.ModePrefer || snapshot.Group.Protocols[0] != protocol.Anthropic {
		t.Fatalf("group rule = %+v", snapshot.Group)
	}
	keyRule := snapshot.KeyModelRules[protocolroute.KeyModelScopeKey{ChannelKeyID: 11, UpstreamModel: "model-a"}]
	if keyRule.Mode != protocolroute.ModeForce || keyRule.Protocols[0] != protocol.Anthropic {
		t.Fatalf("key rule = %+v", keyRule)
	}
	channelRule := snapshot.ChanModelRules[protocolroute.ChanModelScopeKey{ChannelID: 3, UpstreamModel: "model-a"}]
	if channelRule.Mode != protocolroute.ModePrefer || channelRule.Protocols[0] != protocol.OpenAIResponse {
		t.Fatalf("channel rule = %+v", channelRule)
	}
}

func TestBuildProtocolPolicySnapshotDowngradesNonAllowlistedGroupToObserve(t *testing.T) {
	state := &dbmodel.ProtocolPolicyState{
		ProtocolPolicyPayload: dbmodel.ProtocolPolicyPayload{
			Config: dbmodel.ProtocolRoutingConfigPolicy{
				ProtocolRoutingEnabled: true,
				Mode:                   dbmodel.ProtocolRoutingModeAdaptive,
				AdaptiveGroupAllowlist: []int{8},
			},
		},
	}

	snapshot, mode := buildProtocolPolicySnapshot(state, 7)
	if mode != protocolroute.RoutingObserve || snapshot.Mode != protocolroute.RoutingObserve {
		t.Fatalf("mode = %q snapshot = %q", mode, snapshot.Mode)
	}
}

func TestBuildAttemptConfigsUsesProfileAndLegacyPrecedence(t *testing.T) {
	legacyOverride := `{"legacy":true}`
	profileOverride := `{"profile":true}`
	channel := &dbmodel.Channel{
		Type:          outbound.OutboundTypeOpenAIChat,
		BaseUrls:      []dbmodel.BaseUrl{{URL: "https://legacy.example.test", Delay: 1}},
		CustomHeader:  []dbmodel.CustomHeader{{HeaderKey: "X-Shared", HeaderValue: "channel"}, {HeaderKey: "X-Channel", HeaderValue: "1"}},
		ParamOverride: &legacyOverride,
	}
	policy := dbmodel.ChannelProtocolPolicy{
		Profiles: []dbmodel.ProtocolProfilePolicy{{
			Protocol:      string(protocol.Anthropic),
			Enabled:       true,
			BaseUrls:      []dbmodel.BaseUrl{{URL: "https://slow.example.test", Delay: 20}, {URL: "https://fast.example.test", Delay: 2}},
			CustomHeaders: []dbmodel.CustomHeader{{HeaderKey: "X-Shared", HeaderValue: "profile"}, {HeaderKey: "X-Profile", HeaderValue: "1"}},
			ParamOverride: &profileOverride,
		}},
	}

	legacy, profiles := buildAttemptConfigs(channel, policy)
	if legacy.BaseURL != "https://legacy.example.test" || string(legacy.ParamOverride) != legacyOverride {
		t.Fatalf("legacy config = %+v", legacy)
	}
	anthropic := profiles[protocol.Anthropic]
	if anthropic.BaseURL != "https://fast.example.test" {
		t.Fatalf("profile base URL = %q", anthropic.BaseURL)
	}
	if anthropic.HeaderPolicy.Set["X-Shared"] != "profile" || anthropic.HeaderPolicy.Set["X-Channel"] != "1" {
		t.Fatalf("profile headers = %+v", anthropic.HeaderPolicy.Set)
	}
	if string(anthropic.ParamOverride) != profileOverride {
		t.Fatalf("profile override = %s", anthropic.ParamOverride)
	}
}
