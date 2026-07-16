package protocolroute

import (
	"encoding/json"
	"sort"

	"github.com/bestruirui/octopus/internal/protocol"
)

// AttemptConfig is the immutable endpoint/header/body configuration for one protocol.
type AttemptConfig struct {
	BaseURL       string
	HeaderPolicy  HeaderPolicy
	ParamOverride json.RawMessage
}

// Decision 是 resolvePrimary 的输出。
type Decision struct {
	// Incompatible=true 表示该 Key 无安全候选（KeyProtocolIncompatible，§6.4）：
	// 调用方应把 Key 加入请求级排除集并重选同渠道下一个 Key。
	Incompatible bool
	// IncompatibleScope/Reason 记录不兼容来源（force 作用域或 legacy 原因）。
	IncompatibleScope  string
	IncompatibleReason string

	// Plan 是选出的不可变尝试计划（Incompatible=false 时非 nil）。
	Plan *AttemptPlan
}

// ResolveInput 是 resolvePrimary 的输入。全部为值或只读引用；
// resolver 不修改任何输入（无运行副作用）。
type ResolveInput struct {
	Snapshot PolicySnapshot

	ChannelID    int
	ChannelKeyID int
	ChannelType  protocol.Protocol // 由 Channel.Type 显式映射而来

	RequestedModel string
	UpstreamModel  string

	Ingress  protocol.Protocol
	Features RequestFeatureFlags

	BaseURL       string
	HeaderPolicy  HeaderPolicy
	ParamOverride json.RawMessage

	// AdaptiveProfiles contains protocol-specific attempt configuration. The
	// selected protocol is the only entry copied into the resulting plan.
	AdaptiveProfiles map[protocol.Protocol]AttemptConfig

	// LegacyEligible 是调用方注入的现有 relay 兼容性判断结果（§6.4）：
	// 请求 Handler/渠道类型匹配、passthroughRequired、Embedding 路径等。
	// LegacyFixedAttempt 必须先通过该判断。
	LegacyEligible bool

	// PreviousAttemptProtocol 仅作为最终平局提示（§6.4），
	// 不过滤候选、不覆盖入口/人工策略/Channel.Type。
	PreviousAttemptProtocol protocol.Protocol
}

// ResolvePrimary 实现设计 §6.4 的确定性算法（阶段 A：证据读取恒关闭）。
//
// 阶段 A 简化点（相对完整算法）：
//   - snapshot.LearningReadEnabled 恒为 false：不注入历史候选、不读证据分、
//     不应用协议冷却与转换可靠性过滤；
//   - metadataHints 未实现（无该信号源），对应 §6.3 顺序 7 空缺。
func ResolvePrimary(in ResolveInput) Decision {
	snap := in.Snapshot
	rules := resolveManualRules(snap, in.ChannelID, in.ChannelKeyID, in.UpstreamModel)

	legacyFixedRequired := in.Features.RequiresLegacyOnly() || !in.ChannelType.IsAdaptive()
	if legacyFixedRequired {
		// force 与 legacy 底座冲突：显式 FORCE 是唯一可有意排除 legacy 的方式（§5.1）
		if rules.force != nil && rules.force.Protocols[0] != in.ChannelType {
			return Decision{
				Incompatible:       true,
				IncompatibleScope:  rules.forceScope,
				IncompatibleReason: "force protocol conflicts with legacy-fixed candidate",
			}
		}
		if !in.LegacyEligible {
			return Decision{
				Incompatible:       true,
				IncompatibleReason: "legacy path not eligible for this request",
			}
		}
		return Decision{Plan: newLegacyPlanFromInput(in)}
	}

	if rules.force != nil {
		fp := rules.force.Protocols[0]
		if fp != in.Ingress && !snap.ConversionEnabled {
			return Decision{
				Incompatible:       true,
				IncompatibleScope:  rules.forceScope,
				IncompatibleReason: "protocol conversion is disabled",
			}
		}
		if _, ok := fp.ToOutboundType(); !ok {
			return Decision{
				Incompatible:       true,
				IncompatibleScope:  rules.forceScope,
				IncompatibleReason: "forced protocol has no registered adapter",
			}
		}
		if !profileEnabled(snap, in.ChannelID, in.ChannelType, fp) {
			return Decision{
				Incompatible:       true,
				IncompatibleScope:  rules.forceScope,
				IncompatibleReason: "forced protocol has no enabled profile on channel",
			}
		}
		verdict := EvaluateConversion(in.Ingress, fp, in.Features)
		if verdict == VerdictForbidden || verdict == VerdictLegacyFixed {
			return Decision{
				Incompatible:       true,
				IncompatibleScope:  rules.forceScope,
				IncompatibleReason: "forced protocol blocked by conversion gate",
			}
		}
		return Decision{Plan: newAdaptivePlanFromInput(in, fp, verdict, "force_"+rules.forceScope, SignalIngress+100)}
	}

	// 候选集合 = 入口 ∪ prefer 规则 ∪ Channel.Type ∪ 已启用 Profile（§6.4 union）
	type cand struct {
		proto  protocol.Protocol
		score  int
		source string
	}
	seen := map[protocol.Protocol]*cand{}
	add := func(p protocol.Protocol, score int, source string) {
		if !p.IsAdaptive() {
			return
		}
		if c, ok := seen[p]; ok {
			if score > c.score {
				c.score, c.source = score, source
			}
			return
		}
		seen[p] = &cand{p, score, source}
	}

	add(in.Ingress, SignalIngress, "ingress")
	for _, sp := range rules.preferred {
		add(sp.proto, sp.score, sp.source)
	}
	add(in.ChannelType, SignalChannelType, "channel_type")
	if profiles, ok := snap.EnabledProfiles[in.ChannelID]; ok {
		for p := range profiles {
			if profiles[p] {
				add(p, SignalChannelType-1, "enabled_profile")
			}
		}
	}

	// 过滤：注册 adapter + 启用 Profile + 转换门禁（§6.4 filter 链）
	verdicts := map[protocol.Protocol]ConversionVerdict{}
	var kept []*cand
	for p, c := range seen {
		if _, ok := p.ToOutboundType(); !ok {
			continue
		}
		if !profileEnabled(snap, in.ChannelID, in.ChannelType, p) {
			continue
		}
		if p != in.Ingress && !snap.ConversionEnabled {
			continue
		}
		v := EvaluateConversion(in.Ingress, p, in.Features)
		if v == VerdictForbidden || v == VerdictLegacyFixed {
			continue
		}
		verdicts[p] = v
		kept = append(kept, c)
	}

	if len(kept) == 0 {
		if in.LegacyEligible {
			return Decision{Plan: newLegacyPlanFromInput(in)}
		}
		return Decision{
			Incompatible:       true,
			IncompatibleReason: "no adaptive candidate and legacy path not eligible",
		}
	}

	// stableSort（§6.4）：score DESC →（阶段 A 无 evidence/lastAccepted）
	// previousAttemptProtocolMatch DESC → protocol 字典序 ASC
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].score != kept[j].score {
			return kept[i].score > kept[j].score
		}
		im := kept[i].proto == in.PreviousAttemptProtocol
		jm := kept[j].proto == in.PreviousAttemptProtocol
		if im != jm {
			return im
		}
		return kept[i].proto < kept[j].proto
	})

	best := kept[0]
	return Decision{Plan: newAdaptivePlanFromInput(in, best.proto, verdicts[best.proto], best.source, best.score)}
}

func newLegacyPlanFromInput(in ResolveInput) *AttemptPlan {
	return NewLegacyFixedAttempt(PlanSpec{
		ChannelID:        in.ChannelID,
		ChannelKeyID:     in.ChannelKeyID,
		RequestedModel:   in.RequestedModel,
		UpstreamModel:    in.UpstreamModel,
		IngressProtocol:  in.Ingress,
		UpstreamProtocol: in.ChannelType,
		Features:         in.Features,
		BaseURL:          in.BaseURL,
		HeaderPolicy:     in.HeaderPolicy,
		ParamOverride:    in.ParamOverride,
		PolicySource:     "legacy_fixed",
		ConfigRevision:   in.Snapshot.ConfigRevision,
	})
}

func newAdaptivePlanFromInput(in ResolveInput, up protocol.Protocol, verdict ConversionVerdict, source string, score int) *AttemptPlan {
	config := in.AdaptiveProfiles[up]
	return NewAttemptPlan(PlanSpec{
		ChannelID:        in.ChannelID,
		ChannelKeyID:     in.ChannelKeyID,
		RequestedModel:   in.RequestedModel,
		UpstreamModel:    in.UpstreamModel,
		IngressProtocol:  in.Ingress,
		UpstreamProtocol: up,
		ConversionMode:   ModeFor(in.Ingress, up, verdict),
		Features:         in.Features,
		BaseURL:          config.BaseURL,
		HeaderPolicy:     config.HeaderPolicy,
		ParamOverride:    config.ParamOverride,
		PolicySource:     source,
		PolicyPriority:   score,
		ConfigRevision:   in.Snapshot.ConfigRevision,
		AttemptKind:      KindCandidatePrimary,
	})
}
