package price

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func setTestPreset(t *testing.T, name string, value model.LLMPrice) {
	t.Helper()
	llmPriceLock.Lock()
	previous, existed := llmPrice[name]
	llmPrice[name] = value
	llmPriceLock.Unlock()
	t.Cleanup(func() {
		llmPriceLock.Lock()
		defer llmPriceLock.Unlock()
		if existed {
			llmPrice[name] = previous
		} else {
			delete(llmPrice, name)
		}
	})
}

func TestResolveLLMPriceFallsBackFromZeroActualToRequestModel(t *testing.T) {
	setTestPreset(t, "test-zero-actual", model.LLMPrice{})
	setTestPreset(t, "test-priced-group", model.LLMPrice{Input: 2, Output: 8, CacheRead: 0.2})

	got := ResolveLLMPrice("test-zero-actual", "test-priced-group")
	if got == nil || got.Input != 2 || got.Output != 8 || got.CacheRead != 0.2 {
		t.Fatalf("unexpected resolved price: %#v", got)
	}
}

func TestResolveLLMPriceKeepsNonZeroActualPrice(t *testing.T) {
	setTestPreset(t, "test-priced-actual", model.LLMPrice{Input: 1, Output: 4})
	setTestPreset(t, "test-priced-request", model.LLMPrice{Input: 2, Output: 8})

	got := ResolveLLMPrice("test-priced-actual", "test-priced-request")
	if got == nil || got.Input != 1 || got.Output != 4 {
		t.Fatalf("unexpected resolved price: %#v", got)
	}
}
