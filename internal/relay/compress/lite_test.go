package compress

import (
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func textMsg(role, text string) transformerModel.Message {
	return transformerModel.Message{Role: role, Content: transformerModel.MessageContent{Content: &text}}
}

func msgText(m transformerModel.Message) string {
	if m.Content.Content != nil {
		return *m.Content.Content
	}
	var b strings.Builder
	for _, p := range m.Content.MultipleContent {
		if p.Text != nil {
			b.WriteString(*p.Text)
		}
	}
	return b.String()
}

func padTo(s string, n int) string {
	for len(s) < n {
		s += strings.Repeat("x", 100) + "\n"
	}
	return s
}

func liteCfg() *dbmodel.GroupCompressConfig {
	return &dbmodel.GroupCompressConfig{Enabled: true, Lite: true}
}

func TestCollapseWhitespaceFoldsBlankRuns(t *testing.T) {
	body := "line one\n\n\n\n\nline two   \nline three\t\t\n"
	body = padTo(body, liteMinTextChars+100)
	out := collapseWhitespace(body)
	if strings.Contains(out, "\n\n\n") {
		t.Fatalf("expected blank runs folded, got %q", out[:120])
	}
	if strings.Contains(out, "two   \n") || strings.Contains(out, "three\t\t\n") {
		t.Fatalf("expected trailing whitespace trimmed")
	}
}

func TestCollapseWhitespacePreservesFencedCode(t *testing.T) {
	inner := "package main\n\n\n\n\nfunc main() {}\n"
	text := padTo("before\n\n\n\nafter\n```go\n"+inner+"```\ntail text\n", liteMinTextChars+100)
	out := collapseWhitespace(text)
	if !strings.Contains(out, inner) {
		t.Fatalf("fenced code block must be preserved verbatim")
	}
	if strings.Contains(out, "before\n\n\n\nafter") {
		t.Fatalf("blank run outside fence should be folded")
	}
}

func TestLiteSkipsShortText(t *testing.T) {
	short := "a\n\n\n\nb"
	msgs := []transformerModel.Message{textMsg("user", short)}
	out := liteEngine{}.Apply(msgs, liteCfg())
	if msgText(out[0]) != short {
		t.Fatalf("short text must be untouched")
	}
}

func TestTruncateKeepHeadTailPreservesSummary(t *testing.T) {
	head := "npm warn deprecated inflight@1.0.6\n"
	tail := "added 214 packages in 9s\nfound 0 vulnerabilities\n"
	body := head + strings.Repeat("npm warn deprecated intermediate line\n", 200) + tail
	msgs := []transformerModel.Message{textMsg("tool", body)}
	out := liteEngine{}.Apply(msgs, liteCfg())
	got := msgText(out[0])
	if len(got) >= len(body) {
		t.Fatalf("expected reduction, before=%d after=%d", len(body), len(got))
	}
	if !strings.Contains(got, head[:20]) {
		t.Fatalf("head must be preserved")
	}
	if !strings.Contains(got, "found 0 vulnerabilities") {
		t.Fatalf("tail summary line must be preserved (OmniRoute head-only truncation lost it)")
	}
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("truncation marker missing")
	}
}

func TestDedupSystemMessages(t *testing.T) {
	sys := strings.Repeat("You are a helpful assistant. ", 40)
	msgs := []transformerModel.Message{
		textMsg("system", sys),
		textMsg("user", "q"),
		textMsg("system", sys),
	}
	out := liteEngine{}.Apply(msgs, liteCfg())
	count := 0
	for _, m := range out {
		if m.Role == "system" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected duplicate system message removed, got %d system messages", count)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
}

func TestLiteIdempotent(t *testing.T) {
	body := padTo("alpha\n\n\n\n\nbeta   \n", liteMinTextChars+200)
	msgs := []transformerModel.Message{textMsg("user", body)}
	once := liteEngine{}.Apply(msgs, liteCfg())
	twice := liteEngine{}.Apply(once, liteCfg())
	if msgText(once[0]) != msgText(twice[0]) {
		t.Fatalf("lite must be idempotent")
	}
}

func TestLiteMultiModalTextPart(t *testing.T) {
	long := padTo("part one\n\n\n\n\npart two\n", liteMinTextChars+100)
	msg := transformerModel.Message{
		Role: "user",
		Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
			{Type: "text", Text: &long},
		}},
	}
	out := liteEngine{}.Apply([]transformerModel.Message{msg}, liteCfg())
	got := msgText(out[0])
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("multimodal text part should be collapsed too")
	}
}
