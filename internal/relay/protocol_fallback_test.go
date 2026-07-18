package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func TestRunSameChannelAttemptsFallsBackOnceWithoutChangingCandidateIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)

	var mu sync.Mutex
	var paths []string
	var authHeaders []string
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		models = append(models, string(body))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"endpoint_not_found","message":"Responses endpoint is unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "protocol-fallback-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "same-secret"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	group := model.Group{
		Mode: model.GroupModeFailover,
		Items: []model.GroupItem{{
			ChannelID: channel.ID,
			ModelName: "upstream-model",
			Priority:  1,
		}},
	}
	iterator := balancer.NewIterator(group, 7, "alias-model")
	if !iterator.Next() {
		t.Fatal("iterator has no candidate")
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"alias-model","messages":[{"role":"user","content":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	rawBody, internalRequest, inAdapter, err := parseRequest(inbound.InboundTypeOpenAIChat, c)
	if err != nil {
		t.Fatalf("parseRequest failed: %v", err)
	}
	request := &relayRequest{
		c:               c,
		inAdapter:       inAdapter,
		internalRequest: internalRequest,
		metrics:         NewRelayMetrics(7, "alias-model", rawBody, internalRequest),
		apiKeyID:        7,
		requestModel:    "alias-model",
		iter:            iterator,
		rawBody:         rawBody,
	}
	plans := []*protocolroute.AttemptPlan{
		protocolroute.NewAttemptPlan(protocolroute.PlanSpec{
			ChannelID:         channel.ID,
			ChannelKeyID:      channel.Keys[0].ID,
			RequestedModel:    "alias-model",
			UpstreamModel:     "upstream-model",
			IngressProtocol:   protocol.OpenAIChat,
			UpstreamProtocol:  protocol.OpenAIResponse,
			ConversionMode:    protocolroute.ModeTranslated,
			GroupProtocolMode: protocolroute.GroupProtocolAuto,
			BaseURL:           upstream.URL + "/v1",
			AttemptKind:       protocolroute.KindCandidatePrimary,
		}),
		protocolroute.NewAttemptPlan(protocolroute.PlanSpec{
			ChannelID:         channel.ID,
			ChannelKeyID:      channel.Keys[0].ID,
			RequestedModel:    "alias-model",
			UpstreamModel:     "upstream-model",
			IngressProtocol:   protocol.OpenAIChat,
			UpstreamProtocol:  protocol.OpenAIChat,
			ConversionMode:    protocolroute.ModeNormalized,
			GroupProtocolMode: protocolroute.GroupProtocolAuto,
			BaseURL:           upstream.URL + "/v1",
			AttemptKind:       protocolroute.KindSameCandidateProtocolFallback,
		}),
	}

	result := runSameChannelAttempts(c.Request.Context(), request, channel, channel.Keys[0], plans, 0, 1)
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("paths = %v", paths)
	}
	for i := range authHeaders {
		if authHeaders[i] != "Bearer same-secret" {
			t.Fatalf("auth[%d] = %q", i, authHeaders[i])
		}
		if !strings.Contains(models[i], `"model":"upstream-model"`) {
			t.Fatalf("payload[%d] changed upstream model: %s", i, models[i])
		}
	}
	attempts := iterator.Attempts()
	if len(attempts) != 2 {
		t.Fatalf("attempt log count = %d, want 2: %+v", len(attempts), attempts)
	}
	if attempts[0].ProtocolMode != string(protocolroute.GroupProtocolAuto) ||
		attempts[0].IngressProtocol != string(protocol.OpenAIChat) ||
		attempts[0].SelectedProtocol != string(protocol.OpenAIResponse) ||
		attempts[0].AttemptKind != string(protocolroute.KindCandidatePrimary) ||
		attempts[0].FallbackReason != "" {
		t.Fatalf("primary attempt metadata = %+v", attempts[0])
	}
	if attempts[1].ProtocolMode != string(protocolroute.GroupProtocolAuto) ||
		attempts[1].IngressProtocol != string(protocol.OpenAIChat) ||
		attempts[1].SelectedProtocol != string(protocol.OpenAIChat) ||
		attempts[1].AttemptKind != string(protocolroute.KindSameCandidateProtocolFallback) ||
		attempts[1].FallbackReason != protocolroute.FallbackReasonEndpointNotFound {
		t.Fatalf("fallback attempt metadata = %+v", attempts[1])
	}
}
