package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/bodycache"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func TestImagesAttemptPreservesGPTImage2JSONAndRewritesOnlyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{
		"model":"public-image-group",
		"prompt":"A red fox under northern lights",
		"n":2,
		"size":"1536x1024",
		"quality":"high",
		"background":"transparent",
		"output_format":"webp",
		"output_compression":87,
		"moderation":"low",
		"user":"tenant-42",
		"vendor_extension":{"preserve":true}
	}`)
	responseBody := `{"created":1,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":11,"output_tokens":29,"total_tokens":40}}`
	captured := make(chan capturedImageRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- captureImageRequest(r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	result := runJSONImageAttempt(t, server.URL, "/images/generations", requestBody, "gpt-image-2-upstream")
	gotRequest := <-captured

	if result.err != nil {
		t.Fatalf("imagesAttempt() error = %v", result.err)
	}
	if result.statusCode != http.StatusOK || !result.written {
		t.Fatalf("imagesAttempt() status=%d written=%t", result.statusCode, result.written)
	}
	if result.contentType != "application/json; charset=utf-8" {
		t.Fatalf("upstream content type = %q", result.contentType)
	}
	wantUsage := imagesUsage{InputTokens: 11, OutputTokens: 29, TotalTokens: 40}
	if result.usage == nil || *result.usage != wantUsage {
		t.Fatalf("usage = %#v, want %#v", result.usage, wantUsage)
	}
	if gotRequest.method != http.MethodPost || gotRequest.path != "/v1/images/generations" {
		t.Fatalf("upstream target = %s %s", gotRequest.method, gotRequest.path)
	}
	if gotRequest.header.Get("Authorization") != "Bearer image-test-key" {
		t.Fatalf("upstream Authorization = %q", gotRequest.header.Get("Authorization"))
	}
	if gotRequest.header.Get("X-Request-Trace") != "trace-123" {
		t.Fatalf("upstream trace header = %q", gotRequest.header.Get("X-Request-Trace"))
	}

	var gotPayload, wantPayload map[string]any
	if err := json.Unmarshal(gotRequest.body, &gotPayload); err != nil {
		t.Fatalf("unmarshal upstream request: %v", err)
	}
	if err := json.Unmarshal(requestBody, &wantPayload); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	wantPayload["model"] = "gpt-image-2-upstream"
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("upstream JSON changed fields beyond model:\n got: %#v\nwant: %#v", gotPayload, wantPayload)
	}
	if got := result.recorder.Body.String(); got != responseBody {
		t.Fatalf("downstream body = %q, want exact upstream body %q", got, responseBody)
	}
}

func TestImagesAttemptPreservesMultipartEditAndRewritesModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalBody, boundary := buildMultipartEditBody(t)
	responseBody := `{"created":2,"data":[{"url":"https://example.test/image.png"}]}`
	captured := make(chan capturedImageRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- captureImageRequest(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	cache := newImagesBodyCache(t, originalBody)
	modelName, stream, err := parseMultipartModelAndStream(cache, boundary)
	if err != nil {
		t.Fatalf("parseMultipartModelAndStream() error = %v", err)
	}
	if modelName != "public-image-edit-group" || stream {
		t.Fatalf("parsed model=%q stream=%t", modelName, stream)
	}
	recorder, c := newImagesTestContext("/v1/images/edits", originalBody, "multipart/form-data; boundary="+boundary)
	result := callImagesAttempt(t, c, recorder, cache, server.URL, "/images/edits", true, boundary, nil, false, "gpt-image-2-edit")
	gotRequest := <-captured

	if result.err != nil {
		t.Fatalf("imagesAttempt() error = %v", result.err)
	}
	if result.statusCode != http.StatusOK || !result.written {
		t.Fatalf("imagesAttempt() status=%d written=%t", result.statusCode, result.written)
	}
	if gotRequest.path != "/v1/images/edits" {
		t.Fatalf("upstream path = %q", gotRequest.path)
	}
	parts := parseCapturedMultipart(t, gotRequest)
	assertMultipartField(t, parts, "model", "gpt-image-2-edit")
	assertMultipartField(t, parts, "prompt", "Keep the subject and replace the sky")
	assertMultipartField(t, parts, "input_fidelity", "high")
	assertMultipartField(t, parts, "quality", "high")
	assertMultipartField(t, parts, "size", "1536x1024")
	assertMultipartFile(t, parts, "image", "source.png", "image/png", []byte("source-png-bytes"))
	assertMultipartFile(t, parts, "mask", "mask.png", "image/png", []byte("mask-png-bytes"))
	if got := result.recorder.Body.String(); got != responseBody {
		t.Fatalf("downstream body = %q, want %q", got, responseBody)
	}
}

func TestImagesAttemptStreamsPartialAndCompletedEventsExactly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawSSE := "event: image_generation.partial_image\n" +
		"data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\"cGFydGlhbA==\"}\n\n" +
		"event: image_generation.completed\n" +
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZmluYWw=\",\"usage\":{\"input_tokens\":13,\"output_tokens\":31,\"total_tokens\":44}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, rawSSE)
	}))
	defer server.Close()

	body := []byte(`{"model":"public-image-group","prompt":"stream it","stream":true,"partial_images":1}`)
	result := runJSONImageAttempt(t, server.URL, "/images/generations", body, "gpt-image-2")

	if result.err != nil {
		t.Fatalf("imagesAttempt() error = %v", result.err)
	}
	if !result.written {
		t.Fatal("completed stream was not marked written")
	}
	wantUsage := imagesUsage{InputTokens: 13, OutputTokens: 31, TotalTokens: 44}
	if result.usage == nil || *result.usage != wantUsage {
		t.Fatalf("completed usage = %#v, want %#v", result.usage, wantUsage)
	}
	if got := result.recorder.Body.String(); got != rawSSE {
		t.Fatalf("SSE changed during relay:\n got: %q\nwant: %q", got, rawSSE)
	}
}

func TestImagesAttemptRejectsTruncatedSSEWithoutCompletedEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	truncated := "event: image_generation.partial_image\n" +
		"data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\"cGFydGlhbA==\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, truncated)
	}))
	defer server.Close()

	body := []byte(`{"model":"public-image-group","prompt":"stream it","stream":true}`)
	result := runJSONImageAttempt(t, server.URL, "/images/generations", body, "gpt-image-2")

	if result.err == nil {
		t.Fatal("truncated SSE without image_generation.completed was accepted as success")
	}
	if !result.written {
		t.Fatal("partial SSE bytes were written but written=false")
	}
}

func TestImagesAttemptTreatsSSEFailedEventAsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := "event: image_generation.failed\n" +
		"data: {\"type\":\"image_generation.failed\",\"error\":{\"code\":\"content_policy_violation\",\"message\":\"request rejected\"}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, failed)
	}))
	defer server.Close()

	body := []byte(`{"model":"public-image-group","prompt":"stream it","stream":true}`)
	result := runJSONImageAttempt(t, server.URL, "/images/generations", body, "gpt-image-2")

	if result.err == nil {
		t.Fatal("image_generation.failed event was accepted as success")
	}
	if !result.written {
		t.Fatal("failed SSE event was relayed but written=false")
	}
}

func TestImagesHandlerPreservesFinalUpstream429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := setupRelayTestDB(t)
	upstreamBody := `{"error":{"type":"rate_limit_error","message":"image capacity exhausted"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer server.Close()

	channel := &model.Channel{
		Name:      "image-rate-limit-channel",
		Type:      outbound.OutboundTypeOpenAIChat,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: server.URL + "/v1"}},
		Model:     "gpt-image-2",
		ProxyMode: model.ProxyUsageModeDirect,
		Keys:      []model.ChannelKey{{Enabled: true, ChannelKey: "image-test-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}
	group := &model.Group{Name: "public-image-rate-limit", Mode: model.GroupModeFailover, RetryEnabled: true}
	if err := op.GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}
	item := &model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-image-2", Priority: 1, Weight: 1}
	if err := op.GroupItemAdd(item, ctx); err != nil {
		t.Fatalf("GroupItemAdd() error = %v", err)
	}

	requestBody := []byte(`{"model":"public-image-rate-limit","prompt":"draw"}`)
	recorder, c := newImagesTestContext("/v1/images/generations", requestBody, "application/json")
	c.Set("api_key_id", 77)
	ImagesHandler("/images/generations", c)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("downstream status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "17" {
		t.Fatalf("Retry-After = %q, want 17", got)
	}
	if !strings.Contains(recorder.Body.String(), "image capacity exhausted") {
		t.Fatalf("downstream error lost safe upstream message: %s", recorder.Body.String())
	}
}

func TestImagesAttemptReturnsUpstreamHTTPErrorWithoutWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid image size"}}`)
	}))
	defer server.Close()

	body := []byte(`{"model":"public-image-group","prompt":"draw","size":"invalid"}`)
	result := runJSONImageAttempt(t, server.URL, "/images/generations", body, "gpt-image-2")

	if result.statusCode != http.StatusBadRequest || result.written {
		t.Fatalf("imagesAttempt() status=%d written=%t", result.statusCode, result.written)
	}
	if result.err == nil || !strings.Contains(result.err.Error(), "invalid image size") {
		t.Fatalf("imagesAttempt() error = %v", result.err)
	}
	if result.recorder.Body.Len() != 0 {
		t.Fatalf("attempt wrote error before retry decision: %q", result.recorder.Body.String())
	}
}

type imageAttemptResult struct {
	statusCode  int
	written     bool
	usage       *imagesUsage
	contentType string
	err         error
	recorder    *httptest.ResponseRecorder
}

type capturedImageRequest struct {
	method      string
	path        string
	header      http.Header
	contentType string
	body        []byte
}

type capturedMultipartPart struct {
	filename    string
	contentType string
	body        []byte
}

func runJSONImageAttempt(t *testing.T, upstreamURL string, endpoint string, requestBody []byte, actualModel string) imageAttemptResult {
	t.Helper()
	cache := newImagesBodyCache(t, requestBody)
	payload, modelName, stream, err := parseJSONModelAndStream(cache)
	if err != nil {
		t.Fatalf("parseJSONModelAndStream() error = %v", err)
	}
	if modelName == "" {
		t.Fatal("parseJSONModelAndStream() returned an empty model")
	}
	recorder, c := newImagesTestContext("/v1"+endpoint, requestBody, "application/json")
	return callImagesAttempt(t, c, recorder, cache, upstreamURL, endpoint, false, "", payload, stream, actualModel)
}

func callImagesAttempt(
	t *testing.T,
	c *gin.Context,
	recorder *httptest.ResponseRecorder,
	cache *bodycache.BodyCache,
	upstreamURL string,
	endpoint string,
	isMultipart bool,
	boundary string,
	payload map[string]any,
	stream bool,
	actualModel string,
) imageAttemptResult {
	t.Helper()
	channel := &model.Channel{
		Name:      "image-test-channel",
		Type:      outbound.OutboundTypeOpenAIChat,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: upstreamURL + "/v1"}},
		ProxyMode: model.ProxyUsageModeDirect,
	}
	status, written, usage, contentType, err := imagesAttempt(
		context.Background(), endpoint, c, cache, isMultipart, boundary, payload, stream,
		channel, "image-test-key", 0, newImagesRelayMetrics(1, "public-image-group"), actualModel, nil,
	)
	return imageAttemptResult{
		statusCode: status, written: written, usage: usage,
		contentType: contentType, err: err, recorder: recorder,
	}
}

func newImagesBodyCache(t *testing.T, body []byte) *bodycache.BodyCache {
	t.Helper()
	cache, err := bodycache.New(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("bodycache.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Errorf("BodyCache.Close() error = %v", err)
		}
	})
	return cache
}

func newImagesTestContext(path string, body []byte, contentType string) (*httptest.ResponseRecorder, *gin.Context) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Request.Header.Set("Authorization", "Bearer downstream-key")
	c.Request.Header.Set("X-Request-Trace", "trace-123")
	return recorder, c
}

func captureImageRequest(r *http.Request) capturedImageRequest {
	body, _ := io.ReadAll(r.Body)
	return capturedImageRequest{
		method: r.Method, path: r.URL.Path, header: r.Header.Clone(),
		contentType: r.Header.Get("Content-Type"), body: body,
	}
}

func buildMultipartEditBody(t *testing.T) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"model": "public-image-edit-group", "prompt": "Keep the subject and replace the sky",
		"input_fidelity": "high", "quality": "high", "size": "1536x1024",
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%q) error = %v", key, err)
		}
	}
	writeMultipartFile(t, writer, "image", "source.png", "image/png", []byte("source-png-bytes"))
	writeMultipartFile(t, writer, "mask", "mask.png", "image/png", []byte("mask-png-bytes"))
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart.Writer.Close() error = %v", err)
	}
	return body.Bytes(), writer.Boundary()
}

func writeMultipartFile(t *testing.T, writer *multipart.Writer, name string, filename string, contentType string, body []byte) {
	t.Helper()
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart(%q) error = %v", name, err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write multipart %q error = %v", name, err)
	}
}

func parseCapturedMultipart(t *testing.T, request capturedImageRequest) map[string]capturedMultipartPart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(request.contentType)
	if err != nil {
		t.Fatalf("ParseMediaType(%q) error = %v", request.contentType, err)
	}
	if mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("upstream Content-Type = %q", request.contentType)
	}
	reader := multipart.NewReader(bytes.NewReader(request.body), params["boundary"])
	parts := make(map[string]capturedMultipartPart)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return parts
		}
		if err != nil {
			t.Fatalf("NextPart() error = %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll(%q) error = %v", part.FormName(), err)
		}
		parts[part.FormName()] = capturedMultipartPart{
			filename: part.FileName(), contentType: part.Header.Get("Content-Type"), body: body,
		}
		_ = part.Close()
	}
}

func assertMultipartField(t *testing.T, parts map[string]capturedMultipartPart, name string, want string) {
	t.Helper()
	part, ok := parts[name]
	if !ok {
		t.Fatalf("multipart field %q missing", name)
	}
	if part.filename != "" || string(part.body) != want {
		t.Fatalf("multipart field %q = filename %q body %q, want body %q", name, part.filename, part.body, want)
	}
}

func assertMultipartFile(
	t *testing.T,
	parts map[string]capturedMultipartPart,
	name string,
	filename string,
	contentType string,
	want []byte,
) {
	t.Helper()
	part, ok := parts[name]
	if !ok {
		t.Fatalf("multipart file %q missing", name)
	}
	if part.filename != filename || part.contentType != contentType || !bytes.Equal(part.body, want) {
		t.Fatalf("multipart file %q = filename %q content-type %q body %q", name, part.filename, part.contentType, part.body)
	}
}
