package balancer

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/model"
)

// StrictRandom 无放回随机：按分组「抽牌」——连续请求不重复命中同一通道，
// 直到整副牌轮空再重新洗牌。比纯随机更均匀地分摊流量。
//
// 移植自 omniroute getNextFromDeck（无放回 deck 抽牌，key=combo:${name}）。
// octopus 的 Balancer.Candidates(items) 签名不携带 groupID，故 deck key 改用
// items 集合指纹（排序后的 ChannelID 串）：同一分组的 items 集合稳定时指纹稳定，
// 语义与 per-group deck 一致。deck 内存随分组数有限；分组更新时旧 deck 自然不再被命中。
type StrictRandom struct{}

type strictDeck struct {
	items []model.GroupItem
	pos   int // 下一个待发位置；到 len(items) 即轮空，下次重洗
}

var strictDecks sync.Map // key: fingerprint string -> *strictDeck

// strictDeckKey 基于 items 集合生成稳定指纹（与顺序无关）。
func strictDeckKey(items []model.GroupItem) string {
	ids := make([]int, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ChannelID)
	}
	sort.Ints(ids)
	var b strings.Builder
	b.Grow(len(ids) * 8)
	for _, id := range ids {
		b.WriteString(strconv.Itoa(id))
		b.WriteByte(':')
	}
	return b.String()
}

func (b *StrictRandom) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	if n == 1 {
		out := make([]model.GroupItem, 1)
		out[0] = items[0]
		return out
	}
	key := strictDeckKey(items)
	v, _ := strictDecks.LoadOrStore(key, &strictDeck{})
	deck := v.(*strictDeck)

	// 轮空或集合变化（长度不符/内容不符）→ 重新洗牌
	needReshuffle := deck.pos >= len(deck.items) ||
		len(deck.items) != n ||
		!sameItemSet(deck.items, items)
	if needReshuffle {
		deck.items = make([]model.GroupItem, n)
		copy(deck.items, items)
		rand.Shuffle(n, func(i, j int) {
			deck.items[i], deck.items[j] = deck.items[j], deck.items[i]
		})
		deck.pos = 0
	}

	// 从 deck.pos 起按顺序返回（头是本轮「抽出的牌」），pos 推进
	start := deck.pos
	deck.pos++
	result := make([]model.GroupItem, n)
	for i := 0; i < n; i++ {
		result[i] = deck.items[(start+i)%n]
	}
	return result
}

func sameItemSet(a, b []model.GroupItem) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make(map[int]struct{}, len(a))
	for _, it := range a {
		sa[it.ChannelID] = struct{}{}
	}
	for _, it := range b {
		if _, ok := sa[it.ChannelID]; !ok {
			return false
		}
	}
	return true
}
