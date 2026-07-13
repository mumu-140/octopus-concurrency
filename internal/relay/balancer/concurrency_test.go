package balancer

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestChannelConcurrencyLimit(t *testing.T) {
	Reset()
	var acquired atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if TryAcquireChannel(1, 3) {
				acquired.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := acquired.Load(); got != 3 {
		t.Fatalf("acquired %d slots, want 3", got)
	}
	if got := CurrentChannelConcurrency(1); got != 3 {
		t.Fatalf("current concurrency = %d, want 3", got)
	}
	for range 3 {
		ReleaseChannel(1)
	}
	if got := CurrentChannelConcurrency(1); got != 0 {
		t.Fatalf("current concurrency after release = %d, want 0", got)
	}
}

func TestUnlimitedChannelConcurrency(t *testing.T) {
	Reset()
	if !TryAcquireChannel(2, 0) {
		t.Fatal("unlimited channel rejected acquire")
	}
	if got := CurrentChannelConcurrency(2); got != 0 {
		t.Fatalf("unlimited channel was counted: %d", got)
	}
}
