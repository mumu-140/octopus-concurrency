package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/apperror"
	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestChannelProtocolPolicyReplaceCommitsProfilesAndOverrides(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	channel, key := createProtocolRouteTestChannel(t, "primary", outbound.OutboundTypeOpenAIChat)

	paramOverride := `{"temperature":0.2}`
	state, err := ChannelProtocolPolicyReplace(channel.ID, &model.ChannelProtocolPolicyUpdateRequest{
		ExpectedRevision: 0,
		Profiles: []model.ProtocolProfilePolicy{{
			Protocol:      "openai_response",
			Enabled:       true,
			BaseUrls:      []model.BaseUrl{{URL: "https://responses.example/v1"}},
			CustomHeaders: []model.CustomHeader{{HeaderKey: "X-Route", HeaderValue: "responses"}},
			ParamOverride: &paramOverride,
		}},
		Overrides: []model.ModelProtocolOverridePolicy{{
			ChannelKeyID:       key.ID,
			UpstreamModel:      "gpt-5.6",
			Mode:               model.ProtocolPolicyModeForce,
			PreferredProtocols: []string{"openai_response"},
			Enabled:            true,
		}},
	}, "admin", ctx)
	if err != nil {
		t.Fatalf("replace channel policy: %v", err)
	}
	if state.ActiveRevision != 1 {
		t.Fatalf("active revision=%d, want 1", state.ActiveRevision)
	}

	policy, err := ChannelProtocolPolicyGet(channel.ID, ctx)
	if err != nil {
		t.Fatalf("get channel policy: %v", err)
	}
	if len(policy.Profiles) != 1 || policy.Profiles[0].Protocol != "openai_response" {
		t.Fatalf("unexpected profiles: %+v", policy.Profiles)
	}
	if len(policy.Overrides) != 1 || policy.Overrides[0].ChannelKeyID != key.ID {
		t.Fatalf("unexpected overrides: %+v", policy.Overrides)
	}
	assertProtocolRevisionIntegrity(t, ctx, 1, 1)
}

func TestChannelProtocolPolicyReplaceRejectsInvalidInputWithoutMutation(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	channel, _ := createProtocolRouteTestChannel(t, "primary", outbound.OutboundTypeOpenAIChat)
	other, otherKey := createProtocolRouteTestChannel(t, "other", outbound.OutboundTypeOpenAIChat)
	_ = other

	tests := []struct {
		name string
		req  model.ChannelProtocolPolicyUpdateRequest
	}{
		{
			name: "duplicate profile",
			req: model.ChannelProtocolPolicyUpdateRequest{Profiles: []model.ProtocolProfilePolicy{
				{Protocol: "openai_response", Enabled: true, BaseUrls: []model.BaseUrl{{URL: "https://one.example/v1"}}},
				{Protocol: "openai_response", Enabled: true, BaseUrls: []model.BaseUrl{{URL: "https://two.example/v1"}}},
			}},
		},
		{
			name: "credential header",
			req: model.ChannelProtocolPolicyUpdateRequest{Profiles: []model.ProtocolProfilePolicy{{
				Protocol: "anthropic", Enabled: true, BaseUrls: []model.BaseUrl{{URL: "https://anthropic.example/v1"}},
				CustomHeaders: []model.CustomHeader{{HeaderKey: "Authorization", HeaderValue: "secret"}},
			}}},
		},
		{
			name: "foreign channel key",
			req: model.ChannelProtocolPolicyUpdateRequest{Overrides: []model.ModelProtocolOverridePolicy{{
				ChannelKeyID: otherKey.ID, UpstreamModel: "gpt-5.6", Mode: model.ProtocolPolicyModeForce,
				PreferredProtocols: []string{"openai_response"}, Enabled: true,
			}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.req.ExpectedRevision = 0
			_, err := ChannelProtocolPolicyReplace(channel.ID, &test.req, "admin", ctx)
			if apperror.Code(err) != CodeProtocolRoutingValidation {
				t.Fatalf("expected validation error, got %v (%s)", err, apperror.Code(err))
			}
		})
	}

	policy, err := ChannelProtocolPolicyGet(channel.ID, ctx)
	if err != nil {
		t.Fatalf("get channel policy: %v", err)
	}
	if len(policy.Profiles) != 0 || len(policy.Overrides) != 0 {
		t.Fatalf("invalid requests mutated policy: %+v", policy)
	}
	assertProtocolRevisionIntegrity(t, ctx, 0, 0)
}

func TestChannelProtocolPolicyReplaceRollsBackWhenRevisionInsertFails(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	channel, _ := createProtocolRouteTestChannel(t, "primary", outbound.OutboundTypeOpenAIChat)
	original := model.ChannelProtocolProfile{
		ChannelID: channel.ID,
		Protocol:  "openai_response",
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: "https://original.example/v1"}},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&original).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := dbpkg.GetDB().WithContext(ctx).Migrator().DropTable(&model.ProtocolPolicyRevision{}); err != nil {
		t.Fatalf("drop revision table: %v", err)
	}

	_, err := ChannelProtocolPolicyReplace(channel.ID, &model.ChannelProtocolPolicyUpdateRequest{
		ExpectedRevision: 0,
		Profiles: []model.ProtocolProfilePolicy{{
			Protocol: "anthropic", Enabled: true, BaseUrls: []model.BaseUrl{{URL: "https://new.example/v1"}},
		}},
	}, "admin", ctx)
	if apperror.Code(err) != CodeProtocolRoutingDatabase {
		t.Fatalf("expected database error, got %v (%s)", err, apperror.Code(err))
	}

	var profiles []model.ChannelProtocolProfile
	if err := dbpkg.GetDB().WithContext(ctx).Where("channel_id = ?", channel.ID).Find(&profiles).Error; err != nil {
		t.Fatalf("load profiles after rollback: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Protocol != "openai_response" {
		t.Fatalf("transaction did not roll back: %+v", profiles)
	}
}

func TestChannelProtocolPolicyGetReturnsNotFound(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	_, err := ChannelProtocolPolicyGet(9999, ctx)
	if apperror.Code(err) != CodeProtocolRoutingNotFound {
		t.Fatalf("expected not found, got %v (%s)", err, apperror.Code(err))
	}
}

func createProtocolRouteTestChannel(t *testing.T, name string, channelType outbound.OutboundType) (*model.Channel, *model.ChannelKey) {
	t.Helper()
	channel := &model.Channel{Name: name, Type: channelType, Enabled: true}
	if err := dbpkg.GetDB().Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := &model.ChannelKey{ChannelID: channel.ID, Enabled: true, ChannelKey: "test-key"}
	if err := dbpkg.GetDB().Create(key).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}
	return channel, key
}
