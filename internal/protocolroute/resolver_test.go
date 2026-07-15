package protocolroute

import (
	"testing"

	"github.com/bestruirui/octopus/internal/protocol"
)

func adaptiveInput() ResolveInput {
	return ResolveInput{
		Snapshot:       LegacySnapshot(),
		ChannelID:      1,
		ChannelKeyID:   10,
		ChannelType:    protocol.OpenAIChat,
		RequestedModel: "alias",
		UpstreamModel:  "m",
		Ingress:        protocol.OpenAIChat,
		Features:       RequestFeatureFlags{PlainText: true},
		LegacyEligible: true,
	}
}

// ---- 基础路径 ----

func TestResolveDefaultFollowsChannelTypeAndIngress(t *testing.T) {
	in := adaptiveInput()
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	// ingress(chat,800) 与 channel_type(chat,500) 同协议 → chat
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIChat {
		t.Fatalf("upstream = %q, want openai_chat", got)
	}
	if d.Plan.IsLegacyFixed() {
		t.Fatal("adaptive candidate must not be legacy fixed")
	}
	if d.Plan.AttemptKind() != KindCandidatePrimary {
		t.Fatalf("kind = %q", d.Plan.AttemptKind())
	}
}

func TestResolveIngressBeatsChannelTypeWhenProfileEnabled(t *testing.T) {
	in := adaptiveInput()
	in.Ingress = protocol.OpenAIResponse // Codex 入口
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true},
	}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIResponse {
		t.Fatalf("upstream = %q, want openai_response (ingress signal 800)", got)
	}
	if got := d.Plan.ConversionMode(); got != ModeRawPassthrough {
		t.Fatalf("mode = %q, want raw_passthrough", got)
	}
}

func TestResolveImplicitDefaultProfileRestrictsToChannelType(t *testing.T) {
	in := adaptiveInput()
	in.Ingress = protocol.OpenAIResponse
	// EnabledProfiles 无该渠道条目 → 隐式默认 Profile：仅 Channel.Type(chat) 可用
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIChat {
		t.Fatalf("upstream = %q, want openai_chat (implicit default profile)", got)
	}
	if got := d.Plan.ConversionMode(); got != ModeTranslated {
		t.Fatalf("mode = %q, want translated", got)
	}
}

// ---- FORCE 语义 ----

func TestGroupForceOverridesEverything(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: []protocol.Protocol{protocol.Anthropic}}
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.Anthropic: true},
	}
	// 低作用域 force 应被 Group FORCE 覆盖（不进入低作用域分支）
	in.Snapshot.KeyModelRules = map[KeyModelScopeKey]ScopedRule{
		{10, "m"}: {Mode: ModeForce, Protocols: []protocol.Protocol{protocol.OpenAIResponse}},
	}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.Anthropic {
		t.Fatalf("upstream = %q, want anthropic (group force)", got)
	}
}

func TestGroupForceWithoutProfileIsIncompatible(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: []protocol.Protocol{protocol.Anthropic}}
	// 隐式默认 Profile 只有 chat → force anthropic 无 Profile → KeyProtocolIncompatible
	d := ResolvePrimary(in)
	if !d.Incompatible {
		t.Fatalf("want incompatible, got plan %+v", d.Plan)
	}
	if d.IncompatibleScope != "group" {
		t.Fatalf("scope = %q, want group", d.IncompatibleScope)
	}
}

func TestKeyModelForceConstrainsCandidate(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true},
	}
	in.Snapshot.KeyModelRules = map[KeyModelScopeKey]ScopedRule{
		{10, "m"}: {Mode: ModeForce, Protocols: []protocol.Protocol{protocol.OpenAIResponse}},
	}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIResponse {
		t.Fatalf("upstream = %q, want openai_response (key-model force)", got)
	}
	if got := d.Plan.PolicySource(); got != "force_key_model" {
		t.Fatalf("source = %q", got)
	}
}

func TestGroupAutoBlocksLowerManualRules(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.Group = ScopedRule{Mode: ModeAuto}
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true},
	}
	in.Snapshot.KeyModelRules = map[KeyModelScopeKey]ScopedRule{
		{10, "m"}: {Mode: ModeForce, Protocols: []protocol.Protocol{protocol.OpenAIResponse}},
	}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	// AUTO 屏蔽低作用域 force → 回到默认排序（ingress=chat 胜）
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIChat {
		t.Fatalf("upstream = %q, want openai_chat (auto blocks lower force)", got)
	}
}

func TestForceIgnoresConversionGateViolation(t *testing.T) {
	in := adaptiveInput()
	in.Features = RequestFeatureFlags{FunctionTools: true}
	in.Ingress = protocol.OpenAIResponse
	in.ChannelType = protocol.Anthropic
	// tools Responses→Anthropic 是 forbidden；FORCE 不能绕过转换门禁（§6.2）
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: []protocol.Protocol{protocol.Anthropic}}
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.Anthropic: true},
	}
	d := ResolvePrimary(in)
	if !d.Incompatible {
		t.Fatalf("want incompatible (force blocked by conversion gate), got %+v", d.Plan)
	}
}

// ---- PREFER 信号 ----

func TestGroupPreferRaisesCandidate(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.Group = ScopedRule{Mode: ModePrefer, Protocols: []protocol.Protocol{protocol.OpenAIResponse}}
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true},
	}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	// ingress chat(800) 仍高于 group prefer responses(700)
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIChat {
		t.Fatalf("upstream = %q, want openai_chat (ingress 800 > prefer 700)", got)
	}
}

func TestPreviousProtocolIsOnlyTieBreaker(t *testing.T) {
	in := adaptiveInput()
	// 构造平局：两个协议同分。ingress=chat 与 channel_type=chat 都是 chat；
	// 用两个 prefer 同分制造平局。
	in.Ingress = protocol.OpenAIChat
	in.ChannelType = protocol.OpenAIResponse
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true, protocol.OpenAIResponse: true, protocol.Anthropic: true},
	}
	in.Snapshot.Group = ScopedRule{Mode: ModePrefer, Protocols: []protocol.Protocol{protocol.OpenAIResponse, protocol.Anthropic}}
	// prefer 内两协议同为 700；previous=anthropic 只能在同分里抬 anthropic，
	// 但 ingress chat 800 仍最高 → chat 胜；previous 不得过滤或覆盖。
	in.PreviousAttemptProtocol = protocol.Anthropic
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.OpenAIChat {
		t.Fatalf("upstream = %q, want openai_chat (previous is tie-break only)", got)
	}

	// 移除 ingress 优势后（ingress 也是 responses），responses(800) vs anthropic(700)：
	// previous=anthropic 仍不能翻越更高分。
	in2 := in
	in2.Ingress = protocol.OpenAIResponse
	d2 := ResolvePrimary(in2)
	if got := d2.Plan.UpstreamProtocol(); got != protocol.OpenAIResponse {
		t.Fatalf("upstream = %q, want openai_response (score beats previous hint)", got)
	}
}

// ---- legacy 固定底座 ----

func TestNonAdaptiveChannelTypeGetsLegacyFixed(t *testing.T) {
	in := adaptiveInput()
	in.ChannelType = protocol.Gemini
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if !d.Plan.IsLegacyFixed() {
		t.Fatal("gemini candidate must be legacy fixed")
	}
	if got := d.Plan.UpstreamProtocol(); got != protocol.Gemini {
		t.Fatalf("legacy plan must keep channel type, got %q", got)
	}
}

func TestMultimodalRequestGetsLegacyFixedOnAdaptiveChannel(t *testing.T) {
	in := adaptiveInput()
	in.Features = RequestFeatureFlags{Multimodal: true}
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if !d.Plan.IsLegacyFixed() {
		t.Fatal("multimodal request must be legacy fixed")
	}
}

func TestLegacyFixedRequiresLegacyEligible(t *testing.T) {
	in := adaptiveInput()
	in.ChannelType = protocol.Gemini
	in.LegacyEligible = false
	d := ResolvePrimary(in)
	if !d.Incompatible {
		t.Fatal("want incompatible when legacy not eligible")
	}
}

func TestForceConflictOnLegacyFixedCandidate(t *testing.T) {
	in := adaptiveInput()
	in.ChannelType = protocol.Gemini
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: []protocol.Protocol{protocol.OpenAIChat}}
	d := ResolvePrimary(in)
	if !d.Incompatible {
		t.Fatal("want incompatible: force chat conflicts with gemini legacy candidate")
	}
	if d.IncompatibleScope != "group" {
		t.Fatalf("scope = %q, want group", d.IncompatibleScope)
	}
	// force 与 Channel.Type 一致时放行
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: []protocol.Protocol{protocol.Gemini}}
	d2 := ResolvePrimary(in)
	if d2.Incompatible || !d2.Plan.IsLegacyFixed() {
		t.Fatalf("force gemini on gemini must pass as legacy fixed: %+v", d2)
	}
}

func TestNoCandidateFallsBackToLegacy(t *testing.T) {
	in := adaptiveInput()
	in.Features = RequestFeatureFlags{ResponsesNative: true} // 仅 Responses 直通可行
	in.Ingress = protocol.OpenAIResponse
	// 渠道只有 chat Profile → responses 被 Profile 过滤，chat 被矩阵 forbidden → 空候选
	d := ResolvePrimary(in)
	if d.Incompatible {
		t.Fatalf("unexpected incompatible: %+v", d)
	}
	if !d.Plan.IsLegacyFixed() {
		t.Fatal("empty adaptive candidates must fall back to legacy fixed")
	}
	// legacy 也不可用时 → incompatible
	in.LegacyEligible = false
	d2 := ResolvePrimary(in)
	if !d2.Incompatible {
		t.Fatal("want incompatible when neither adaptive nor legacy available")
	}
}

// ---- 跨候选无硬锁 ----

func TestNoCrossCandidateProtocolLock(t *testing.T) {
	// 候选 A：anthropic 渠道 → anthropic
	inA := adaptiveInput()
	inA.ChannelID, inA.ChannelKeyID = 1, 10
	inA.ChannelType = protocol.Anthropic
	dA := ResolvePrimary(inA)
	if dA.Plan.UpstreamProtocol() != protocol.Anthropic {
		t.Fatalf("candidate A = %q", dA.Plan.UpstreamProtocol())
	}
	// 候选 B：chat 渠道，previous=anthropic 只是平局提示 → 仍 chat
	inB := adaptiveInput()
	inB.ChannelID, inB.ChannelKeyID = 2, 20
	inB.PreviousAttemptProtocol = protocol.Anthropic
	dB := ResolvePrimary(inB)
	if dB.Plan.UpstreamProtocol() != protocol.OpenAIChat {
		t.Fatalf("candidate B = %q, previous protocol must not lock", dB.Plan.UpstreamProtocol())
	}
}

// ---- resolver 无副作用 ----

func TestResolverDoesNotMutateInput(t *testing.T) {
	in := adaptiveInput()
	in.Snapshot.EnabledProfiles = map[int]map[protocol.Protocol]bool{
		1: {protocol.OpenAIChat: true},
	}
	in.HeaderPolicy = HeaderPolicy{Set: map[string]string{"X-A": "1"}}
	_ = ResolvePrimary(in)
	if in.HeaderPolicy.Set["X-A"] != "1" || len(in.Snapshot.EnabledProfiles[1]) != 1 {
		t.Fatal("resolver mutated input")
	}
}

// ---- observe 影子钩子 ----

func TestObserveDisabledIsNoop(t *testing.T) {
	SetObserveEnabled(false)
	// 传入会 panic 的极端输入也必须安全（关闭时直接返回）
	ObserveShadowDecision(ResolveInput{})
}

func TestObserveSwallowsPanics(t *testing.T) {
	SetObserveEnabled(true)
	defer SetObserveEnabled(false)
	// force 规则 Protocols 为空会在 resolver 内触发越界 → 必须被吞掉
	in := adaptiveInput()
	in.Snapshot.Group = ScopedRule{Mode: ModeForce, Protocols: nil}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("observe leaked panic: %v", r)
			}
		}()
		ObserveShadowDecision(in)
	}()
}

func TestObserveRecordsWithoutChangingDecisionInputs(t *testing.T) {
	SetObserveEnabled(true)
	defer SetObserveEnabled(false)
	in := ShadowInputFromLegacy(1, 10, protocol.OpenAIChat, "alias", "m",
		protocol.OpenAIChat, RequestFeatureFlags{PlainText: true}, true)
	ObserveShadowDecision(in)
	if in.ChannelID != 1 || in.Snapshot.Mode != RoutingLegacy {
		t.Fatal("shadow input unexpectedly mutated")
	}
}
