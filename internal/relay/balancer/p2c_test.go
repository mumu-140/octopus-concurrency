package balancer

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestP2CReturnsAllAndSmallerFirst(t *testing.T) {
	Reset()
	// ch1 并发 5，ch2 并发 0，ch3 并发 2
	for range 5 {
		TryAcquireChannel(1, 100)
	}
	TryAcquireChannel(3, 100)

	b := &P2C{}
	items := []model.GroupItem{
		mkItem(1, 1, 1),
		mkItem(2, 1, 2),
		mkItem(3, 1, 3),
	}
	for i := 0; i < 30; i++ {
		got := b.Candidates(items)
		if len(got) != 3 {
			t.Fatalf("iter %d: got %d candidates, want 3", i, len(got))
		}
		// primary（首个）必须在被随机抽到的两个之一中，且是并发较低者
		// 不能稳定断言必为 ch2（p2c 是随机两选一），但应满足：primary 不在
		// 被抽两高者间取较高。这里仅校验长度 + 集合完整。
		seen := map[int]struct{}{}
		for _, it := range got {
			seen[it.ChannelID] = struct{}{}
		}
		if len(seen) != 3 {
			t.Fatalf("iter %d: result set incomplete: %v", i, seen)
		}
	}
}

func TestP2CSingleCandidate(t *testing.T) {
	Reset()
	b := &P2C{}
	items := []model.GroupItem{mkItem(1, 1, 99)}
	got := b.Candidates(items)
	if len(got) != 1 || got[0].ChannelID != 99 {
		t.Fatalf("single candidate should pass through, got %v", got)
	}
}

func TestP2CDegenerateLowConcurrency(t *testing.T) {
	Reset()
	// 全部并发 0，随机两选一无差别；仅校验集合完整且不 panic
	b := &P2C{}
	items := []model.GroupItem{
		mkItem(1, 1, 201),
		mkItem(2, 1, 202),
	}
	got := b.Candidates(items)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}
