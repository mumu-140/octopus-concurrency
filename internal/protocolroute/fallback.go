package protocolroute

import (
	"encoding/json"
	"net/http"
	"strings"
)

const (
	FallbackReasonEndpointNotFound    = "endpoint_not_found"
	FallbackReasonMethodNotAllowed    = "method_not_allowed"
	FallbackReasonProtocolUnsupported = "protocol_unsupported"
)

// FallbackInput describes the observable result of one protocol attempt.
type FallbackInput struct {
	StatusCode      int
	ErrorBody       string
	UpstreamStarted bool
	DeliveryStarted bool
	AlreadyFallback bool
}

// FallbackDecision permits only a closed set of pre-execution mismatches.
type FallbackDecision struct {
	Allowed bool
	Reason  string
}

var explicitMismatchCodes = map[string]string{
	"endpoint_not_found":       FallbackReasonEndpointNotFound,
	"route_not_found":          FallbackReasonEndpointNotFound,
	"unsupported_endpoint":     FallbackReasonProtocolUnsupported,
	"endpoint_not_supported":   FallbackReasonProtocolUnsupported,
	"unsupported_api":          FallbackReasonProtocolUnsupported,
	"unsupported_protocol":     FallbackReasonProtocolUnsupported,
	"api_format_not_supported": FallbackReasonProtocolUnsupported,
}

// ClassifyProtocolFallback returns an allowed decision only when retrying with
// another protocol cannot duplicate model execution or client delivery.
func ClassifyProtocolFallback(in FallbackInput) FallbackDecision {
	if in.UpstreamStarted || in.DeliveryStarted || in.AlreadyFallback {
		return FallbackDecision{}
	}
	if in.StatusCode != http.StatusBadRequest &&
		in.StatusCode != http.StatusNotFound &&
		in.StatusCode != http.StatusMethodNotAllowed {
		return FallbackDecision{}
	}

	code, message := fallbackErrorFields(in.ErrorBody)
	if modelFailure(code, message, in.ErrorBody) {
		return FallbackDecision{}
	}
	if in.StatusCode == http.StatusMethodNotAllowed {
		return FallbackDecision{Allowed: true, Reason: FallbackReasonMethodNotAllowed}
	}
	if reason := explicitMismatchCodes[code]; reason != "" {
		return FallbackDecision{Allowed: true, Reason: reason}
	}
	return fallbackDecisionFromMessage(message + " " + in.ErrorBody)
}

func fallbackErrorFields(body string) (string, string) {
	var envelope struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &envelope)
	code := firstNonEmpty(envelope.Error.Code, envelope.Error.Type, envelope.Code, envelope.Type)
	message := firstNonEmpty(envelope.Error.Message, envelope.Message)
	return strings.ToLower(strings.TrimSpace(code)), strings.ToLower(strings.TrimSpace(message))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func modelFailure(values ...string) bool {
	joined := strings.ToLower(strings.Join(values, " "))
	return strings.Contains(joined, "model_not_found") ||
		strings.Contains(joined, "model not found") ||
		strings.Contains(joined, "model does not exist")
}

func fallbackDecisionFromMessage(value string) FallbackDecision {
	lower := strings.ToLower(value)
	endpointSignals := []string{
		"unsupported endpoint", "endpoint is not supported", "endpoint not supported",
		"unknown endpoint", "endpoint not found", "route not found", "unknown route",
		"unknown api endpoint", "cannot post /v1/",
	}
	for _, signal := range endpointSignals {
		if strings.Contains(lower, signal) {
			return FallbackDecision{Allowed: true, Reason: FallbackReasonEndpointNotFound}
		}
	}
	if strings.Contains(lower, "unsupported protocol") || strings.Contains(lower, "protocol is not supported") {
		return FallbackDecision{Allowed: true, Reason: FallbackReasonProtocolUnsupported}
	}
	return FallbackDecision{}
}
