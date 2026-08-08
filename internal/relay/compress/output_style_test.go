package compress

import (
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func styleCfg(style string) *dbmodel.GroupCompressConfig {
	return &dbmodel.GroupCompressConfig{Enabled: true, OutputStyle: style}
}

func TestOutputStyleInjectsIntoExistingSystem(t *testing.T) {
	msgs := []transformerModel.Message{
		textMsg("system", "You are a helpful assistant."),
		textMsg("user", "hi"),
	}
	out := outputStyleEngine{}.Apply(msgs, styleCfg("terse-prose"))
	if len(out) != 2 {
		t.Fatalf("should not add message when system exists, got %d", len(out))
	}
	got := msgText(out[0])
	if !strings.HasPrefix(got, "You are a helpful assistant.") || !strings.Contains(got, styleMarker) {
		t.Fatalf("style line should be appended to system message: %q", got)
	}
}

func TestOutputStyleCreatesSystemWhenAbsent(t *testing.T) {
	msgs := []transformerModel.Message{textMsg("user", "hi")}
	out := outputStyleEngine{}.Apply(msgs, styleCfg("terse-cjk"))
	if len(out) != 2 || out[0].Role != "system" {
		t.Fatalf("expected new leading system message")
	}
	if !strings.Contains(msgText(out[0]), styleMarker) {
		t.Fatalf("marker missing")
	}
	if msgText(out[1]) != "hi" {
		t.Fatalf("user message must be untouched")
	}
}

func TestOutputStyleIdempotent(t *testing.T) {
	msgs := []transformerModel.Message{textMsg("system", "sys"), textMsg("user", "hi")}
	once := outputStyleEngine{}.Apply(msgs, styleCfg("terse-prose"))
	twice := outputStyleEngine{}.Apply(once, styleCfg("terse-prose"))
	if msgText(once[0]) != msgText(twice[0]) || len(twice) != len(once) {
		t.Fatalf("output style must be idempotent")
	}
	if strings.Count(msgText(twice[0]), styleMarker) != 1 {
		t.Fatalf("marker must appear exactly once")
	}
}

func TestOutputStyleOffOrUnknown(t *testing.T) {
	msgs := []transformerModel.Message{textMsg("system", "sys")}
	engine := outputStyleEngine{}
	if out := engine.Apply(msgs, styleCfg("")); msgText(out[0]) != "sys" {
		t.Fatalf("empty style must be no-op")
	}
	if out := engine.Apply(msgs, styleCfg("nonexistent")); msgText(out[0]) != "sys" {
		t.Fatalf("unknown style must be no-op")
	}
}
