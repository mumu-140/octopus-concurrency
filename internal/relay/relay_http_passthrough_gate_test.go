package relay

import (
	"testing"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

// TestRawBodyStillAuthoritative pins the passthrough gate that keeps gateway-side
// request compression effective.
//
// Background: compression rewrites InternalLLMRequest.Messages in place, but the
// captured rawBody keeps the original client payload. The raw passthrough path
// forwards rawBody verbatim (rewriting only the model field), so a compressed
// request taking that path would reach the upstream uncompressed while the relay
// log still reported CompressSavedPct.
func TestRawBodyStillAuthoritative(t *testing.T) {
	t.Run("nil request is never authoritative", func(t *testing.T) {
		if rawBodyStillAuthoritative(nil) {
			t.Fatal("nil request must not be treated as authoritative")
		}
	})

	t.Run("uncompressed request keeps passthrough", func(t *testing.T) {
		req := &transformerModel.InternalLLMRequest{}
		if !rawBodyStillAuthoritative(req) {
			t.Fatal("request without CompressStats must stay eligible for passthrough")
		}
	})

	t.Run("compressed request loses passthrough", func(t *testing.T) {
		req := &transformerModel.InternalLLMRequest{
			CompressStats: &transformerModel.CompressStats{
				BeforeBytes: 4096,
				AfterBytes:  2048,
			},
		}
		if rawBodyStillAuthoritative(req) {
			t.Fatal("compressed request must fall through to the standard path so the compressed messages are re-serialized")
		}
	})
}
