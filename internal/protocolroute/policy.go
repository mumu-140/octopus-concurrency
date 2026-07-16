package protocolroute

import (
	"github.com/bestruirui/octopus/internal/protocol"
)

// PolicyMode 是四种策略模式（设计 §6.2）。
type PolicyMode string

const (
	ModeInherit PolicyMode = "inherit"
	ModeAuto    PolicyMode = "auto"
	ModePrefer  PolicyMode = "prefer"
	ModeForce   PolicyMode = "force"
)

// RoutingMode 是全局运行模式（设计 §10.1）。
type RoutingMode string

const (
	RoutingLegacy   RoutingMode = "legacy"
	RoutingObserve  RoutingMode = "observe"
	RoutingAdaptive RoutingMode = "adaptive"
)

// 默认非强制信号参考值（设计 §6.3）。
const (
	SignalIngress       = 800
	SignalGroupPrefer   = 700
	SignalEvidence      = 650 // 阶段 A 不读取，占位保持顺序稳定
	SignalKeyModelPref  = 625
	SignalChanModelPref = 600
	SignalChannelType   = 500
	SignalMetadataHint  = 100
)

// ScopedRule 是单一作用域的人工规则。
type ScopedRule struct {
	Mode      PolicyMode
	Protocols []protocol.Protocol // prefer: 有序列表；force: 只允许一个
}

// PolicySnapshot 是一次请求解析用的不可变策略快照（设计 §6.5）。
// 阶段 A 由调用方静态构造；阶段 B 起从 revision payload 反序列化。
type PolicySnapshot struct {
	ConfigRevision    int64
	Mode              RoutingMode
	ConversionEnabled bool

	// LearningReadEnabled=false 时 resolver 整体旁路证据/冷却/粘性（§6.4）。
	// 阶段 A 固定 false。
	LearningReadEnabled bool

	// Group 级策略（作用域最高）。
	Group ScopedRule

	// Key-模型 / 渠道-模型 覆盖：key 为 upstreamModel。
	// 阶段 A 允许为空；查找 miss 即 INHERIT。
	KeyModelRules  map[KeyModelScopeKey]ScopedRule
	ChanModelRules map[ChanModelScopeKey]ScopedRule

	// EnabledProfiles[channelID] = 该渠道已启用的协议集合。
	// 无该渠道条目时，隐式默认 Profile 规则生效：仅 Channel.Type 对应协议可用（§6.4/§10.5）。
	EnabledProfiles map[int]map[protocol.Protocol]bool
}

// KeyModelScopeKey 是 Key-模型覆盖的查找键。
type KeyModelScopeKey struct {
	ChannelKeyID  int
	UpstreamModel string
}

// ChanModelScopeKey 是渠道-模型覆盖的查找键。
type ChanModelScopeKey struct {
	ChannelID     int
	UpstreamModel string
}

// LegacySnapshot 返回阶段 A 默认快照：legacy 模式、无人工规则、无学习读取。
func LegacySnapshot() PolicySnapshot {
	return PolicySnapshot{
		Mode:  RoutingLegacy,
		Group: ScopedRule{Mode: ModeInherit},
	}
}

// resolvedRules 是三作用域规则合并结果（设计 §6.1/§6.2）。
type resolvedRules struct {
	force      *ScopedRule
	forceScope string // "group" / "key_model" / "channel_model"
	preferred  []scoredProtocol
}

type scoredProtocol struct {
	proto  protocol.Protocol
	score  int
	source string
}

// resolveManualRules 按固定作用域优先级合并人工规则：
// 分组 > 渠道 Key-模型 > 渠道-模型（§6.1）。
//
// 语义（§6.2）：
//   - Group FORCE 覆盖全部较低规则；
//   - Group AUTO 屏蔽 Key/Channel 模型人工覆盖；
//   - Group PREFER/INHERIT 时较低作用域 FORCE 仍可约束本候选；
//   - 同为 PREFER 时高作用域信号分更高。
func resolveManualRules(snap PolicySnapshot, channelID, channelKeyID int, upstreamModel string) resolvedRules {
	var out resolvedRules

	groupRule := snap.Group
	if groupRule.Mode == ModeForce && len(groupRule.Protocols) == 1 {
		out.force = &groupRule
		out.forceScope = "group"
		return out
	}
	if groupRule.Mode == ModePrefer {
		for _, p := range groupRule.Protocols {
			out.preferred = append(out.preferred, scoredProtocol{p, SignalGroupPrefer, "group_prefer"})
		}
	}

	lowerBlocked := groupRule.Mode == ModeAuto

	if !lowerBlocked {
		if r, ok := snap.KeyModelRules[KeyModelScopeKey{channelKeyID, upstreamModel}]; ok {
			switch r.Mode {
			case ModeForce:
				if len(r.Protocols) == 1 {
					rr := r
					out.force = &rr
					out.forceScope = "key_model"
					return out
				}
			case ModePrefer:
				for _, p := range r.Protocols {
					out.preferred = append(out.preferred, scoredProtocol{p, SignalKeyModelPref, "key_model_prefer"})
				}
			}
		}
		if r, ok := snap.ChanModelRules[ChanModelScopeKey{channelID, upstreamModel}]; ok {
			switch r.Mode {
			case ModeForce:
				if len(r.Protocols) == 1 {
					rr := r
					out.force = &rr
					out.forceScope = "channel_model"
					return out
				}
			case ModePrefer:
				for _, p := range r.Protocols {
					out.preferred = append(out.preferred, scoredProtocol{p, SignalChanModelPref, "channel_model_prefer"})
				}
			}
		}
	}

	return out
}

// profileEnabled 报告协议在候选渠道上是否有已启用 Profile。
// EnabledProfiles 无该渠道条目时应用隐式默认 Profile：仅 channelType 本身可用。
func profileEnabled(snap PolicySnapshot, channelID int, channelType, p protocol.Protocol) bool {
	if profiles, ok := snap.EnabledProfiles[channelID]; ok {
		if enabled, configured := profiles[p]; configured {
			return enabled
		}
	}
	return p == channelType
}
