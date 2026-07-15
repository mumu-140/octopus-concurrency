package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/outlierwindow"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestImagesHandlerEnforcesChannelConcurrency(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	var upstreamStarts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamStarts.Add(1)
		writeImagesSuccess(w)
	}))
	defer server.Close()

	channel := newImagesTestChannel("image-concurrency", server.URL)
	channel.MaxConcurrency = 1
	group := &model.Group{Name: "public-image-concurrency", Mode: model.GroupModeFailover}
	created := persistImagesRoute(t, ctx, group, channel)[0]

	if !balancer.TryAcquireChannel(created.ID, created.MaxConcurrency) {
		t.Fatal("failed to reserve test concurrency slot")
	}
	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-concurrency","prompt":"draw"}`),
		"application/json",
	)
	c.Set("api_key_id", 1001)
	ImagesHandler("/images/generations", c)
	balancer.ReleaseChannel(created.ID)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if got := upstreamStarts.Load(); got != 0 {
		t.Fatalf("upstream starts = %d, want 0", got)
	}
	if got := balancer.CurrentChannelConcurrency(created.ID); got != 0 {
		t.Fatalf("channel concurrency after release = %d, want 0", got)
	}
}

func TestImagesHandlerEnforcesChannelRPM(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	var upstreamStarts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamStarts.Add(1)
		writeImagesSuccess(w)
	}))
	defer server.Close()

	channel := newImagesTestChannel("image-rpm", server.URL)
	channel.MaxRPM = 1
	group := &model.Group{Name: "public-image-rpm", Mode: model.GroupModeFailover}
	created := persistImagesRoute(t, ctx, group, channel)[0]
	if !balancer.TryConsumeChannelRPM(created.ID, created.MaxRPM, time.Now()) {
		t.Fatal("failed to consume initial RPM token")
	}

	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-rpm","prompt":"draw"}`),
		"application/json",
	)
	c.Set("api_key_id", 1002)
	ImagesHandler("/images/generations", c)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	if got := upstreamStarts.Load(); got != 0 {
		t.Fatalf("upstream starts = %d, want 0", got)
	}
	if got := balancer.CurrentChannelConcurrency(created.ID); got != 0 {
		t.Fatalf("channel concurrency = %d, want 0", got)
	}
}

func TestImagesHandlerFailsOverToNextKeyWithinChannel(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	var (
		mu    sync.Mutex
		auths []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		auths = append(auths, auth)
		mu.Unlock()
		if auth == "Bearer image-key-two" {
			writeImagesSuccess(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"first key failed"}}`)
	}))
	defer server.Close()

	channel := newImagesTestChannel("image-key-failover", server.URL)
	channel.Keys = []model.ChannelKey{
		{Enabled: true, ChannelKey: "image-key-one", TotalCost: 0},
		{Enabled: true, ChannelKey: "image-key-two", TotalCost: 1},
	}
	group := &model.Group{
		Name:         "public-image-key-failover",
		Mode:         model.GroupModeFailover,
		RetryEnabled: true,
		MaxRetries:   2,
	}
	created := persistImagesRoute(t, ctx, group, channel)[0]
	outlierwindow.Clear(created.ID)

	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-key-failover","prompt":"draw"}`),
		"application/json",
	)
	c.Set("api_key_id", 1003)
	ImagesHandler("/images/generations", c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	mu.Lock()
	gotAuths := append([]string(nil), auths...)
	mu.Unlock()
	wantAuths := []string{"Bearer image-key-one", "Bearer image-key-two"}
	if fmt.Sprint(gotAuths) != fmt.Sprint(wantAuths) {
		t.Fatalf("authorization sequence = %v, want %v", gotAuths, wantAuths)
	}
	stats := outlierwindow.Evaluate(created.ID, time.Now())
	if stats.Samples != 1 || stats.Failures != 0 {
		t.Fatalf("outlier stats = %+v, want one successful sample", stats)
	}
}

func TestImagesHandlerCapsTotalUpstreamStartsAcrossManyCandidates(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	var upstreamStarts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamStarts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"capacity unavailable"}}`)
	}))
	defer server.Close()

	group := &model.Group{
		Name:         "public-image-budget",
		Mode:         model.GroupModeFailover,
		RetryEnabled: true,
		MaxRetries:   3,
	}
	channels := make([]*model.Channel, 0, 74)
	for i := 0; i < 74; i++ {
		channels = append(channels, newImagesTestChannel(fmt.Sprintf("image-budget-%02d", i), server.URL))
	}
	persistImagesRoute(t, ctx, group, channels...)

	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-budget","prompt":"draw"}`),
		"application/json",
	)
	c.Set("api_key_id", 1004)
	ImagesHandler("/images/generations", c)

	if got := upstreamStarts.Load(); got != 4 {
		t.Fatalf("upstream starts = %d, want 4", got)
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestImagesHandlerClientCancellationDoesNotPolluteHealth(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	channel := newImagesTestChannel("image-cancel", server.URL)
	channel.MaxConcurrency = 1
	group := &model.Group{
		Name:         "public-image-cancel",
		Mode:         model.GroupModeFailover,
		RetryEnabled: true,
		MaxRetries:   3,
	}
	created := persistImagesRoute(t, ctx, group, channel)[0]
	keyID := created.Keys[0].ID
	outlierwindow.Clear(created.ID)
	if err := op.SettingSetInt(model.SettingKeyCircuitBreakerThreshold, 1); err != nil {
		t.Fatalf("SettingSetInt threshold failed: %v", err)
	}

	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-cancel","prompt":"draw"}`),
		"application/json",
	)
	requestCtx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestCtx)
	c.Set("api_key_id", 1005)
	done := make(chan struct{})
	go func() {
		ImagesHandler("/images/generations", c)
		close(done)
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ImagesHandler did not stop after cancellation")
	}

	if got := balancer.CurrentChannelConcurrency(created.ID); got != 0 {
		t.Fatalf("channel concurrency = %d, want 0", got)
	}
	if tripped, _ := balancer.IsTripped(created.ID, keyID, "gpt-image-2"); tripped {
		t.Fatal("client cancellation tripped circuit breaker")
	}
	if stats := outlierwindow.Evaluate(created.ID, time.Now()); stats.Samples != 0 {
		t.Fatalf("client cancellation added outlier sample: %+v", stats)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote body: %q", recorder.Body.String())
	}
}

func TestImagesHandlerTruncatedSSEDoesNotPolluteHealth(t *testing.T) {
	ginTestMode(t)
	ctx := setupRelayTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0}\n\n")
	}))
	defer server.Close()

	channel := newImagesTestChannel("image-truncated", server.URL)
	channel.MaxConcurrency = 1
	group := &model.Group{Name: "public-image-truncated", Mode: model.GroupModeFailover}
	created := persistImagesRoute(t, ctx, group, channel)[0]
	keyID := created.Keys[0].ID
	outlierwindow.Clear(created.ID)
	if err := op.SettingSetInt(model.SettingKeyCircuitBreakerThreshold, 1); err != nil {
		t.Fatalf("SettingSetInt threshold failed: %v", err)
	}

	recorder, c := newImagesTestContext(
		"/v1/images/generations",
		[]byte(`{"model":"public-image-truncated","prompt":"draw","stream":true}`),
		"application/json",
	)
	c.Set("api_key_id", 1006)
	ImagesHandler("/images/generations", c)

	if recorder.Body.Len() == 0 {
		t.Fatal("truncated SSE did not relay partial bytes")
	}
	if got := balancer.CurrentChannelConcurrency(created.ID); got != 0 {
		t.Fatalf("channel concurrency = %d, want 0", got)
	}
	if tripped, _ := balancer.IsTripped(created.ID, keyID, "gpt-image-2"); tripped {
		t.Fatal("truncated SSE tripped circuit breaker")
	}
	if stats := outlierwindow.Evaluate(created.ID, time.Now()); stats.Samples != 0 {
		t.Fatalf("truncated SSE added outlier sample: %+v", stats)
	}
}

func persistImagesRoute(
	t *testing.T,
	ctx context.Context,
	group *model.Group,
	channels ...*model.Channel,
) []*model.Channel {
	t.Helper()
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate(%q) error = %v", group.Name, err)
	}
	created := make([]*model.Channel, 0, len(channels))
	for i, channel := range channels {
		if err := op.ChannelCreate(channel, ctx); err != nil {
			t.Fatalf("ChannelCreate(%q) error = %v", channel.Name, err)
		}
		reloaded, err := op.ChannelGet(channel.ID, ctx)
		if err != nil {
			t.Fatalf("ChannelGet(%q) error = %v", channel.Name, err)
		}
		item := &model.GroupItem{
			GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-image-2",
			Priority: i + 1, Weight: 1,
		}
		if err := op.GroupItemAdd(item, ctx); err != nil {
			t.Fatalf("GroupItemAdd(%q) error = %v", channel.Name, err)
		}
		created = append(created, reloaded)
	}
	return created
}

func newImagesTestChannel(name string, upstreamURL string) *model.Channel {
	return &model.Channel{
		Name:      name,
		Type:      outbound.OutboundTypeOpenAIChat,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: upstreamURL + "/v1"}},
		Model:     "gpt-image-2",
		ProxyMode: model.ProxyUsageModeDirect,
		Keys:      []model.ChannelKey{{Enabled: true, ChannelKey: name + "-key"}},
	}
}

func writeImagesSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"created":1,"data":[]}`)
}

func ginTestMode(t *testing.T) {
	t.Helper()
}
