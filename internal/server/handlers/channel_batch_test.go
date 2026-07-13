package handlers

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestApplyHeaderPatchMergeCaseInsensitive(t *testing.T) {
	got := applyHeaderPatch(
		[]model.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: "old"}, {HeaderKey: "X-Test", HeaderValue: "keep"}},
		"merge",
		[]model.CustomHeader{{HeaderKey: "user-agent", HeaderValue: "new"}, {HeaderKey: "Originator", HeaderValue: "codex_cli_rs"}},
		[]string{"x-test"},
	)
	if len(got) != 2 || got[0].HeaderValue != "new" || got[1].HeaderKey != "Originator" {
		t.Fatalf("unexpected headers: %#v", got)
	}
}

func TestApplyHeaderPatchReplace(t *testing.T) {
	got := applyHeaderPatch(
		[]model.CustomHeader{{HeaderKey: "X-Old", HeaderValue: "old"}},
		"replace",
		[]model.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: "client"}},
		nil,
	)
	if len(got) != 1 || got[0].HeaderKey != "User-Agent" {
		t.Fatalf("unexpected headers: %#v", got)
	}
}
