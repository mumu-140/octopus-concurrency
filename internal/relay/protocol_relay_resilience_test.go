package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func TestAdaptiveRelayPreservesSameChannelRetryBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":"failed"}`, http.StatusInternalServerError)
	}))
	defer upstream.Close()
	groupName := setupAdaptiveChatRoute(t, ctx, upstream.URL+"/v1", true, 2)

	recorder := executeAdaptiveChatRequest(groupName, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits=%d want=2", hits.Load())
	}
}

func TestAdaptiveRelayPreservesRetryAfterErrorClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "3")
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer upstream.Close()
	groupName := setupAdaptiveChatRoute(t, ctx, upstream.URL+"/v1", false, 1)

	recorder := executeAdaptiveChatRequest(groupName, nil)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "3" {
		t.Fatalf("status=%d retry-after=%q body=%s", recorder.Code, recorder.Header().Get("Retry-After"), recorder.Body.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits=%d", hits.Load())
	}
}

func TestAdaptiveRelayStopsOnClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbContext := setupRelayTestDB(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		upstream.Close()
	}()
	groupName := setupAdaptiveChatRoute(t, dbContext, upstream.URL+"/v1", false, 1)

	requestContext, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		executeAdaptiveChatRequest(groupName, requestContext)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not stop after cancellation")
	}
}

func setupAdaptiveChatRoute(t *testing.T, ctx context.Context, baseURL string, retryEnabled bool, maxRetries int) string {
	t.Helper()
	channel := &model.Channel{
		Name:     "adaptive-resilience-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: baseURL}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "resilience-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "adaptive-resilience-group", Mode: model.GroupModeFailover, RetryEnabled: retryEnabled, MaxRetries: maxRetries}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}
	enableAdaptiveProtocolTestPolicy(t, ctx, channel.ID, group.ID, protocol.OpenAIChat, baseURL)
	return group.Name
}

func executeAdaptiveChatRequest(groupName string, ctx context.Context) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+groupName+`","messages":[{"role":"user","content":"hello"}]}`))
	if ctx != nil {
		request = request.WithContext(ctx)
	}
	request.Header.Set("Content-Type", "application/json")
	c.Request = request
	Handler(inbound.InboundTypeOpenAIChat, c)
	return recorder
}
