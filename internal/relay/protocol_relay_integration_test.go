package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

type capturedProtocolRequest struct {
	path   string
	header http.Header
	body   []byte
}

func TestGroupRelayExecutesSelectedProtocols(t *testing.T) {
	tests := []struct {
		name           string
		protocol       protocol.Protocol
		wantPath       string
		wantAuthHeader string
		response       string
	}{
		{
			name:           "openai_chat",
			protocol:       protocol.OpenAIChat,
			wantPath:       "/v1/chat/completions",
			wantAuthHeader: "Authorization",
			response:       `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name:           "openai_response",
			protocol:       protocol.OpenAIResponse,
			wantPath:       "/v1/responses",
			wantAuthHeader: "Authorization",
			response:       `{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name:           "anthropic",
			protocol:       protocol.Anthropic,
			wantPath:       "/v1/messages",
			wantAuthHeader: "X-Api-Key",
			response:       `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx := setupRelayTestDB(t)

			var captured capturedProtocolRequest
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				captured = capturedProtocolRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.response))
			}))
			defer upstream.Close()
			channel := &model.Channel{
				Name:          "adaptive-" + tt.name,
				Type:          outbound.OutboundTypeOpenAIChat,
				Enabled:       true,
				BaseUrls:      []model.BaseUrl{{URL: upstream.URL + "/v1"}},
				CustomHeader:  []model.CustomHeader{{HeaderKey: "X-Channel", HeaderValue: "channel"}},
				Keys:          []model.ChannelKey{{Enabled: true, ChannelKey: "upstream-secret"}},
			}
			if err := op.ChannelCreate(channel, ctx); err != nil {
				t.Fatalf("ChannelCreate failed: %v", err)
			}
			group := &model.Group{Name: "adaptive-group-" + tt.name, Mode: model.GroupModeFailover}
			if err := op.GroupCreate(group, ctx); err != nil {
				t.Fatalf("GroupCreate failed: %v", err)
			}
			if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "upstream-model", Priority: 1, Weight: 1}, ctx); err != nil {
				t.Fatalf("GroupItemAdd failed: %v", err)
			}
			enableGroupProtocolPolicy(t, ctx, group.ID, model.ProtocolPolicyModeOverride, tt.protocol)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("api_key_id", 7)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+group.Name+`","messages":[{"role":"user","content":"hello"}]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("Authorization", "Bearer client-secret")
			c.Request.Header.Set("X-Api-Key", "client-secret")
			c.Request.Header.Set("Cookie", "session=client-secret")
			c.Request.Header.Set("Anthropic-Beta", "source-only")
			Handler(inbound.InboundTypeOpenAIChat, c)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if captured.path != tt.wantPath {
				t.Fatalf("path=%q want=%q", captured.path, tt.wantPath)
			}
			if got := captured.header.Get(tt.wantAuthHeader); got != authValue(tt.protocol) {
				t.Fatalf("auth %s=%q", tt.wantAuthHeader, got)
			}
			if tt.protocol != protocol.OpenAIChat {
				if captured.header.Get("Cookie") != "" || captured.header.Get("Anthropic-Beta") != "" {
					t.Fatalf("unsafe client headers leaked: cookie=%q beta=%q", captured.header.Get("Cookie"), captured.header.Get("Anthropic-Beta"))
				}
			}
			if captured.header.Get("X-Channel") != "channel" {
				t.Fatalf("channel headers=%v", captured.header)
			}
			var payload map[string]any
			if err := json.Unmarshal(captured.body, &payload); err != nil {
				t.Fatalf("decode upstream body: %v body=%s", err, captured.body)
			}
			if payload["model"] != "upstream-model" {
				t.Fatalf("upstream payload=%v", payload)
			}
		})
	}
}

func enableGroupProtocolPolicy(t *testing.T, ctx context.Context, groupID int, mode model.ProtocolPolicyMode, preferred ...protocol.Protocol) {
	t.Helper()
	enabled := true
	if _, err := op.ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
		ExpectedRevision:       0,
		ProtocolRoutingEnabled: &enabled,
	}, "test", ctx); err != nil {
		// Best-effort: enable the kill switch even when revision already advanced.
		state, getErr := op.ProtocolPolicyGet(ctx)
		if getErr != nil {
			t.Fatalf("ProtocolPolicyGet failed: %v", getErr)
		}
		if _, err = op.ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
			ExpectedRevision:       state.ActiveRevision,
			ProtocolRoutingEnabled: &enabled,
		}, "test", ctx); err != nil {
			t.Fatalf("ProtocolRoutingConfigUpdate failed: %v", err)
		}
	}
	protocols := make([]string, 0, len(preferred))
	for _, value := range preferred {
		protocols = append(protocols, string(value))
	}
	if _, err := op.GroupUpdate(&model.GroupUpdateRequest{
		ID: groupID,
		ProtocolMode: &mode,
		PreferredProtocols: &protocols,
	}, ctx); err != nil {
		t.Fatalf("GroupUpdate protocol policy failed: %v", err)
	}
}

func stringPointer(value string) *string { return &value }

func authValue(target protocol.Protocol) string {
	if target == protocol.Anthropic {
		return "upstream-secret"
	}
	return "Bearer upstream-secret"
}
