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

func TestDisabledRoutingUsesChannelDefaultOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected path", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"legacy"}}]}`))
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "disabled-routing-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "disabled-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "disabled-routing-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	mode := model.ProtocolPolicyModeOverride
	preferred := []string{string(protocol.OpenAIResponse)}
	if _, err := op.GroupUpdate(&model.GroupUpdateRequest{ID: group.ID, ProtocolMode: &mode, PreferredProtocols: &preferred}, ctx); err != nil {
		t.Fatalf("GroupUpdate failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"disabled-routing-group","messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Handler(inbound.InboundTypeOpenAIChat, c)

	if recorder.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status=%d hits=%d body=%s", recorder.Code, hits.Load(), recorder.Body.String())
	}
}

func TestGroupRelayRejectsUnsafeForcedConversionWithoutDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		paths = append(paths, r.URL.Path)
		http.Error(w, "must not convert", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "group-gate-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "gate-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "group-gate-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	enableGroupProtocolPolicy(t, ctx, group.ID, model.ProtocolPolicyModeOverride, protocol.Anthropic)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"group-gate-group","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Handler(inbound.InboundTypeOpenAIChat, c)

	if hits.Load() != 1 || (len(paths) == 1 && paths[0] != "/v1/chat/completions") {
		t.Fatalf("hits=%d paths=%v, want one channel-default chat attempt", hits.Load(), paths)
	}
	if recorder.Code == http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupRelayUsesFirstAvailableKey(t *testing.T) {
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
		Name:     "group-key-selection-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "second-key", TotalCost: 1},
		},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "group-key-selection-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	enableGroupProtocolPolicy(t, ctx, group.ID, model.ProtocolPolicyModeFollow)

	recorder := executeAdaptiveChatRequest(group.Name, nil)
	if recorder.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status=%d hits=%d body=%s", recorder.Code, hits.Load(), recorder.Body.String())
	}
}
