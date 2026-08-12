package balancer

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestStrictRandomNoImmediateRepeat(t *testing.T) {
	Reset()
	b := &StrictRandom{}
	items := []model.GroupItem{
		mkItem(1, 1, 301),
		mkItem(2, 1, 302),
		mkItem(3, 1, 303),
	}
	first := b.Candidates(items)
	second := b.Candidates(items)
	// 无放回：第二次的头不应与第一次的头相同（直到轮空再洗）
	if first[0].ChannelID == second[0].ChannelID {
		t.Fatalf("strict-random deck should not repeat head immediately: first=%d second=%d",
			first[0].ChannelID, second[0].ChannelID)
	}
}

func TestStrictRandomDeckReshufflesAfterExhaustion(t *testing.T) {
	Reset()
	b := &StrictRandom{}
	items := []model.GroupItem{
		mkItem(1, 1, 401),
		mkItem(2, 1, 402),
	}
	// 抽两次即轮空（n=2），第三次应重洗，仍返回合法集合
	for i := 0; i < 3; i++ {
		got := b.Candidates(items)
		if len(got) != 2 {
			t.Fatalf("iter %d: want 2, got %d", i, len(got))
		}
		seen := map[int]struct{}{}
		for _, it := range got {
			seen[it.ChannelID] = struct{}{}
		}
		if len(seen) != 2 {
			t.Fatalf("iter %d: incomplete set %v", i, seen)
		}
	}
}

func TestStrictRandomFingerprintStable(t *testing.T) {
	Reset()
	// 不同顺序的同集合 items 应命中同一 deck（指纹与顺序无关）
	itemsA := []model.GroupItem{
		mkItem(1, 1, 501),
		mkItem(2, 1, 502),
	}
	itemsB := []model.GroupItem{
		mkItem(2, 1, 502),
		mkItem(1, 1, 501),
	}
	if strictDeckKey(itemsA) != strictDeckKey(itemsB) {
		t.Fatal("fingerprint should be order-independent")
	}
}
