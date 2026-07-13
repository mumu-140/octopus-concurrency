package balancer

import (
	"sync"
	"time"
)

type channelRateWindow struct {
	mu      sync.Mutex
	started time.Time
	count   int
}

var globalChannelRate sync.Map // channel ID -> *channelRateWindow

// TryConsumeChannelRPM consumes one request from the channel's fixed one-minute window.
func TryConsumeChannelRPM(channelID, maxRPM int, now time.Time) bool {
	if maxRPM <= 0 {
		return true
	}
	value, _ := globalChannelRate.LoadOrStore(channelID, &channelRateWindow{started: now})
	window := value.(*channelRateWindow)
	window.mu.Lock()
	defer window.mu.Unlock()
	if now.Sub(window.started) >= time.Minute {
		window.started = now
		window.count = 0
	}
	if window.count >= maxRPM {
		return false
	}
	window.count++
	return true
}

func resetRateByChannel(channelID int) {
	globalChannelRate.Delete(channelID)
}
