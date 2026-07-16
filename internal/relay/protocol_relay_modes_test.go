package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func TestObserveRelayExecutesLegacyEndpointOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var legacyHits atomic.Int32
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		legacyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"legacy"}}]}`))
	}))
	defer legacy.Close()
	var profileHits atomic.Int32
	profile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		profileHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","output":[],"status":"completed"}`))
	}))
	defer profile.Close()

	channel := &model.Channel{
		Name:     "observe-legacy-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: legacy.URL + "/v1"}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "observe-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "observe-legacy-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	enableAdaptiveProtocolTestPolicy(t, ctx, channel.ID, group.ID, protocol.OpenAIResponse, profile.URL+"/v1")
	state, err := op.ProtocolPolicyGet(ctx)
	if err != nil {
		t.Fatalf("ProtocolPolicyGet failed: %v", err)
	}
	mode := model.ProtocolRoutingModeObserve
	if _, err := op.ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
		ExpectedRevision: state.ActiveRevision,
		Mode:             &mode,
	}, "test", ctx); err != nil {
		t.Fatalf("set observe mode failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"observe-legacy-group","messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Handler(inbound.InboundTypeOpenAIChat, c)

	if recorder.Code != http.StatusOK || legacyHits.Load() != 1 || profileHits.Load() != 0 {
		t.Fatalf("status=%d legacy=%d profile=%d body=%s", recorder.Code, legacyHits.Load(), profileHits.Load(), recorder.Body.String())
	}
}

func TestAdaptiveRelayGateRejectsUnsafeForcedConversionWithoutDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "must not dispatch", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "adaptive-gate-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "gate-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "adaptive-gate-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	enableAdaptiveProtocolTestPolicy(t, ctx, channel.ID, group.ID, protocol.Anthropic, upstream.URL+"/v1")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"adaptive-gate-group","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Handler(inbound.InboundTypeOpenAIChat, c)

	if hits.Load() != 0 {
		t.Fatalf("unsafe request dispatched %d times", hits.Load())
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdaptiveRelaySelectsNextKeyAfterProtocolIncompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer second-key" {
			http.Error(w, "unexpected key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()
	channel := &model.Channel{
		Name:     "adaptive-key-selection-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "first-key", TotalCost: 0},
			{Enabled: true, ChannelKey: "second-key", TotalCost: 1},
		},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "adaptive-key-selection-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	state, err := op.ProtocolPolicyGet(ctx)
	if err != nil {
		t.Fatalf("ProtocolPolicyGet failed: %v", err)
	}
	state, err = op.ChannelProtocolPolicyReplace(channel.ID, &model.ChannelProtocolPolicyUpdateRequest{
		ExpectedRevision: state.ActiveRevision,
		Overrides: []model.ModelProtocolOverridePolicy{{
			ChannelKeyID:       channel.Keys[0].ID,
			UpstreamModel:      "upstream-model",
			Mode:               model.ProtocolPolicyModeForce,
			PreferredProtocols: []string{string(protocol.Anthropic)},
			Enabled:            true,
		}},
	}, "test", ctx)
	if err != nil {
		t.Fatalf("ChannelProtocolPolicyReplace failed: %v", err)
	}
	enabled := true
	conversionEnabled := true
	mode := model.ProtocolRoutingModeAdaptive
	allowlist := []int{group.ID}
	if _, err := op.ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
		ExpectedRevision:          state.ActiveRevision,
		ProtocolRoutingEnabled:    &enabled,
		Mode:                      &mode,
		ProtocolConversionEnabled: &conversionEnabled,
		AdaptiveGroupAllowlist:    &allowlist,
	}, "test", ctx); err != nil {
		t.Fatalf("ProtocolRoutingConfigUpdate failed: %v", err)
	}

	recorder := executeAdaptiveChatRequest(group.Name, nil)
	if recorder.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status=%d hits=%d body=%s", recorder.Code, hits.Load(), recorder.Body.String())
	}
}
