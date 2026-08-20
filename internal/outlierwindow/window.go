// Package outlierwindow 维护按「渠道-模型」（channelID + modelName）的进程内滚动成败窗口，
// 为被动离群退役（POR）的「门1：滚动窗口聚合」与选路健康度提供证据。
//
// 设计要点：
//   - 数据面（relay）在每次真实请求最终结果点调用 Report，纳秒级、非阻塞。
//   - 控制面（task）周期调用 EvaluateChannel 获取「渠道聚合」窗口统计 + 门1初判；
//     选路侧（balancer）调用 Evaluate 获取单个渠道-模型的窗口统计。
//   - 纯内存、重启清空（与熔断器 internal/relay/balancer/circuit.go 同构）；
//     退役状态的持久化由 model.SiteChannelOutlierState 负责，不在本包。
//   - 本包零依赖 op，阈值由 task 侧通过 Configure 注入，避免循环依赖。
package outlierwindow

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/utils/log"
)

// physicalCap 是环形缓冲的物理容量（编译期常量）。
// Config.Capacity 仅作「评估时取最近 N 条」的逻辑上限，不能超过该值。
const physicalCap = 20

type sample struct {
	at      time.Time
	success bool
}

// ringWindow 单个渠道的定长环形缓冲。零堆分配，原地加锁修改。
type ringWindow struct {
	mu       sync.Mutex
	buf      [physicalCap]sample
	size     int       // 已填充数 (<=physicalCap)
	next     int       // 下一写入位
	lastSeen time.Time // 最近 Report 时间，用于内存回收
}

// Config 门1判定阈值，由控制面 task 每轮注入。
type Config struct {
	Capacity    int           // 评估时取最近 N 条（≤physicalCap）
	TimeWindow  time.Duration // T：样本过期时长
	MinSamples  int           // 样本不足直接 PASS
	FailRate    float64       // 失败率阈值 (0,1]
	ConsecFails int           // 连续失败阈值
}

var defaultConfig = Config{
	Capacity:    physicalCap,
	TimeWindow:  10 * time.Minute,
	MinSamples:  8,
	FailRate:    0.85,
	ConsecFails: 10,
}

// windowKey 是窗口的唯一键：渠道-模型二元组。
// 与熔断器 internal/relay/balancer/circuit.go 的 channel:key:model 三元粒度对齐到
// 渠道-模型这一层（不含 keyID：密钥级失败由熔断器负责，窗口只描述「渠道能否服务该模型」）。
type windowKey struct {
	ChannelID int
	Model     string
}

var (
	store     sync.Map // windowKey -> *ringWindow
	configPtr atomic.Pointer[Config]
)

func init() {
	c := defaultConfig
	configPtr.Store(&c)
}

func currentConfig() Config {
	if c := configPtr.Load(); c != nil {
		return *c
	}
	return defaultConfig
}

// Configure 注入门1阈值（无锁热更）。非法值回退默认；Capacity 超过物理上限按上限封顶。
func Configure(c Config) {
	if c.Capacity <= 0 || c.Capacity > physicalCap {
		c.Capacity = physicalCap
	}
	if c.TimeWindow <= 0 {
		c.TimeWindow = defaultConfig.TimeWindow
	}
	if c.MinSamples <= 0 {
		c.MinSamples = defaultConfig.MinSamples
	}
	if c.FailRate <= 0 || c.FailRate > 1 {
		c.FailRate = defaultConfig.FailRate
	}
	if c.ConsecFails <= 0 {
		c.ConsecFails = defaultConfig.ConsecFails
	}
	configPtr.Store(&c)
}

// PhysicalCap 返回环形缓冲的物理容量，供调用方校验 Capacity 上限。
func PhysicalCap() int { return physicalCap }

func getOrCreate(k windowKey) *ringWindow {
	if v, ok := store.Load(k); ok {
		return v.(*ringWindow)
	}
	w := &ringWindow{}
	actual, _ := store.LoadOrStore(k, w)
	return actual.(*ringWindow)
}

// Report 数据面每次真实请求最终结果调用。非阻塞、best-effort，内部 recover 兜底绝不冒泡。
// statusCode 当前不参与门1判定（门1只看成败序列），保留入参供未来扩展。
func Report(channelID int, modelName string, success bool, statusCode int, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("outlierwindow report panic: %v", r)
		}
	}()
	_ = statusCode
	record(getOrCreate(windowKey{ChannelID: channelID, Model: modelName}), success, now)
}

// ReportChannel 把一次「整渠道级」失败/成功写入该渠道当前所有已知模型子窗口。
// 用于渠道作用域错误（配额耗尽、令牌失效、连接被重置等，与具体模型无关）。
// 本次请求的 (channelID, modelName) 无条件写入（它是这次失败的直接当事者，
// 可能还没有子窗口），其余同渠道子窗口再铺开一遍，同一个键不重复计数。
// 不新增「渠道级独立键」，保持存储只有一种键形态。
func ReportChannel(channelID int, modelName string, success bool, statusCode int, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("outlierwindow report panic: %v", r)
		}
	}()
	_ = statusCode
	self := windowKey{ChannelID: channelID, Model: modelName}
	record(getOrCreate(self), success, now)
	store.Range(func(key, value any) bool {
		k, ok := key.(windowKey)
		if !ok || k.ChannelID != channelID || k == self {
			return true
		}
		w, ok := value.(*ringWindow)
		if !ok {
			return true
		}
		record(w, success, now)
		return true
	})
}

func record(w *ringWindow, success bool, now time.Time) {
	w.mu.Lock()
	w.buf[w.next] = sample{at: now, success: success}
	w.next = (w.next + 1) % physicalCap
	if w.size < physicalCap {
		w.size++
	}
	w.lastSeen = now
	w.mu.Unlock()
}

// WindowStats 窗口统计 + 门1初判结果。
type WindowStats struct {
	Samples          int       // 窗口内有效（未过期、取最近 Capacity）样本数
	Failures         int       // 其中失败数
	FailureRate      float64   // Failures/Samples；Samples=0 时为 0
	ConsecutiveFails int       // 从最新往回数的连续失败数
	LastSuccessAt    time.Time // 有效样本中最近一次成功；无则零值
	LastSampleAt     time.Time // 有效样本中最近一次的时间
	Candidate        bool      // 门1判定：true=应进门2
}

// Evaluate 选路侧调用：返回单个渠道-模型的窗口统计 + 门1判定。惰性过期裁剪，不清窗。
func Evaluate(channelID int, modelName string, now time.Time) WindowStats {
	v, ok := store.Load(windowKey{ChannelID: channelID, Model: modelName})
	if !ok {
		return WindowStats{}
	}
	return v.(*ringWindow).evaluate(now, currentConfig())
}

// EvaluateChannel 控制面（POR 门1）调用：把该渠道下全部模型子窗口聚合成渠道视角统计。
// 聚合规则：Samples/Failures 求和；FailureRate 按聚合值重算；
// ConsecutiveFails 取各子窗口最大值（不求和——不同模型的连续失败不构成同一条失败序列）；
// LastSuccessAt / LastSampleAt 取最大值。随后用聚合值重跑门1三档判定。
func EvaluateChannel(channelID int, now time.Time) WindowStats {
	c := currentConfig()
	var st WindowStats
	found := false
	store.Range(func(key, value any) bool {
		k, ok := key.(windowKey)
		if !ok || k.ChannelID != channelID {
			return true
		}
		w, ok := value.(*ringWindow)
		if !ok {
			return true
		}
		sub := w.evaluate(now, c)
		if sub.Samples == 0 {
			return true
		}
		found = true
		st.Samples += sub.Samples
		st.Failures += sub.Failures
		if sub.ConsecutiveFails > st.ConsecutiveFails {
			st.ConsecutiveFails = sub.ConsecutiveFails
		}
		if sub.LastSuccessAt.After(st.LastSuccessAt) {
			st.LastSuccessAt = sub.LastSuccessAt
		}
		if sub.LastSampleAt.After(st.LastSampleAt) {
			st.LastSampleAt = sub.LastSampleAt
		}
		return true
	})
	if !found || st.Samples == 0 {
		return WindowStats{}
	}
	st.FailureRate = float64(st.Failures) / float64(st.Samples)
	st.Candidate = gate1(st, c)
	return st
}

// gate1 门1三档判定：样本不足 PASS；失败率未达阈值 PASS；
// 否则 连续失败达标 或 窗口内无任何成功 → Candidate。
func gate1(st WindowStats, c Config) bool {
	if st.Samples < c.MinSamples {
		return false
	}
	if st.FailureRate < c.FailRate {
		return false
	}
	return st.ConsecutiveFails >= c.ConsecFails || st.LastSuccessAt.IsZero()
}

// orderedLocked 按时间从旧到新返回有效样本（调用方需持锁）。
func (w *ringWindow) orderedLocked() []sample {
	if w.size == 0 {
		return nil
	}
	start := 0
	if w.size == physicalCap {
		start = w.next // 缓冲已满时最旧元素在 next 处
	}
	out := make([]sample, 0, w.size)
	for i := 0; i < w.size; i++ {
		out = append(out, w.buf[(start+i)%physicalCap])
	}
	return out
}

func (w *ringWindow) evaluate(now time.Time, c Config) WindowStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	ordered := w.orderedLocked()
	cutoff := now.Add(-c.TimeWindow)
	valid := make([]sample, 0, len(ordered))
	for _, s := range ordered {
		if s.at.After(cutoff) {
			valid = append(valid, s)
		}
	}
	if len(valid) > c.Capacity {
		valid = valid[len(valid)-c.Capacity:]
	}

	var st WindowStats
	st.Samples = len(valid)
	if st.Samples == 0 {
		return st
	}
	for _, s := range valid {
		if s.success {
			st.LastSuccessAt = s.at
		} else {
			st.Failures++
		}
	}
	st.LastSampleAt = valid[len(valid)-1].at
	st.FailureRate = float64(st.Failures) / float64(st.Samples)
	for i := len(valid) - 1; i >= 0; i-- {
		if valid[i].success {
			break
		}
		st.ConsecutiveFails++
	}

	st.Candidate = gate1(st, c)
	return st
}

// Clear 清空指定渠道-模型窗口（该模型探活成功/恢复后调用，重新积累证据）。
func Clear(channelID int, modelName string) {
	store.Delete(windowKey{ChannelID: channelID, Model: modelName})
}

// ClearChannel 清空指定渠道下全部模型子窗口（渠道探活成功/退役恢复后调用）。
func ClearChannel(channelID int) {
	store.Range(func(key, _ any) bool {
		if k, ok := key.(windowKey); ok && k.ChannelID == channelID {
			store.Delete(k)
		}
		return true
	})
}

// Reap 回收 lastSeen 早于 now-ttl 的窗口（已删除/长期无流量渠道），返回回收数。
func Reap(now time.Time, ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	cutoff := now.Add(-ttl)
	reaped := 0
	store.Range(func(key, value any) bool {
		w, ok := value.(*ringWindow)
		if !ok {
			return true
		}
		// 持锁完成「复检 + 删除」：若先解锁再删除，并发 Report 可能在空隙刷新
		// lastSeen，使刚恢复流量的窗口仍被回收而丢失证据（TOCTOU）。
		w.mu.Lock()
		if w.lastSeen.Before(cutoff) {
			store.Delete(key)
			reaped++
		}
		w.mu.Unlock()
		return true
	})
	return reaped
}
