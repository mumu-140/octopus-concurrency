package balancer

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/outlierwindow"
)

// rotationCounters 轮换计数器，按「用途 + 候选集合指纹」分桶。
// 原实现是单个全局计数器，被 RoundRobin 与 HealthFirst 同档轮换共用：
// 不同分组、不同模型、不同策略互相推进对方的游标，轮询退化为伪随机。
var rotationCounters sync.Map // key: string -> *uint64

// nextRotation 返回指定桶自增后的计数值。
func nextRotation(bucket string) uint64 {
	if v, ok := rotationCounters.Load(bucket); ok {
		return atomic.AddUint64(v.(*uint64), 1)
	}
	var c uint64
	actual, _ := rotationCounters.LoadOrStore(bucket, &c)
	return atomic.AddUint64(actual.(*uint64), 1)
}

// Balancer 根据负载均衡模式选择通道
type Balancer interface {
	// Candidates 返回按策略排序的候选列表
	// 调用方在遍历候选列表时自行检查熔断状态
	Candidates(items []model.GroupItem) []model.GroupItem
}

// GetBalancer 根据模式返回对应的负载均衡器
func GetBalancer(mode model.GroupMode) Balancer {
	switch mode {
	case model.GroupModeRoundRobin:
		return &RoundRobin{}
	case model.GroupModeRandom:
		return &Random{}
	case model.GroupModeFailover:
		return &Failover{}
	case model.GroupModeWeighted:
		return &Weighted{}
	case model.GroupModeHealthFirst:
		return &HealthFirst{}
	case model.GroupModeLeastUsed:
		return &LeastUsed{}
	case model.GroupModeP2C:
		return &P2C{}
	case model.GroupModeStrictRandom:
		return &StrictRandom{}
	default:
		return &RoundRobin{}
	}
}

// RoundRobin 轮询：从上次位置开始轮转排列。
// 计数器按候选集合指纹分桶，保证同一分组内严格轮转，且不被其他分组串扰。
type RoundRobin struct{}

func (b *RoundRobin) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	idx := int(nextRotation("rr:"+itemSetKey(items)) % uint64(n))
	result := make([]model.GroupItem, n)
	for i := 0; i < n; i++ {
		result[i] = items[(idx+i)%n]
	}
	return result
}

// Random 随机：随机打乱所有 items
type Random struct{}

func (b *Random) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	result := make([]model.GroupItem, n)
	copy(result, items)
	rand.Shuffle(n, func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}

// Failover 故障转移：Priority 升序 → 未熔断优先 → 健康分降序。
// 原实现只按 Priority 排序，最高优先级的渠道-模型即使连续失败也永远排第一，
// 每次请求都要先撞一次坏上游才会降级；加入熔断与健康度作为同优先级内的次级键后，
// 同一 Priority 内坏项自动后移，Priority 的语义（优先级高的先用）不变。
type Failover struct{}

func (b *Failover) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	now := time.Now()
	type foEntry struct {
		item    model.GroupItem
		score   float64
		tripped bool
	}
	es := make([]foEntry, n)
	for i, item := range items {
		es[i] = foEntry{
			item:    item,
			score:   itemHealthScore(item.ChannelID, item.ModelName, now),
			tripped: PeekItemTripped(item.ChannelID, item.ModelName),
		}
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].item.Priority != es[j].item.Priority {
			return es[i].item.Priority < es[j].item.Priority
		}
		if es[i].tripped != es[j].tripped {
			return !es[i].tripped
		}
		return es[i].score > es[j].score
	})
	result := make([]model.GroupItem, n)
	for i := range es {
		result[i] = es[i].item
	}
	return result
}

// Weighted 加权分配：按权重生成无放回抽样顺序。
//
// 原实现用 score = rand.Float64() * w/totalWeight 降序排列，首位命中概率并不等于
// w/totalWeight：两个权重 10 与 1 的候选，理论首位比应为 10:1（0.909），
// 旧公式实测约 0.95，权重被系统性放大。
// 现改用指数赛跑（等价于 A-Res 加权无放回抽样）：key = -ln(U)/w，升序取最小。
// 该式下 P(item i 排首位) = w_i / Σw 严格成立，且整个排列即一次加权无放回抽样。
type Weighted struct{}

func (b *Weighted) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}

	type weightedItem struct {
		item model.GroupItem
		key  float64
	}

	scored := make([]weightedItem, n)
	for i, item := range items {
		w := item.Weight
		if w <= 0 {
			w = 1
		}
		// 1-rand.Float64() 落在 (0,1]，避免 rand.Float64() 取到 0 时 -ln(0)=+Inf
		scored[i] = weightedItem{
			item: item,
			key:  -math.Log(1-rand.Float64()) / float64(w),
		}
	}

	// key 越小越优先（权重越大，期望 key 越小）
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].key < scored[j].key
	})

	result := make([]model.GroupItem, n)
	for i := range scored {
		result[i] = scored[i].item
	}
	return result
}

// LeastUsed 最少使用：按在途并发升序，优先空闲通道；并发相同按 Priority 升序。
// 移植自 omniroute sortTargetsByUsage（octopus 用实时在途并发替代历史请求数，意图等价、数据更贴当前负载）。
type LeastUsed struct{}

func (b *LeastUsed) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	type luEntry struct {
		item model.GroupItem
		conc int64
	}
	es := make([]luEntry, n)
	for i, item := range items {
		es[i] = luEntry{item: item, conc: CurrentChannelConcurrency(item.ChannelID)}
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].conc != es[j].conc {
			return es[i].conc < es[j].conc
		}
		return es[i].item.Priority < es[j].item.Priority
	})
	result := make([]model.GroupItem, n)
	for i := range es {
		result[i] = es[i].item
	}
	return result
}

// P2C 二选一（Power of Two Choices）：随机取两个候选，选在途并发较低者置首；
// 余下按并发升序作 fallback 尾部。n<2 退化为按并发升序。
// 移植自 omniroute orderTargetsByPowerOfTwoChoices。
type P2C struct{}

func (b *P2C) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	if n == 1 {
		out := make([]model.GroupItem, 1)
		out[0] = items[0]
		return out
	}
	i := rand.Intn(n)
	j := rand.Intn(n)
	for j == i {
		j = rand.Intn(n)
	}
	// 二选一：选并发较低者作为 primary；primaryIdx 跟踪胜者索引以便从 rest 中排除
	primaryIdx := i
	if CurrentChannelConcurrency(items[j].ChannelID) < CurrentChannelConcurrency(items[i].ChannelID) {
		primaryIdx = j
	}
	primary := items[primaryIdx]

	// 余下（不含 primary）按并发升序、Priority tie-break
	rest := make([]model.GroupItem, 0, n-1)
	for k, it := range items {
		if k == primaryIdx {
			continue
		}
		rest = append(rest, it)
	}
	sort.SliceStable(rest, func(a, bb int) bool {
		ca, cb := CurrentChannelConcurrency(rest[a].ChannelID), CurrentChannelConcurrency(rest[bb].ChannelID)
		if ca != cb {
			return ca < cb
		}
		return rest[a].Priority < rest[bb].Priority
	})

	result := make([]model.GroupItem, 0, n)
	result = append(result, primary)
	result = append(result, rest...)
	return result
}

// itemHealthScore 渠道-模型健康分（0 最差 ~ 1 最好），基于 outlierwindow 滚动成败窗口。
// 粒度必须是渠道-模型：一个渠道通常挂很多模型，单个模型不可用（model_not_found、
// 上下文超限、限流）不能把整渠道判成不健康。
// 移植自 omniroute scoring.ts 的 health/cost/quota 因子在「无 p95/cost/quota 数据」时的 A 类退化：
// 仅用进程内已有的成败窗口。冷启动（样本不足）给中性分 0.5，避免新通道被冤枉。
func itemHealthScore(channelID int, modelName string, now time.Time) float64 {
	st := outlierwindow.Evaluate(channelID, modelName, now)
	if st.Samples == 0 {
		return 0.5 // 冷启动：无证据，中性
	}
	// health = 1 - 失败率
	score := 1.0 - st.FailureRate
	// 连续失败惩罚：连续失败越多越低（每条线性扣 0.1，至多扣 0.5）
	penalty := 0.0
	if st.ConsecutiveFails > 0 {
		penalty = 0.1 * float64(st.ConsecutiveFails)
		if penalty > 0.5 {
			penalty = 0.5
		}
	}
	score -= penalty
	if score < 0 {
		score = 0
	}
	// 样本不足（未达 MinSamples）仍给一点保守回拉：证据不足不全信
	if st.Samples < minHealthSamples {
		// 向中性 0.5 拉拢一半
		score = (score + 0.5) / 2
	}
	return score
}

const minHealthSamples = 8 // 与 outlierwindow defaultConfig.MinSamples 对齐

// Reset clears in-memory balancer state for tests.
func Reset() {
	rotationCounters = sync.Map{}
	globalBreaker = sync.Map{}
	globalSession = sync.Map{}
	globalConcurrency = sync.Map{}
	globalChannelRate = sync.Map{}
}
