package protocolroute

import (
	"encoding/json"
	"testing"

	"github.com/bestruirui/octopus/internal/protocol"
	tmodel "github.com/bestruirui/octopus/internal/transformer/model"
)

func strPtr(s string) *string { return &s }

// ---- CaptureFeatures ----

func TestCaptureFeaturesPlainTextAndStream(t *testing.T) {
	stream := true
	req := &tmodel.InternalLLMRequest{
		Model:  "m",
		Stream: &stream,
		Messages: []tmodel.Message{
			{Role: "user", Content: tmodel.MessageContent{Content: strPtr("hello")}},
		},
	}
	f := CaptureFeatures(req)
	if !f.PlainText || !f.Streaming {
		t.Fatalf("flags = %+v, want PlainText+Streaming", f)
	}
	if f.RequiresLegacyOnly() {
		t.Fatal("plain text must be adaptive-eligible")
	}
}

func TestCaptureFeaturesMultimodalForcesLegacyOnly(t *testing.T) {
	req := &tmodel.InternalLLMRequest{
		Model: "m",
		Messages: []tmodel.Message{
			{Role: "user", Content: tmodel.MessageContent{MultipleContent: []tmodel.MessageContentPart{
				{Type: "image_url", ImageURL: &tmodel.ImageURL{URL: "https://x/1.png"}},
			}}},
		},
	}
	f := CaptureFeatures(req)
	if !f.Multimodal || !f.RequiresLegacyOnly() {
		t.Fatalf("flags = %+v, want Multimodal→LegacyOnly", f)
	}
}

func TestCaptureFeaturesResponsesNativeAndContinuation(t *testing.T) {
	req := &tmodel.InternalLLMRequest{
		Model:              "m",
		RawAPIFormat:       tmodel.APIFormatOpenAIResponse,
		RawInputItems:      json.RawMessage(`[{"type":"message"}]`),
		PreviousResponseID: strPtr("resp_1"),
	}
	f := CaptureFeatures(req)
	if !f.ResponsesNative || !f.Continuation {
		t.Fatalf("flags = %+v, want ResponsesNative+Continuation", f)
	}
	if f.RequiresLegacyOnly() {
		t.Fatal("responses-native must stay adaptive (gated by matrix, not legacy)")
	}
}

func TestCaptureFeaturesAnthropicMarkers(t *testing.T) {
	req := &tmodel.InternalLLMRequest{
		Model: "m",
		Messages: []tmodel.Message{
			{Role: "user", Content: tmodel.MessageContent{Content: strPtr("hi")}, CacheControl: &tmodel.CacheControl{Type: "ephemeral"}},
			{Role: "assistant", Content: tmodel.MessageContent{Content: strPtr("ok")}, ReasoningBlocks: []tmodel.ReasoningBlock{{}}},
		},
	}
	f := CaptureFeatures(req)
	if !f.AnthropicCache || !f.AnthropicSignature {
		t.Fatalf("flags = %+v, want AnthropicCache+AnthropicSignature", f)
	}
}

func TestCaptureFeaturesEmbedding(t *testing.T) {
	req := &tmodel.InternalLLMRequest{
		Model:          "m",
		EmbeddingInput: &tmodel.EmbeddingInput{},
	}
	f := CaptureFeatures(req)
	if !f.Embedding || !f.RequiresLegacyOnly() {
		t.Fatalf("flags = %+v, want Embedding→LegacyOnly", f)
	}
}

// ---- EvaluateConversion 矩阵 ----

func TestMatrixPlainText(t *testing.T) {
	f := RequestFeatureFlags{PlainText: true}
	cases := []struct {
		in, up protocol.Protocol
		want   ConversionVerdict
	}{
		{protocol.OpenAIResponse, protocol.OpenAIResponse, VerdictLossless},
		{protocol.Anthropic, protocol.Anthropic, VerdictLossless},
		{protocol.OpenAIChat, protocol.OpenAIChat, VerdictSupportedWithLimits}, // Chat→Chat normalized
		{protocol.OpenAIChat, protocol.OpenAIResponse, VerdictSupportedWithLimits},
		{protocol.OpenAIChat, protocol.Anthropic, VerdictSupportedWithLimits},
		{protocol.OpenAIResponse, protocol.Anthropic, VerdictSupportedWithLimits},
	}
	for _, c := range cases {
		if got := EvaluateConversion(c.in, c.up, f); got != c.want {
			t.Fatalf("plain %s→%s = %q, want %q", c.in, c.up, got, c.want)
		}
	}
}

func TestMatrixFunctionTools(t *testing.T) {
	f := RequestFeatureFlags{FunctionTools: true}
	if got := EvaluateConversion(protocol.OpenAIResponse, protocol.Anthropic, f); got != VerdictForbidden {
		t.Fatalf("tools Responses→Anthropic = %q, want forbidden", got)
	}
	if got := EvaluateConversion(protocol.Anthropic, protocol.OpenAIResponse, f); got != VerdictForbidden {
		t.Fatalf("tools Anthropic→Responses = %q, want forbidden", got)
	}
	if got := EvaluateConversion(protocol.OpenAIChat, protocol.Anthropic, f); got != VerdictSupportedWithLimits {
		t.Fatalf("tools Chat→Anthropic = %q, want supported_with_limits", got)
	}
	if got := EvaluateConversion(protocol.OpenAIResponse, protocol.OpenAIResponse, f); got != VerdictLossless {
		t.Fatalf("tools Responses passthrough = %q, want lossless", got)
	}
	if got := EvaluateConversion(protocol.OpenAIChat, protocol.OpenAIChat, f); got != VerdictSupportedWithLimits {
		t.Fatalf("tools Chat→Chat = %q, want supported_with_limits", got)
	}
}

func TestMatrixResponsesNativeOnlyPassthrough(t *testing.T) {
	f := RequestFeatureFlags{ResponsesNative: true}
	if got := EvaluateConversion(protocol.OpenAIResponse, protocol.OpenAIResponse, f); got != VerdictLossless {
		t.Fatalf("native Responses→Responses = %q, want lossless", got)
	}
	for _, up := range []protocol.Protocol{protocol.OpenAIChat, protocol.Anthropic} {
		if got := EvaluateConversion(protocol.OpenAIResponse, up, f); got != VerdictForbidden {
			t.Fatalf("native Responses→%s = %q, want forbidden", up, got)
		}
	}
}

func TestMatrixAnthropicSignatureOnlyPassthrough(t *testing.T) {
	f := RequestFeatureFlags{AnthropicSignature: true}
	if got := EvaluateConversion(protocol.Anthropic, protocol.Anthropic, f); got != VerdictLossless {
		t.Fatalf("signature Anthropic→Anthropic = %q, want lossless", got)
	}
	for _, up := range []protocol.Protocol{protocol.OpenAIChat, protocol.OpenAIResponse} {
		if got := EvaluateConversion(protocol.Anthropic, up, f); got != VerdictForbidden {
			t.Fatalf("signature Anthropic→%s = %q, want forbidden", up, got)
		}
	}
}

func TestMatrixNonAdaptiveIsLegacyFixed(t *testing.T) {
	f := RequestFeatureFlags{PlainText: true}
	if got := EvaluateConversion(protocol.OpenAIChat, protocol.Gemini, f); got != VerdictLegacyFixed {
		t.Fatalf("Chat→Gemini = %q, want legacy_fixed", got)
	}
	if got := EvaluateConversion(protocol.Unknown, protocol.OpenAIChat, f); got != VerdictLegacyFixed {
		t.Fatalf("Unknown→Chat = %q, want legacy_fixed", got)
	}
}

func TestModeFor(t *testing.T) {
	if got := ModeFor(protocol.OpenAIResponse, protocol.OpenAIResponse, VerdictLossless); got != ModeRawPassthrough {
		t.Fatalf("Responses same = %q, want raw_passthrough", got)
	}
	if got := ModeFor(protocol.OpenAIChat, protocol.OpenAIChat, VerdictSupportedWithLimits); got != ModeNormalized {
		t.Fatalf("Chat→Chat = %q, want normalized", got)
	}
	if got := ModeFor(protocol.OpenAIChat, protocol.Anthropic, VerdictSupportedWithLimits); got != ModeTranslated {
		t.Fatalf("Chat→Anthropic = %q, want translated", got)
	}
	if got := ModeFor(protocol.OpenAIChat, protocol.Gemini, VerdictLegacyFixed); got != ModeLegacy {
		t.Fatalf("legacy verdict = %q, want legacy", got)
	}
}

// ---- AttemptPlan 不可变性 ----

func TestAttemptPlanIsImmutable(t *testing.T) {
	hp := HeaderPolicy{Set: map[string]string{"X-A": "1"}}
	param := json.RawMessage(`{"temperature":0.5}`)
	spec := PlanSpec{
		ChannelID:        7,
		ChannelKeyID:     70,
		RequestedModel:   "alias",
		UpstreamModel:    "real-model",
		IngressProtocol:  protocol.OpenAIResponse,
		UpstreamProtocol: protocol.OpenAIResponse,
		ConversionMode:   ModeRawPassthrough,
		Features:         RequestFeatureFlags{PlainText: true},
		BaseURL:          "https://up.example/v1",
		HeaderPolicy:     hp,
		ParamOverride:    param,
		PolicySource:     "group_prefer",
		PolicyPriority:   700,
		ConfigRevision:   42,
		AttemptKind:      KindCandidatePrimary,
		FallbackAuthID:   "auth-1",
	}
	plan := NewAttemptPlan(spec)

	// 修改构造输入不影响 Plan
	hp.Set["X-A"] = "mutated"
	param[2] = 'X'
	spec.BaseURL = "mutated"

	if plan.HeaderPolicy().Set["X-A"] != "1" {
		t.Fatal("plan header mutated via spec")
	}
	if string(plan.ParamOverride()) != `{"temperature":0.5}` {
		t.Fatal("plan param mutated via spec")
	}
	if plan.BaseURL() != "https://up.example/v1" {
		t.Fatal("plan baseURL mutated via spec")
	}

	// 修改访问器返回值不影响 Plan 内部
	got := plan.HeaderPolicy()
	got.Set["X-A"] = "mutated-again"
	if plan.HeaderPolicy().Set["X-A"] != "1" {
		t.Fatal("plan header mutated via accessor")
	}
	pb := plan.ParamOverride()
	pb[2] = 'Y'
	if string(plan.ParamOverride()) != `{"temperature":0.5}` {
		t.Fatal("plan param mutated via accessor")
	}

	if plan.ChannelID() != 7 || plan.ChannelKeyID() != 70 ||
		plan.UpstreamProtocol() != protocol.OpenAIResponse ||
		plan.ConfigRevision() != 42 || plan.FallbackAuthID() != "auth-1" {
		t.Fatalf("plan scalar fields wrong: %+v", plan)
	}
	if plan.IsLegacyFixed() {
		t.Fatal("adaptive plan must not be legacy fixed")
	}
}

func TestLegacyFixedAttemptConstraints(t *testing.T) {
	plan := NewLegacyFixedAttempt(PlanSpec{
		ChannelID:        3,
		ChannelKeyID:     30,
		UpstreamModel:    "m",
		IngressProtocol:  protocol.OpenAIChat,
		UpstreamProtocol: protocol.Gemini,                   // 候选 Channel.Type
		AttemptKind:      KindSameCandidateProtocolFallback, // 构造函数必须覆盖
		FallbackAuthID:   "must-be-cleared",
	})
	if !plan.IsLegacyFixed() {
		t.Fatal("want legacy fixed")
	}
	if plan.ConversionMode() != ModeLegacy {
		t.Fatalf("mode = %q, want legacy", plan.ConversionMode())
	}
	if plan.AttemptKind() != KindCandidatePrimary {
		t.Fatalf("kind = %q, want candidate_primary", plan.AttemptKind())
	}
	if plan.FallbackAuthID() != "" {
		t.Fatal("legacy fixed must not carry FallbackAuthID")
	}
	if plan.UpstreamProtocol() != protocol.Gemini {
		t.Fatalf("legacy plan must keep Channel.Type protocol, got %q", plan.UpstreamProtocol())
	}
}

// ---- Attempt 隔离：请求深拷贝配合 Plan ----

func TestAttemptRequestSnapshotIsolation(t *testing.T) {
	stream := true
	orig := &tmodel.InternalLLMRequest{
		Model:  "m",
		Stream: &stream,
		Messages: []tmodel.Message{
			{Role: "user", Content: tmodel.MessageContent{Content: strPtr("hi")}},
		},
	}
	first := orig.Clone()
	second := orig.Clone()

	// 第一次转换修改自己的快照
	first.Model = "changed-by-first"
	*first.Messages[0].Content.Content = "mutated"

	if second.Model != "m" {
		t.Fatal("second attempt snapshot polluted by first")
	}
	if *second.Messages[0].Content.Content != "hi" {
		t.Fatal("second attempt message polluted by first")
	}
	if orig.Model != "m" {
		t.Fatal("original request polluted")
	}
}
