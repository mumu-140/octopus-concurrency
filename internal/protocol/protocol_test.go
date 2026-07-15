package protocol

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	tmodel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestOutboundTypeMappingIsStableAndBijective(t *testing.T) {
	cases := []struct {
		out  outbound.OutboundType
		want Protocol
	}{
		{outbound.OutboundTypeOpenAIChat, OpenAIChat},
		{outbound.OutboundTypeOpenAIResponse, OpenAIResponse},
		{outbound.OutboundTypeAnthropic, Anthropic},
		{outbound.OutboundTypeGemini, Gemini},
		{outbound.OutboundTypeVolcengine, Volcengine},
		{outbound.OutboundTypeOpenAIEmbedding, OpenAIEmbedding},
	}
	for _, c := range cases {
		if got := FromOutboundType(c.out); got != c.want {
			t.Fatalf("FromOutboundType(%d) = %q, want %q", c.out, got, c.want)
		}
		back, ok := c.want.ToOutboundType()
		if !ok || back != c.out {
			t.Fatalf("%q.ToOutboundType() = (%d,%t), want (%d,true)", c.want, back, ok, c.out)
		}
	}
	if got := FromOutboundType(outbound.OutboundType(99)); got != Unknown {
		t.Fatalf("unknown OutboundType mapped to %q, want unknown", got)
	}
	if _, ok := Unknown.ToOutboundType(); ok {
		t.Fatal("Unknown.ToOutboundType() must not resolve")
	}
}

func TestSiteModelRouteTypeMapping(t *testing.T) {
	if got := FromSiteModelRouteType(model.SiteModelRouteTypeAnthropic); got != Anthropic {
		t.Fatalf("route anthropic -> %q", got)
	}
	if got := FromSiteModelRouteType(model.SiteModelRouteTypeUnknown); got != Unknown {
		t.Fatalf("route unknown -> %q", got)
	}
	if got := Gemini.ToSiteModelRouteType(); got != model.SiteModelRouteTypeGemini {
		t.Fatalf("gemini back-mapping -> %q", got)
	}
	if got := Unknown.ToSiteModelRouteType(); got != model.SiteModelRouteTypeUnknown {
		t.Fatalf("unknown back-mapping -> %q", got)
	}
}

func TestAPIFormatMapping(t *testing.T) {
	cases := map[tmodel.APIFormat]Protocol{
		tmodel.APIFormatOpenAIChatCompletion:  OpenAIChat,
		tmodel.APIFormatOpenAIResponse:        OpenAIResponse,
		tmodel.APIFormatAnthropicMessage:      Anthropic,
		tmodel.APIFormatGeminiContents:        Gemini,
		tmodel.APIFormatOpenAIEmbedding:       OpenAIEmbedding,
		tmodel.APIFormatOpenAIImageGeneration: Unknown, // 图片生成独立 Handler
		tmodel.APIFormatAiSDKText:             Unknown,
	}
	for f, want := range cases {
		if got := FromAPIFormat(f); got != want {
			t.Fatalf("FromAPIFormat(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestAdaptiveSetIsExactlyThreeProtocols(t *testing.T) {
	adaptive := []Protocol{OpenAIChat, OpenAIResponse, Anthropic}
	fixed := []Protocol{Gemini, Volcengine, OpenAIEmbedding, Unknown}
	for _, p := range adaptive {
		if !p.IsAdaptive() {
			t.Fatalf("%q must be adaptive", p)
		}
	}
	for _, p := range fixed {
		if p.IsAdaptive() {
			t.Fatalf("%q must NOT be adaptive", p)
		}
	}
}

func TestValid(t *testing.T) {
	for _, p := range []Protocol{OpenAIChat, OpenAIResponse, Anthropic, Gemini, Volcengine, OpenAIEmbedding} {
		if !p.Valid() {
			t.Fatalf("%q should be valid", p)
		}
	}
	if Unknown.Valid() || Protocol("bogus").Valid() {
		t.Fatal("unknown/bogus must be invalid")
	}
}
