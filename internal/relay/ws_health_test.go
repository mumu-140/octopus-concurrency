package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/outlierwindow"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/coder/websocket"
)

const (
	wsHealthGroupName = "ws-health-group"
	wsHealthModelName = "ws-health-model"
	wsHealthOtherName = "ws-health-other"
)

// newWSHealthRoute 建一个 OpenAI Chat 渠道 + Failover 分组（RetryEnabled=false ⇒ 同渠道只尝试一次），
// 并清空该渠道的历史健康窗口（outlierwindow 是进程级全局存储，sqlite 临时库的自增 ID 会跨用例复用）。
func newWSHealthRoute(t *testing.T, ctx context.Context, upstreamURL string) int {
	t.Helper()

	channel := &model.Channel{
		Name:      "ws-health-chan",
		Type:      outbound.OutboundTypeOpenAIChat,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: upstreamURL + "/v1"}},
		Model:     wsHealthModelName,
		ProxyMode: model.ProxyUsageModeDirect,
		Keys:      []model.ChannelKey{{Enabled: true, ChannelKey: "ws-health-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}

	group := &model.Group{Name: wsHealthGroupName, Mode: model.GroupModeFailover, SessionKeepTime: 60}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	item := &model.GroupItem{
		GroupID: group.ID, ChannelID: channel.ID, ModelName: wsHealthModelName,
		Priority: 1, Weight: 1,
	}
	if err := op.GroupItemAdd(item, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}

	outlierwindow.ClearChannel(channel.ID)
	return channel.ID
}

func newWSHealthRelayRequest(t *testing.T, ctx context.Context) (*relayRequest, *model.Group) {
	t.Helper()

	clientConn, serverConn := newTestWSConnPair(t)
	t.Cleanup(func() {
		clientConn.Close(websocket.StatusNormalClosure, "")
		serverConn.Close(websocket.StatusNormalClosure, "")
	})

	rawBody := []byte(`{"model":"` + wsHealthGroupName + `","messages":[{"role":"user","content":"hi"}]}`)
	newInternal := func() *transformerModel.InternalLLMRequest {
		return &transformerModel.InternalLLMRequest{
			Model:        wsHealthGroupName,
			RawAPIFormat: transformerModel.APIFormatOpenAIChatCompletion,
			Messages:     []transformerModel.Message{{Role: "user", Content: transformerModel.MessageContent{Content: stringPtr("hi")}}},
		}
	}

	req, group, err := newWSRelayRequest(
		ctx,
		serverConn,
		inbound.Get(inbound.InboundTypeOpenAIChat),
		9001,
		wsHealthGroupName,
		newInternal(),
		newInternal(),
		nil,
		rawBody,
	)
	if err != nil {
		t.Fatalf("newWSRelayRequest failed: %v", err)
	}
	return req, group
}

// 模型级失败（400 且报文不含任何渠道级特征）只惩罚当前 (渠道, 模型)，
// 同渠道其它模型的窗口必须保持原样。
func TestRunWSRelayModelScopeFailureOnlyCurrentModel(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"deliberate test failure","type":"invalid_request"}}`)
	}))
	defer server.Close()

	channelID := newWSHealthRoute(t, ctx, server.URL)
	// 预置同渠道另一模型的一次成功样本，用于证明模型级失败不会外溢。
	outlierwindow.Report(channelID, wsHealthOtherName, true, http.StatusOK, time.Now())

	req, group := newWSHealthRelayRequest(t, ctx)
	result := runWSRelay(ctx, req, group)
	if result.Success {
		t.Fatal("expected ws relay to fail on upstream 400")
	}
	if result.Canceled {
		t.Fatalf("upstream 400 must not be reported as canceled: %+v", result)
	}

	stats := outlierwindow.Evaluate(channelID, wsHealthModelName, time.Now())
	if stats.Samples != 1 || stats.Failures != 1 {
		t.Fatalf("current model stats = %+v, want one failure sample", stats)
	}
	other := outlierwindow.Evaluate(channelID, wsHealthOtherName, time.Now())
	if other.Samples != 1 || other.Failures != 0 {
		t.Fatalf("other model stats = %+v, want the pre-seeded success sample untouched", other)
	}
}

// 渠道级失败（5xx）铺到该渠道全部已知模型子键。
func TestRunWSRelayChannelScopeFailureFansOut(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"deliberate test failure"}}`)
	}))
	defer server.Close()

	channelID := newWSHealthRoute(t, ctx, server.URL)
	outlierwindow.Report(channelID, wsHealthOtherName, true, http.StatusOK, time.Now())

	req, group := newWSHealthRelayRequest(t, ctx)
	if result := runWSRelay(ctx, req, group); result.Success {
		t.Fatal("expected ws relay to fail on upstream 502")
	}

	stats := outlierwindow.Evaluate(channelID, wsHealthModelName, time.Now())
	if stats.Samples != 1 || stats.Failures != 1 {
		t.Fatalf("current model stats = %+v, want one failure sample", stats)
	}
	other := outlierwindow.Evaluate(channelID, wsHealthOtherName, time.Now())
	if other.Samples != 2 || other.Failures != 1 {
		t.Fatalf("other model stats = %+v, want channel-scope failure fanned out", other)
	}
}

// 客户端主动断开不是上游故障，不得写入任何健康度样本。
func TestRunWSRelayCanceledSkipsHealth(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		// 挂住上游，保证取消发生在请求飞行途中；若直接返回，上游会先以 200 完成并记一次成功样本。
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	channelID := newWSHealthRoute(t, ctx, server.URL)

	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, group := newWSHealthRelayRequest(t, relayCtx)

	resultCh := make(chan wsRelayResult, 1)
	go func() { resultCh <- runWSRelay(relayCtx, req, group) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream request to start")
	}
	cancel()

	select {
	case result := <-resultCh:
		if result.Success {
			t.Fatal("canceled ws relay reported success")
		}
		if !result.Canceled {
			t.Fatalf("expected canceled result, got %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for runWSRelay to return")
	}

	if stats := outlierwindow.Evaluate(channelID, wsHealthModelName, time.Now()); stats.Samples != 0 {
		t.Fatalf("client cancellation added outlier sample: %+v", stats)
	}
}
