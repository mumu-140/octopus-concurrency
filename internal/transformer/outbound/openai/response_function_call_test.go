package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/samber/lo"
)

func TestConvertInputFromMessagesGeneratesFunctionCallIDWithoutItemReference(t *testing.T) {
	// function_call gets typed IDs; function_call_output keeps only call_id unless client provided item_reference.
	msgs := []model.Message{
		{
			Role: "assistant",
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_abc123",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"location":"Beijing"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: lo.ToPtr("call_abc123"),
			Content: model.MessageContent{
				Content: lo.ToPtr("Sunny, 25°C"),
			},
		},
	}

	input := convertInputFromMessages(msgs, model.TransformOptions{ArrayInputs: lo.ToPtr(true)})

	if len(input.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(input.Items))
	}

	functionCall := input.Items[0]
	if functionCall.Type != "function_call" {
		t.Fatalf("expected first item to be function_call, got %s", functionCall.Type)
	}
	if functionCall.ID == "" {
		t.Error("function_call item missing ID")
	}
	if !strings.HasPrefix(functionCall.ID, "fc_") {
		t.Errorf("function_call id = %q, want fc_ prefix", functionCall.ID)
	}
	if functionCall.CallID != "call_abc123" {
		t.Errorf("expected call_id=call_abc123, got %s", functionCall.CallID)
	}

	functionCallOutput := input.Items[1]
	if functionCallOutput.Type != "function_call_output" {
		t.Fatalf("expected second item to be function_call_output, got %s", functionCallOutput.Type)
	}
	if functionCallOutput.ItemReference != nil {
		t.Fatalf("function_call_output must not synthesize item_reference, got %v", *functionCallOutput.ItemReference)
	}
	if functionCallOutput.CallID != "call_abc123" {
		t.Errorf("expected call_id=call_abc123, got %s", functionCallOutput.CallID)
	}
}

func TestSanitizeResponsesRawItemsDoesNotAddItemReference(t *testing.T) {
	rawItems := json.RawMessage(`[
		{
			"id": "item_xyz789",
			"type": "function_call",
			"call_id": "call_abc123",
			"name": "get_weather",
			"arguments": "{\"location\":\"Beijing\"}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_abc123",
			"output": {"text": "Sunny, 25°C"}
		}
	]`)

	sanitized := sanitizeResponsesRawItems(rawItems)

	var items []map[string]interface{}
	if err := json.Unmarshal(sanitized, &items); err != nil {
		t.Fatalf("failed to unmarshal sanitized items: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0]["id"] != "fc_xyz789" {
		t.Errorf("function_call id = %#v, want fc_xyz789", items[0]["id"])
	}

	functionCallOutput := items[1]
	if functionCallOutput["type"] != "function_call_output" {
		t.Fatalf("expected second item to be function_call_output, got %v", functionCallOutput["type"])
	}
	if _, ok := functionCallOutput["item_reference"]; ok {
		t.Fatalf("function_call_output must not gain item_reference, got %#v", functionCallOutput["item_reference"])
	}
}

func TestSanitizeResponsesRawItemsPreservesClientItemReference(t *testing.T) {
	rawItems := json.RawMessage(`[
		{"id":"item_xyz","type":"function_call","call_id":"call_1","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","item_reference":"item_xyz","output":{"text":"ok"}}
	]`)
	sanitized := sanitizeResponsesRawItems(rawItems)
	var items []map[string]interface{}
	if err := json.Unmarshal(sanitized, &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if items[0]["id"] != "fc_xyz" {
		t.Errorf("function_call id = %#v, want fc_xyz", items[0]["id"])
	}
	// Passthrough: do not rewrite client item_reference even if id was normalized.
	if items[1]["item_reference"] != "item_xyz" {
		t.Errorf("item_reference = %#v, want original client value item_xyz", items[1]["item_reference"])
	}
}

func TestSanitizeResponsesRawItemsLeavesNullItemReferenceAlone(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"null value", `[
			{"id":"item_xyz","type":"function_call","call_id":"call_1","name":"f","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","item_reference":null,"output":{"text":"ok"}}
		]`},
		{"empty string", `[
			{"id":"item_xyz","type":"function_call","call_id":"call_1","name":"f","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","item_reference":"","output":{"text":"ok"}}
		]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := sanitizeResponsesRawItems(json.RawMessage(tt.raw))
			var items []map[string]interface{}
			if err := json.Unmarshal(sanitized, &items); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			ref, hasRef := items[1]["item_reference"]
			if hasRef {
				if s, ok := ref.(string); ok && strings.HasPrefix(s, "fc_") {
					t.Fatalf("null/empty item_reference must not be backfilled, got %v", ref)
				}
			}
			if s, ok := ref.(string); ok && s != "" {
				t.Fatalf("unexpected non-empty item_reference value: %v", ref)
			}
		})
	}
}

func TestSanitizeResponsesRawItemsBackfillsMissingFunctionCallID(t *testing.T) {
	rawItems := json.RawMessage(`[
		{
			"type": "function_call",
			"call_id": "call_noid",
			"name": "do_thing",
			"arguments": "{}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_noid",
			"output": {"text": "done"}
		}
	]`)

	sanitized := sanitizeResponsesRawItems(rawItems)

	var items []map[string]interface{}
	if err := json.Unmarshal(sanitized, &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	generatedID, ok := items[0]["id"].(string)
	if !ok || generatedID == "" {
		t.Fatal("function_call missing generated id")
	}
	if !strings.HasPrefix(generatedID, "fc_") {
		t.Errorf("generated function_call id = %q, want fc_ prefix", generatedID)
	}
	if _, ok := items[1]["item_reference"]; ok {
		t.Fatalf("function_call_output must not gain item_reference, got %#v", items[1]["item_reference"])
	}
}

func TestMarshalResponsesInputItemsOmitsItemReference(t *testing.T) {
	msgs := []model.Message{
		{
			Role: "assistant",
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_test123",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "test_func",
						Arguments: `{}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: lo.ToPtr("call_test123"),
			Content: model.MessageContent{
				Content: lo.ToPtr("result"),
			},
		},
	}

	rawItems, err := MarshalResponsesInputItems(msgs)
	if err != nil {
		t.Fatalf("MarshalResponsesInputItems failed: %v", err)
	}

	var items []map[string]interface{}
	if err := json.Unmarshal(rawItems, &items); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var functionCallID string
	var foundCall, foundOutput bool
	for _, item := range items {
		switch item["type"] {
		case "function_call":
			if id, ok := item["id"].(string); ok {
				functionCallID = id
				foundCall = true
			}
		case "function_call_output":
			foundOutput = true
			if _, ok := item["item_reference"]; ok {
				t.Fatalf("marshaled function_call_output must omit item_reference, got %#v", item["item_reference"])
			}
		}
	}

	if !foundCall {
		t.Fatal("function_call item not found in marshaled output")
	}
	if functionCallID == "" {
		t.Fatal("function_call item has empty id")
	}
	if !foundOutput {
		t.Fatal("function_call_output item not found")
	}
}
