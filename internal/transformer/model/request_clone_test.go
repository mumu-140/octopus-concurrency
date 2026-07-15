package model

import (
	"encoding/json"
	"net/url"
	"testing"
)

func buildCloneFixture() *InternalLLMRequest {
	stream := true
	prevID := "resp_123"
	budget := int64(2048)
	return &InternalLLMRequest{
		Model:  "gpt-test",
		Stream: &stream,
		Messages: []Message{
			{
				Role: "user",
				Content: MessageContent{MultipleContent: []MessageContentPart{
					{Type: "text", Text: clStrPtr("hello")},
				}},
				CacheControl: &CacheControl{Type: "ephemeral"},
			},
		},
		Tools:               []Tool{{Type: "function"}},
		Metadata:            map[string]string{"k": "v"},
		TransformerMetadata: map[string]string{"tm": "1"},
		RawRequest:          []byte(`{"model":"gpt-test"}`),
		RawAPIFormat:        APIFormatOpenAIResponse,
		RawInputItems:       json.RawMessage(`[{"type":"message"}]`),
		PreviousResponseID:  &prevID,
		ReasoningBudget:     &budget,
		Query:               url.Values{"a": {"1", "2"}},
	}
}

func clStrPtr(s string) *string { return &s }

func TestCloneProducesDeepIndependentCopy(t *testing.T) {
	orig := buildCloneFixture()
	cl := orig.Clone()

	// 修改克隆的每一类可变字段，原对象必须不受影响。
	*cl.Stream = false
	cl.Messages[0].Role = "assistant"
	*cl.Messages[0].Content.MultipleContent[0].Text = "mutated"
	cl.Messages[0].CacheControl.Type = "changed"
	cl.Metadata["k"] = "mutated"
	cl.TransformerMetadata["tm"] = "mutated"
	cl.RawRequest[0] = 'X'
	cl.RawInputItems[0] = 'X'
	*cl.PreviousResponseID = "mutated"
	*cl.ReasoningBudget = 1
	cl.Query.Set("a", "mutated")
	cl.Tools[0].Type = "mutated"

	if !*orig.Stream {
		t.Fatal("orig.Stream mutated via clone")
	}
	if orig.Messages[0].Role != "user" {
		t.Fatal("orig message role mutated")
	}
	if *orig.Messages[0].Content.MultipleContent[0].Text != "hello" {
		t.Fatal("orig content text mutated")
	}
	if orig.Messages[0].CacheControl.Type != "ephemeral" {
		t.Fatal("orig cache control mutated")
	}
	if orig.Metadata["k"] != "v" || orig.TransformerMetadata["tm"] != "1" {
		t.Fatal("orig maps mutated")
	}
	if orig.RawRequest[0] != '{' || orig.RawInputItems[0] != '[' {
		t.Fatal("orig raw bytes mutated")
	}
	if *orig.PreviousResponseID != "resp_123" {
		t.Fatal("orig previous_response_id mutated")
	}
	if *orig.ReasoningBudget != 2048 {
		t.Fatal("orig reasoning budget mutated")
	}
	if orig.Query.Get("a") != "1" {
		t.Fatal("orig query mutated")
	}
	if orig.Tools[0].Type != "function" {
		t.Fatal("orig tools mutated")
	}
}

func TestClonePreservesJSONDashFields(t *testing.T) {
	orig := buildCloneFixture()
	cl := orig.Clone()

	if cl.RawAPIFormat != APIFormatOpenAIResponse {
		t.Fatalf("RawAPIFormat lost: %q", cl.RawAPIFormat)
	}
	if string(cl.RawRequest) != string(orig.RawRequest) {
		t.Fatal("RawRequest bytes differ")
	}
	if string(cl.RawInputItems) != string(orig.RawInputItems) {
		t.Fatal("RawInputItems bytes differ")
	}
	if cl.PreviousResponseID == nil || *cl.PreviousResponseID != "resp_123" {
		t.Fatal("PreviousResponseID lost")
	}
	if cl.TransformerMetadata["tm"] != "1" {
		t.Fatal("TransformerMetadata lost")
	}
}

func TestCloneNilAndEmpty(t *testing.T) {
	var nilReq *InternalLLMRequest
	if nilReq.Clone() != nil {
		t.Fatal("nil clone should be nil")
	}
	empty := &InternalLLMRequest{}
	cl := empty.Clone()
	if cl == nil || cl == empty {
		t.Fatal("empty clone should be a new object")
	}
	if cl.Messages != nil || cl.Metadata != nil || cl.Query != nil {
		t.Fatal("nil fields must stay nil")
	}
}
