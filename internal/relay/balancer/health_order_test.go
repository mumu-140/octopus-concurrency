package balancer

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/outlierwindow"
)

func mkItem(id, priority int, channelID int) model.GroupItem {
	return model.GroupItem{ID: id, Priority: priority, ChannelID: channelID, ModelName: "m"}
}

// channelsForHealth 标记一批通道为健康（多次成功）或差（多次失败）。
func seedHealth(channelID int, successes, failures int, now time.Time) {
	for i := 0; i < successes; i++ {
		outlierwindow.Report(channelID, true, 200, now)
	}
	for i := 0; i < failures; i++ {
		outlierwindow.Report(channelID, false, 500, now)
	}
}

func TestHealthFirstOrdersHealthyFirst(t *testing.T) {
	Reset()
	now := time.Now()
	// ch1 健康（10 成功），ch2 差（10 失败），ch3 冷启动（无样本）
	seedHealth(101, 10, 0, now)
	seedHealth(102, 0, 10, now)

	b := &HealthFirst{}
	items := []model.GroupItem{
		mkItem(1, 1, 102), // 差
		mkItem(2, 1, 103), // 冷启动 → 中性 0.5 → 降级档
		mkItem(3, 1, 101), // 健康
	}
	got := b.Candidates(items)
	// 健康档(101) 必须在降级档(103) 之前，差档(102) 最后
	if got[0].ChannelID != 101 {
		t.Fatalf("first should be healthy ch101, got %d", got[0].ChannelID)
	}
	if got[2].ChannelID != 102 {
		t.Fatalf("last should be bad ch102, got %d", got[2].ChannelID)
	}
}

func TestHealthFirstTierRotation(t *testing.T) {
	Reset()
	now := time.Now()
	// 三个同健康通道在同一档内，连续多次调用应轮换起始通道（不盯死）
	seedHealth(201, 10, 0, now)
	seedHealth(202, 10, 0, now)
	seedHealth(203, 10, 0, now)

	b := &HealthFirst{}
	items := []model.GroupItem{
		mkItem(1, 1, 201),
		mkItem(2, 1, 202),
		mkItem(3, 1, 203),
	}
	seenFirst := map[int]struct{}{}
	for i := 0; i < 30; i++ {
		got := b.Candidates(items)
		seenFirst[got[0].ChannelID] = struct{}{}
		// 所有返回的应仍是这三个通道
		if len(got) != 3 {
			t.Fatalf("iter %d: got %d candidates, want 3", i, len(got))
		}
	}
	if len(seenFirst) < 2 {
		t.Fatalf("expected rotation across >=2 first channels, got %d distinct", len(seenFirst))
	}
}

func TestHealthFirstColdStartNeutral(t *testing.T) {
	Reset()
	b := &HealthFirst{}
	items := []model.GroupItem{
		mkItem(1, 1, 301),
		mkItem(2, 2, 302),
	}
	got := b.Candidates(items)
	// 双方均冷启动中性分 0.5，同档（降级档）会轮换；仅校验两通道都在结果中
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(got))
	}
	seen := map[int]struct{}{}
	for _, it := range got {
		seen[it.ChannelID] = struct{}{}
	}
	if len(seen) != 2 {
		t.Fatalf("expected both cold-start channels, got %v", seen)
	}
}
