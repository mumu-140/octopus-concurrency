package compress

import (
	"encoding/json"
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

type panicEngine struct{}

func (panicEngine) Name() string { return "panic" }
func (panicEngine) Apply(msgs []transformerModel.Message, _ *dbmodel.GroupCompressConfig) []transformerModel.Message {
	panic("boom")
}

func withMaster(t testing.TB, on bool) {
	t.Helper()
	orig := masterEnabledFn
	masterEnabledFn = func() bool { return on }
	t.Cleanup(func() { masterEnabledFn = orig })
}

func withEngines(t testing.TB, es []Engine) {
	t.Helper()
	orig := engines
	engines = es
	t.Cleanup(func() { engines = orig })
}

func buildRequest(texts ...string) *transformerModel.InternalLLMRequest {
	msgs := make([]transformerModel.Message, len(texts))
	for i, tx := range texts {
		msgs[i] = textMsg("user", tx)
	}
	return &transformerModel.InternalLLMRequest{Model: "m", Messages: msgs}
}

func marshalMessages(t *testing.T, req *transformerModel.InternalLLMRequest) string {
	t.Helper()
	b, err := json.Marshal(req.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func enabledGroup() dbmodel.Group {
	return dbmodel.Group{ID: 1, CompressConfig: &dbmodel.GroupCompressConfig{
		Enabled: true, Lite: true, Headroom: true, OutputStyle: "terse-prose",
	}}
}

func TestMaybeApplyNilConfigUntouched(t *testing.T) {
	withMaster(t, true)
	req := buildRequest(padTo("a\n\n\n\nb", liteMinTextChars+100))
	before := marshalMessages(t, req)
	MaybeApply(req, dbmodel.Group{ID: 1}) // nil CompressConfig
	if marshalMessages(t, req) != before {
		t.Fatalf("nil config group must see zero change")
	}
}

func TestMaybeApplyMasterOffUntouched(t *testing.T) {
	withMaster(t, false)
	req := buildRequest(padTo("a\n\n\n\nb", liteMinTextChars+100))
	before := marshalMessages(t, req)
	MaybeApply(req, enabledGroup())
	if marshalMessages(t, req) != before {
		t.Fatalf("master off must see zero change")
	}
}

func TestMaybeApplyDisabledGroupUntouched(t *testing.T) {
	withMaster(t, true)
	req := buildRequest(padTo("a\n\n\n\nb", liteMinTextChars+100))
	before := marshalMessages(t, req)
	g := enabledGroup()
	g.CompressConfig.Enabled = false
	MaybeApply(req, g)
	if marshalMessages(t, req) != before {
		t.Fatalf("disabled group must see zero change")
	}
}

func TestMaybeApplyFailOpenOnPanic(t *testing.T) {
	withMaster(t, true)
	withEngines(t, []Engine{panicEngine{}})
	body := padTo("alpha\n\n\n\nbeta", liteMinTextChars+100)
	req := buildRequest(body)
	before := marshalMessages(t, req)
	MaybeApply(req, enabledGroup()) // 不应 panic
	if marshalMessages(t, req) != before {
		t.Fatalf("panic engine must leave request unchanged")
	}
}

func TestMaybeApplyChainWorks(t *testing.T) {
	withMaster(t, true)
	raw := buildJSONArray(60)
	toolBody := "npm output head\n" + padTo("", toolMaxChars+2000) + "found 0 vulnerabilities\n"
	req := &transformerModel.InternalLLMRequest{Model: "m", Messages: []transformerModel.Message{
		textMsg("user", "metrics:\n"+raw),
		textMsg("tool", toolBody),
	}}
	before := messagesSize(req.Messages)
	MaybeApply(req, enabledGroup())
	after := messagesSize(req.Messages)
	if after >= before {
		t.Fatalf("expected reduction, before=%d after=%d", before, after)
	}
	if !containsText(req.Messages, "found 0 vulnerabilities") {
		t.Fatalf("tool tail summary must survive the chain")
	}
	if !containsText(req.Messages, tabularMarker) {
		t.Fatalf("headroom conversion missing")
	}
	if !containsText(req.Messages, styleMarker) {
		t.Fatalf("output style injection missing")
	}
	// 压缩统计应暂存到请求对象,供 relay metrics 落库。
	if req.CompressStats == nil {
		t.Fatalf("expected CompressStats to be set on successful compression")
	}
	if req.CompressStats.BeforeBytes != before || req.CompressStats.AfterBytes != after {
		t.Fatalf("CompressStats = {before=%d after=%d}, want {before=%d after=%d}",
			req.CompressStats.BeforeBytes, req.CompressStats.AfterBytes, before, after)
	}
}

func TestMaybeApplyEmptyMessages(t *testing.T) {
	withMaster(t, true)
	MaybeApply(&transformerModel.InternalLLMRequest{Model: "m"}, enabledGroup()) // 不应 panic
	MaybeApply(nil, enabledGroup())                                              // 不应 panic
}

func containsText(msgs []transformerModel.Message, sub string) bool {
	found := false
	forEachText(msgs, func(_, text string) string {
		if strings.Contains(text, sub) {
			found = true
		}
		return text
	})
	return found
}
