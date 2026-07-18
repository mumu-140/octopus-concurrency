package protocolroute

import (
	"encoding/json"

	"github.com/bestruirui/octopus/internal/protocol"
)

// AttemptKind 区分三类上游尝试（设计 §7.1）。
type AttemptKind string

const (
	KindCandidatePrimary              AttemptKind = "candidate_primary"
	KindSameCandidateRetry            AttemptKind = "same_candidate_retry"
	KindSameCandidateProtocolFallback AttemptKind = "same_candidate_protocol_fallback"
)

// HeaderPolicy 是 Attempt 的 Header 改写快照。
// 首版直接快照渠道 CustomHeader 的键值对，保证 Attempt 之间互不影响。
type HeaderPolicy struct {
	Set map[string]string
}

// clone 返回 HeaderPolicy 的独立副本。
func (h HeaderPolicy) clone() HeaderPolicy {
	if h.Set == nil {
		return HeaderPolicy{}
	}
	out := make(map[string]string, len(h.Set))
	for k, v := range h.Set {
		out[k] = v
	}
	return HeaderPolicy{Set: out}
}

// AttemptPlan 是一次上游尝试的不可变快照（设计 §5.2）。
//
// 约束：
//   - 创建后不可修改：全部字段经构造函数复制，无导出可变引用；
//   - 禁止临时改写共享 Channel.Type；
//   - LegacyFixedAttempt 不要求协议 Profile，不参与协议回退。
type AttemptPlan struct {
	channelID         int
	channelKeyID      int
	requestedModel    string
	upstreamModel     string
	ingressProtocol   protocol.Protocol
	upstreamProtocol  protocol.Protocol
	conversionMode    ConversionMode
	features          RequestFeatureFlags
	baseURL           string
	headerPolicy      HeaderPolicy
	paramOverride     json.RawMessage
	policySource      string
	policyPriority    int
	configRevision    int64
	attemptKind       AttemptKind
	fallbackAuthID    string
	groupProtocolMode GroupProtocolMode
	fallbackReason    string
	legacyFixed       bool
}

// PlanSpec 是构造 AttemptPlan 的输入。构造完成后修改 Spec 不影响已建 Plan。
type PlanSpec struct {
	ChannelID         int
	ChannelKeyID      int
	RequestedModel    string
	UpstreamModel     string
	IngressProtocol   protocol.Protocol
	UpstreamProtocol  protocol.Protocol
	ConversionMode    ConversionMode
	Features          RequestFeatureFlags
	BaseURL           string
	HeaderPolicy      HeaderPolicy
	ParamOverride     json.RawMessage
	PolicySource      string
	PolicyPriority    int
	ConfigRevision    int64
	AttemptKind       AttemptKind
	FallbackAuthID    string
	GroupProtocolMode GroupProtocolMode
	FallbackReason    string
}

// NewAttemptPlan 构造 Adaptive AttemptPlan；输入字段全部复制。
func NewAttemptPlan(spec PlanSpec) *AttemptPlan {
	return &AttemptPlan{
		channelID:         spec.ChannelID,
		channelKeyID:      spec.ChannelKeyID,
		requestedModel:    spec.RequestedModel,
		upstreamModel:     spec.UpstreamModel,
		ingressProtocol:   spec.IngressProtocol,
		upstreamProtocol:  spec.UpstreamProtocol,
		conversionMode:    spec.ConversionMode,
		features:          spec.Features,
		baseURL:           spec.BaseURL,
		headerPolicy:      spec.HeaderPolicy.clone(),
		paramOverride:     append(json.RawMessage(nil), spec.ParamOverride...),
		policySource:      spec.PolicySource,
		policyPriority:    spec.PolicyPriority,
		configRevision:    spec.ConfigRevision,
		attemptKind:       spec.AttemptKind,
		fallbackAuthID:    spec.FallbackAuthID,
		groupProtocolMode: spec.GroupProtocolMode,
		fallbackReason:    spec.FallbackReason,
	}
}

// NewLegacyFixedAttempt 构造 LegacyFixedAttempt（设计 §5.1/§5.3）：
// 使用候选现有 Channel.Type 协议、Base URL 与 Header，
// conversionMode 固定 legacy，attemptKind 固定 candidate_primary，
// 不携带 FallbackAuthID（不参与协议回退）。
func NewLegacyFixedAttempt(spec PlanSpec) *AttemptPlan {
	p := NewAttemptPlan(spec)
	p.conversionMode = ModeLegacy
	p.attemptKind = KindCandidatePrimary
	p.fallbackAuthID = ""
	p.legacyFixed = true
	return p
}

// 只读访问器 —— AttemptPlan 无任何 setter。

func (p *AttemptPlan) ChannelID() int                       { return p.channelID }
func (p *AttemptPlan) ChannelKeyID() int                    { return p.channelKeyID }
func (p *AttemptPlan) RequestedModel() string               { return p.requestedModel }
func (p *AttemptPlan) UpstreamModel() string                { return p.upstreamModel }
func (p *AttemptPlan) IngressProtocol() protocol.Protocol   { return p.ingressProtocol }
func (p *AttemptPlan) UpstreamProtocol() protocol.Protocol  { return p.upstreamProtocol }
func (p *AttemptPlan) ConversionMode() ConversionMode       { return p.conversionMode }
func (p *AttemptPlan) Features() RequestFeatureFlags        { return p.features }
func (p *AttemptPlan) BaseURL() string                      { return p.baseURL }
func (p *AttemptPlan) PolicySource() string                 { return p.policySource }
func (p *AttemptPlan) PolicyPriority() int                  { return p.policyPriority }
func (p *AttemptPlan) ConfigRevision() int64                { return p.configRevision }
func (p *AttemptPlan) AttemptKind() AttemptKind             { return p.attemptKind }
func (p *AttemptPlan) FallbackAuthID() string               { return p.fallbackAuthID }
func (p *AttemptPlan) GroupProtocolMode() GroupProtocolMode { return p.groupProtocolMode }
func (p *AttemptPlan) FallbackReason() string               { return p.fallbackReason }
func (p *AttemptPlan) IsLegacyFixed() bool                  { return p.legacyFixed }

// AsProtocolFallback returns an independent plan annotated as the single
// same-candidate protocol fallback.
func (p *AttemptPlan) AsProtocolFallback(reason string) *AttemptPlan {
	if p == nil {
		return nil
	}
	clone := *p
	clone.headerPolicy = p.headerPolicy.clone()
	clone.paramOverride = append(json.RawMessage(nil), p.paramOverride...)
	clone.attemptKind = KindSameCandidateProtocolFallback
	clone.fallbackReason = reason
	return &clone
}

// HeaderPolicy 返回 Header 快照副本，调用方修改不影响 Plan。
func (p *AttemptPlan) HeaderPolicy() HeaderPolicy { return p.headerPolicy.clone() }

// ParamOverride 返回参数覆盖字节副本。
func (p *AttemptPlan) ParamOverride() json.RawMessage {
	return append(json.RawMessage(nil), p.paramOverride...)
}
