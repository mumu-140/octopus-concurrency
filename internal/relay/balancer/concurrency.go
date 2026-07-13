package balancer

import (
	"sync"
	"sync/atomic"
)

var globalConcurrency sync.Map // channel ID -> *atomic.Int64

func channelConcurrency(channelID int) *atomic.Int64 {
	value, _ := globalConcurrency.LoadOrStore(channelID, &atomic.Int64{})
	return value.(*atomic.Int64)
}

// TryAcquireChannel reserves one in-flight slot without exceeding maxConcurrency.
func TryAcquireChannel(channelID, maxConcurrency int) bool {
	if maxConcurrency <= 0 {
		return true
	}
	counter := channelConcurrency(channelID)
	for {
		current := counter.Load()
		if current >= int64(maxConcurrency) {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func ReleaseChannel(channelID int) {
	value, ok := globalConcurrency.Load(channelID)
	if !ok {
		return
	}
	counter := value.(*atomic.Int64)
	for {
		current := counter.Load()
		if current <= 0 {
			return
		}
		if counter.CompareAndSwap(current, current-1) {
			if current == 1 {
				globalConcurrency.CompareAndDelete(channelID, counter)
			}
			return
		}
	}
}

func CurrentChannelConcurrency(channelID int) int64 {
	value, ok := globalConcurrency.Load(channelID)
	if !ok {
		return 0
	}
	return value.(*atomic.Int64).Load()
}

func resetConcurrencyByChannel(channelID int) {
	globalConcurrency.Delete(channelID)
}
