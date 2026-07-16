package protocolroute

import (
	"sync/atomic"

	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// observeEnabled 是进程级 observe 开关。阶段 A 默认关闭（legacy 零行为变化）。
// 阶段 B 起由 revision payload 投影热更新。
var observeEnabled atomic.Bool

// SetObserveEnabled 设置 observe 影子决策开关（幂等，热切换安全）。
func SetObserveEnabled(v bool) { observeEnabled.Store(v) }

// ObserveEnabled 报告 observe 是否开启。
func ObserveEnabled() bool { return observeEnabled.Load() }

// ObserveShadowDecision 在 observe 模式下记录一次影子协议决策。
//
// 约束（设计 §5 / tasks T08）：
//   - 只记录，不改变请求：调用方不得使用返回值影响任何控制流；
//   - panic 安全：影子解析的任何 panic 都被吞掉并降级为一条 warn 日志，
//     不能影响 legacy 请求路径；
//   - observe 关闭时零成本直接返回。
func ObserveShadowDecision(in ResolveInput) {
	if !observeEnabled.Load() {
		return
	}
	ObserveShadowDecisionNow(in)
}

// ObserveShadowDecisionNow records one request-scoped observe decision.
// The caller has already resolved the effective per-group mode.
func ObserveShadowDecisionNow(in ResolveInput) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("protocolroute observe: shadow resolve panic suppressed: %v", r)
		}
	}()

	d := ResolvePrimary(in)
	if d.Incompatible {
		log.Infof("protocolroute observe: channel=%d key=%d model=%s ingress=%s -> incompatible scope=%s reason=%s",
			in.ChannelID, in.ChannelKeyID, in.UpstreamModel, in.Ingress, d.IncompatibleScope, d.IncompatibleReason)
		return
	}
	plan := d.Plan
	log.Infof("protocolroute observe: channel=%d key=%d model=%s ingress=%s -> upstream=%s mode=%s legacy_fixed=%t source=%s legacy_actual=%s",
		in.ChannelID, in.ChannelKeyID, in.UpstreamModel, in.Ingress,
		plan.UpstreamProtocol(), plan.ConversionMode(), plan.IsLegacyFixed(),
		plan.PolicySource(), in.ChannelType)
	if !plan.IsLegacyFixed() && plan.UpstreamProtocol() != in.ChannelType {
		// 影子决策与 legacy 实际协议不同：这是 observe 模式最有价值的差异信号
		log.Infof("protocolroute observe: DIVERGENCE channel=%d key=%d model=%s shadow=%s legacy=%s",
			in.ChannelID, in.ChannelKeyID, in.UpstreamModel, plan.UpstreamProtocol(), in.ChannelType)
	}
}

// ShadowInputFromLegacy 从 legacy relay 已有变量构造影子解析输入。
// 全部按值复制，不持有 relay 可变对象引用。
func ShadowInputFromLegacy(
	channelID, channelKeyID int,
	channelType protocol.Protocol,
	requestedModel, upstreamModel string,
	ingress protocol.Protocol,
	features RequestFeatureFlags,
	legacyEligible bool,
) ResolveInput {
	return ResolveInput{
		Snapshot:       LegacySnapshot(),
		ChannelID:      channelID,
		ChannelKeyID:   channelKeyID,
		ChannelType:    channelType,
		RequestedModel: requestedModel,
		UpstreamModel:  upstreamModel,
		Ingress:        ingress,
		Features:       features,
		LegacyEligible: legacyEligible,
	}
}
