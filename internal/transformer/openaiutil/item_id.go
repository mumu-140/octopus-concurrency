package openaiutil

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var itemIDCounter atomic.Uint64

// ItemIDPrefix returns the Responses API item ID prefix for a known item type.
func ItemIDPrefix(itemType string) string {
	switch itemType {
	case "reasoning":
		return "rs"
	case "message":
		return "msg"
	case "function_call":
		return "fc"
	case "function_call_output":
		return "fco"
	case "custom_tool_call":
		return "ctc"
	case "custom_tool_call_output":
		return "ctco"
	case "image_generation_call":
		return "ig"
	default:
		return ""
	}
}

// NewItemID creates an opaque ID with the prefix expected for itemType.
func NewItemID(itemType string) string {
	prefix := ItemIDPrefix(itemType)
	if prefix == "" {
		prefix = "item"
	}

	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s_%016x%08x", prefix, time.Now().UnixNano(), itemIDCounter.Add(1))
	}
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return prefix + "_" + string(b)
}

// NormalizeLegacyItemID converts a generic item_ ID to a known type prefix.
func NormalizeLegacyItemID(itemType, id string) (string, bool) {
	prefix := ItemIDPrefix(itemType)
	if prefix == "" || !strings.HasPrefix(id, "item_") {
		return id, false
	}
	return prefix + "_" + strings.TrimPrefix(id, "item_"), true
}
