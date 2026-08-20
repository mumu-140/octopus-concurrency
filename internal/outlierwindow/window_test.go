package outlierwindow

import (
	"testing"
	"time"
)

func resetConfig() { Configure(defaultConfig) }

// testModel 单模型场景下的固定模型名。
const testModel = "m-test"

// report 连续上报 n 条同结果样本，时间从 base 起每条间隔 1s。
func report(channelID int, success bool, n int, base time.Time) time.Time {
	t := base
	for i := 0; i < n; i++ {
		Report(channelID, testModel, success, 0, t)
		t = t.Add(time.Second)
	}
	return t
}

func TestGate1_InsufficientSamples(t *testing.T) {
	resetConfig()
	const ch = 1001
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(ch, false, 7, base) // 7 < MinSamples(8)

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.Samples != 7 {
		t.Fatalf("Samples = %d, want 7", st.Samples)
	}
	if st.Candidate {
		t.Fatal("样本不足应 PASS（Candidate=false）")
	}
}

func TestGate1_DilutedBySuccess(t *testing.T) {
	resetConfig()
	const ch = 1002
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 10 失败 + 10 成功，失败率 0.5 < 0.85
	t2 := report(ch, false, 10, base)
	report(ch, true, 10, t2)

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.Samples != 20 {
		t.Fatalf("Samples = %d, want 20", st.Samples)
	}
	if st.FailureRate >= 0.85 {
		t.Fatalf("FailureRate = %.2f, 不应达标", st.FailureRate)
	}
	if st.Candidate {
		t.Fatal("有成功稀释应 PASS")
	}
}

func TestGate1_ConsecutiveFailures(t *testing.T) {
	resetConfig()
	const ch = 1003
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(ch, false, 12, base) // 连续 12 次失败

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.ConsecutiveFails != 12 {
		t.Fatalf("ConsecutiveFails = %d, want 12", st.ConsecutiveFails)
	}
	if st.FailureRate != 1.0 {
		t.Fatalf("FailureRate = %.2f, want 1.0", st.FailureRate)
	}
	if !st.Candidate {
		t.Fatal("连续失败达标应成为候选")
	}
}

func TestGate1_NoSuccessTriggersCandidate(t *testing.T) {
	resetConfig()
	const ch = 1004
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(ch, false, 9, base) // 9 失败：consecutive 9 < 10，但窗口内无成功

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.ConsecutiveFails != 9 {
		t.Fatalf("ConsecutiveFails = %d, want 9", st.ConsecutiveFails)
	}
	if !st.LastSuccessAt.IsZero() {
		t.Fatal("不应有成功记录")
	}
	if !st.Candidate {
		t.Fatal("窗口内无成功应成为候选（noSuccess 分支）")
	}
}

func TestGate1_RecoveringNotCandidate(t *testing.T) {
	resetConfig()
	const ch = 1005
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 18 失败后 2 次成功：失败率 0.9 达标，但最新是成功（consecutive=0、有近成功）
	t2 := report(ch, false, 18, base)
	report(ch, true, 2, t2)

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.FailureRate < 0.85 {
		t.Fatalf("FailureRate = %.2f, 应达标", st.FailureRate)
	}
	if st.ConsecutiveFails != 0 {
		t.Fatalf("ConsecutiveFails = %d, want 0", st.ConsecutiveFails)
	}
	if st.Candidate {
		t.Fatal("正在恢复（最新成功）不应退役")
	}
}

func TestTimeExpiry(t *testing.T) {
	resetConfig()
	const ch = 1006
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(ch, false, 12, base) // 全部发生在 base 附近

	// 在 base + 20min 评估，TimeWindow=10min → 全部过期
	st := Evaluate(ch, testModel, base.Add(20*time.Minute))
	if st.Samples != 0 {
		t.Fatalf("Samples = %d, want 0（应全部过期）", st.Samples)
	}
	if st.Candidate {
		t.Fatal("样本全过期应 PASS")
	}
}

func TestRingWraparound(t *testing.T) {
	resetConfig()
	const ch = 1007
	Clear(ch, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 先 5 次成功，再 25 次失败；物理 cap=20，成功会被覆盖出窗
	t2 := report(ch, true, 5, base)
	report(ch, false, 25, t2)

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.Samples != physicalCap {
		t.Fatalf("Samples = %d, want %d", st.Samples, physicalCap)
	}
	if st.Failures != physicalCap {
		t.Fatalf("Failures = %d, want %d（成功应被环形覆盖）", st.Failures, physicalCap)
	}
	if !st.Candidate {
		t.Fatal("满窗全失败应成为候选")
	}
}

func TestClear(t *testing.T) {
	resetConfig()
	const ch = 1008
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(ch, false, 12, base)
	Clear(ch, testModel)

	st := Evaluate(ch, testModel, base.Add(time.Minute))
	if st.Samples != 0 {
		t.Fatalf("Clear 后 Samples = %d, want 0", st.Samples)
	}
}

func TestReap(t *testing.T) {
	resetConfig()
	const chOld = 1009
	const chFresh = 1010
	Clear(chOld, testModel)
	Clear(chFresh, testModel)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	report(chOld, false, 3, base)                  // lastSeen ≈ base
	report(chFresh, false, 3, base.Add(time.Hour)) // lastSeen ≈ base+1h

	// 在 base+1h 回收 ttl=30min：chOld 过老被回收，chFresh 保留
	reaped := Reap(base.Add(time.Hour), 30*time.Minute)
	if reaped < 1 {
		t.Fatalf("Reap = %d, 至少应回收 chOld", reaped)
	}
	if _, ok := store.Load(windowKey{ChannelID: chOld, Model: testModel}); ok {
		t.Fatal("chOld 应被回收")
	}
	if _, ok := store.Load(windowKey{ChannelID: chFresh, Model: testModel}); !ok {
		t.Fatal("chFresh 不应被回收")
	}
}

func TestConfigureClamp(t *testing.T) {
	Configure(Config{Capacity: 999, TimeWindow: 0, MinSamples: 0, FailRate: 2, ConsecFails: 0})
	c := currentConfig()
	if c.Capacity != physicalCap {
		t.Fatalf("Capacity = %d, 应封顶到 %d", c.Capacity, physicalCap)
	}
	if c.TimeWindow != defaultConfig.TimeWindow {
		t.Fatal("非法 TimeWindow 应回退默认")
	}
	if c.FailRate != defaultConfig.FailRate {
		t.Fatal("非法 FailRate 应回退默认")
	}
	resetConfig()
}

// reportModel 连续上报 n 条同结果样本到指定渠道-模型。
func reportModel(channelID int, modelName string, success bool, n int, base time.Time) time.Time {
	t := base
	for i := 0; i < n; i++ {
		Report(channelID, modelName, success, 0, t)
		t = t.Add(time.Second)
	}
	return t
}

// TestModelIsolation 同渠道不同模型互不污染：这是本次改造的核心承诺。
// 若键退回渠道单粒度，m2 会看到 m1 的 10 条失败，断言失败。
func TestModelIsolation(t *testing.T) {
	resetConfig()
	const ch = 2001
	ClearChannel(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportModel(ch, "m1", false, 10, base)

	now := base.Add(time.Minute)
	if st := Evaluate(ch, "m1", now); st.Samples != 10 || st.Failures != 10 {
		t.Fatalf("m1: Samples=%d Failures=%d, want 10/10", st.Samples, st.Failures)
	}
	if st := Evaluate(ch, "m2", now); st.Samples != 0 {
		t.Fatalf("m2 应无样本，got Samples=%d Failures=%d（模型级隔离被破坏）", st.Samples, st.Failures)
	}
}

// TestReportChannelFansOutToAllModels 渠道作用域失败铺到该渠道全部已知模型子键。
func TestReportChannelFansOutToAllModels(t *testing.T) {
	resetConfig()
	const ch = 2002
	const other = 2003
	ClearChannel(ch)
	ClearChannel(other)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 先建立两个模型子键，各 1 条成功
	reportModel(ch, "m1", true, 1, base)
	reportModel(ch, "m2", true, 1, base)
	reportModel(other, "m1", true, 1, base)

	ReportChannel(ch, "m1", false, 503, base.Add(time.Second))

	now := base.Add(time.Minute)
	for _, m := range []string{"m1", "m2"} {
		st := Evaluate(ch, m, now)
		if st.Samples != 2 || st.Failures != 1 {
			t.Fatalf("%s: Samples=%d Failures=%d, want 2/1（渠道级失败未铺开）", m, st.Samples, st.Failures)
		}
	}
	// 不得越界到其他渠道
	if st := Evaluate(other, "m1", now); st.Failures != 0 {
		t.Fatalf("其他渠道被污染：Failures=%d", st.Failures)
	}
}

// TestReportChannelBootstrapsWhenNoSubKey 该渠道尚无任何子键时，至少写入本次请求的模型键。
func TestReportChannelBootstrapsWhenNoSubKey(t *testing.T) {
	resetConfig()
	const ch = 2004
	ClearChannel(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	ReportChannel(ch, "m1", false, 0, base)

	st := Evaluate(ch, "m1", base.Add(time.Minute))
	if st.Samples != 1 || st.Failures != 1 {
		t.Fatalf("Samples=%d Failures=%d, want 1/1（无子键时未兜底写入）", st.Samples, st.Failures)
	}
}

// TestEvaluateChannelAggregatesModels POR 门1 的渠道聚合口径：
// Samples/Failures 求和，ConsecutiveFails 取 max 而非 sum。
func TestEvaluateChannelAggregatesModels(t *testing.T) {
	resetConfig()
	const ch = 2005
	ClearChannel(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// m1: 2 成功 + 2 连续失败；m2: 1 成功 + 3 连续失败
	t1 := reportModel(ch, "m1", true, 2, base)
	reportModel(ch, "m1", false, 2, t1)
	t2 := reportModel(ch, "m2", true, 1, base)
	reportModel(ch, "m2", false, 3, t2)

	st := EvaluateChannel(ch, base.Add(time.Minute))
	if st.Samples != 8 {
		t.Fatalf("Samples = %d, want 8", st.Samples)
	}
	if st.Failures != 5 {
		t.Fatalf("Failures = %d, want 5", st.Failures)
	}
	if st.ConsecutiveFails != 3 {
		t.Fatalf("ConsecutiveFails = %d, want 3（应取 max，不是求和 5）", st.ConsecutiveFails)
	}
	if st.FailureRate < 0.62 || st.FailureRate > 0.63 {
		t.Fatalf("FailureRate = %.4f, want ≈0.625", st.FailureRate)
	}
	// 失败率 0.625 < 0.85 → 门1 不应判候选
	if st.Candidate {
		t.Fatal("聚合失败率未达阈值，不应成为候选")
	}
}

// TestEvaluateChannelEmpty 无任何子键时返回零值且不判候选。
func TestEvaluateChannelEmpty(t *testing.T) {
	resetConfig()
	const ch = 2006
	ClearChannel(ch)
	st := EvaluateChannel(ch, time.Now())
	if st.Samples != 0 || st.Candidate {
		t.Fatalf("空渠道 st = %+v, want 零值且非候选", st)
	}
}

// TestClearChannelRemovesAllModels ClearChannel 清空该渠道全部模型子键，且不越界。
func TestClearChannelRemovesAllModels(t *testing.T) {
	resetConfig()
	const ch = 2007
	const other = 2008
	ClearChannel(ch)
	ClearChannel(other)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportModel(ch, "m1", false, 3, base)
	reportModel(ch, "m2", false, 3, base)
	reportModel(other, "m1", false, 3, base)

	ClearChannel(ch)

	now := base.Add(time.Minute)
	if st := EvaluateChannel(ch, now); st.Samples != 0 {
		t.Fatalf("ClearChannel 后 Samples = %d, want 0", st.Samples)
	}
	if st := Evaluate(other, "m1", now); st.Samples != 3 {
		t.Fatalf("其他渠道被误清：Samples = %d, want 3", st.Samples)
	}
}
