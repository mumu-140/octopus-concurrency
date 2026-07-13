package balancer

import (
	"testing"
	"time"
)

func TestChannelRPMLimit(t *testing.T) {
	Reset()
	now := time.Unix(100, 0)
	for range 3 {
		if !TryConsumeChannelRPM(7, 3, now) {
			t.Fatal("request rejected before RPM limit")
		}
	}
	if TryConsumeChannelRPM(7, 3, now) {
		t.Fatal("request accepted above RPM limit")
	}
	if !TryConsumeChannelRPM(7, 3, now.Add(time.Minute)) {
		t.Fatal("request rejected after window reset")
	}
}
