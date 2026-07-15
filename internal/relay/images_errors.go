package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	imagesUpstreamMessageLimit = 2048
	imagesRetryAfterLimit      = 128
)

type imagesUpstreamError struct {
	StatusCode int
	RetryAfter string
	Message    string
}

func newImagesUpstreamError(statusCode int, retryAfter string, body []byte) error {
	message := extractImagesErrorMessage(body)
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if message == "" {
		message = "upstream request failed"
	}
	return &imagesUpstreamError{
		StatusCode: statusCode,
		RetryAfter: sanitizeImagesRetryAfter(retryAfter),
		Message:    limitImagesErrorMessage(message),
	}
}

func (e *imagesUpstreamError) Error() string {
	return fmt.Sprintf("upstream error: %d: %s", e.StatusCode, e.Message)
}

func extractImagesErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if message := strings.TrimSpace(envelope.Error.Message); message != "" {
		return message
	}
	return strings.TrimSpace(envelope.Message)
}

func sanitizeImagesRetryAfter(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > imagesRetryAfterLimit || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func limitImagesErrorMessage(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= imagesUpstreamMessageLimit {
		return string(runes)
	}
	return string(runes[:imagesUpstreamMessageLimit])
}

func writeFinalImagesError(c *gin.Context, hb *earlyHeartbeat, err error) {
	statusCode := http.StatusBadGateway
	message := "all channels failed"

	var upstreamErr *imagesUpstreamError
	if errors.As(err, &upstreamErr) {
		if upstreamErr.StatusCode >= http.StatusBadRequest && upstreamErr.StatusCode <= 599 {
			statusCode = upstreamErr.StatusCode
		}
		if upstreamErr.Message != "" {
			message = upstreamErr.Message
		}
		if upstreamErr.RetryAfter != "" {
			c.Header("Retry-After", upstreamErr.RetryAfter)
		}
	}

	hb.FlushOrError(c, statusCode, message)
}
