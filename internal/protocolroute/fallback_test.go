package protocolroute

import "testing"

func TestClassifyProtocolFallbackAllowsOnlyExplicitPreExecutionMismatch(t *testing.T) {
	tests := []struct {
		name   string
		input  FallbackInput
		reason string
	}{
		{
			name: "structured endpoint code",
			input: FallbackInput{
				StatusCode: 404,
				ErrorBody:  `{"error":{"code":"endpoint_not_found","message":"Responses endpoint is unavailable"}}`,
			},
			reason: FallbackReasonEndpointNotFound,
		},
		{
			name:   "method not allowed",
			input:  FallbackInput{StatusCode: 405, ErrorBody: "method not allowed"},
			reason: FallbackReasonMethodNotAllowed,
		},
		{
			name: "explicit unsupported protocol",
			input: FallbackInput{
				StatusCode: 400,
				ErrorBody:  `{"error":{"type":"unsupported_protocol","message":"Anthropic protocol is not supported"}}`,
			},
			reason: FallbackReasonProtocolUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyProtocolFallback(test.input)
			if !got.Allowed || got.Reason != test.reason {
				t.Fatalf("decision = %+v, want allowed reason %q", got, test.reason)
			}
		})
	}
}

func TestClassifyProtocolFallbackRejectsUnsafeFailures(t *testing.T) {
	tests := []struct {
		name  string
		input FallbackInput
	}{
		{name: "connection error", input: FallbackInput{StatusCode: 0, ErrorBody: "i/o timeout"}},
		{name: "unauthorized", input: FallbackInput{StatusCode: 401, ErrorBody: `{"error":"invalid key"}`}},
		{name: "forbidden", input: FallbackInput{StatusCode: 403, ErrorBody: `{"error":"forbidden"}`}},
		{name: "rate limited", input: FallbackInput{StatusCode: 429, ErrorBody: `{"error":"rate limit"}`}},
		{name: "server error", input: FallbackInput{StatusCode: 500, ErrorBody: `{"error":"failed"}`}},
		{name: "gateway timeout", input: FallbackInput{StatusCode: 504, ErrorBody: "timeout"}},
		{
			name:  "model not found",
			input: FallbackInput{StatusCode: 404, ErrorBody: `{"error":{"code":"model_not_found","message":"model does not exist"}}`},
		},
		{name: "unknown not found", input: FallbackInput{StatusCode: 404, ErrorBody: `{"error":"not found"}`}},
		{
			name:  "parameter error",
			input: FallbackInput{StatusCode: 400, ErrorBody: `{"error":{"code":"invalid_request_error","message":"temperature is invalid"}}`},
		},
		{
			name: "upstream execution started",
			input: FallbackInput{
				StatusCode:      404,
				ErrorBody:       `{"error":{"code":"endpoint_not_found"}}`,
				UpstreamStarted: true,
			},
		},
		{
			name: "delivery started",
			input: FallbackInput{
				StatusCode:      404,
				ErrorBody:       `{"error":{"code":"endpoint_not_found"}}`,
				DeliveryStarted: true,
			},
		},
		{
			name: "fallback budget exhausted",
			input: FallbackInput{
				StatusCode:      404,
				ErrorBody:       `{"error":{"code":"endpoint_not_found"}}`,
				AlreadyFallback: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyProtocolFallback(test.input); got.Allowed || got.Reason != "" {
				t.Fatalf("decision = %+v, want denied without reason", got)
			}
		})
	}
}
