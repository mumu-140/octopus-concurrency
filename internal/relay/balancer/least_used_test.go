package balancer

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestLeastUsedOrdersByConcurrency(t *testing.T) {
	Reset()
	// ch1 在途 3，ch2 在途 0，ch3 在途 1
	TryAcquireChannel(1, 100)
	TryAcquireChannel(1, 100)
	TryAcquireChannel(1, 100)
	TryAcquireChannel(3, 100)
	// ch2 不 acquire，保持 0

	b := &LeastUsed{}
	items := []model.GroupItem{
		mkItem(1, 1, 1), // conc 3
		mkItem(2, 1, 2), // conc 0
		mkItem(3, 1, 3), // conc 1
	}
	got := b.Candidates(items)
	if got[0].ChannelID != 2 {
		t.Fatalf("least-used first should be ch2 (conc 0), got %d", got[0].ChannelID)
	}
	if got[1].ChannelID != 3 {
		t.Fatalf("second should be ch3 (conc 1), got %d", got[1].ChannelID)
	}
	if got[2].ChannelID != 1 {
		t.Fatalf("last should be ch1 (conc 3), got %d", got[2].ChannelID)
	}
}

func TestLeastUsedTieBreakPriority(t *testing.T) {
	Reset()
	// 两个通道均 0 并发，按 Priority 升序
	b := &LeastUsed{}
	items := []model.GroupItem{
		{ChannelID: 11, Priority: 5, ModelName: "m"},
		{ChannelID: 12, Priority: 2, ModelName: "m"},
	}
	got := b.Candidates(items)
	if got[0].ChannelID != 12 {
		t.Fatalf("tie-break should pick lower Priority ch12, got %d", got[0].ChannelID)
	}
}
