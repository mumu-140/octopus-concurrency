package task

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestBuildChannelSyncResultReportsModelDelta(t *testing.T) {
	channel := model.Channel{ID: 7, Name: "primary", Model: "gpt-4o, gpt-4.1"}

	result, normalizedModels := buildChannelSyncResult(channel, []string{"gpt-4.1", "gpt-5", ""}, nil)

	if result.Status != ChannelSyncStatusUpdated {
		t.Fatalf("status = %q, want %q", result.Status, ChannelSyncStatusUpdated)
	}
	if !reflect.DeepEqual(normalizedModels, []string{"gpt-4.1", "gpt-5"}) {
		t.Fatalf("normalized models = %#v", normalizedModels)
	}
	if !reflect.DeepEqual(result.AddedModels, []string{"gpt-5"}) {
		t.Fatalf("added models = %#v", result.AddedModels)
	}
	if !reflect.DeepEqual(result.RemovedModels, []string{"gpt-4o"}) {
		t.Fatalf("removed models = %#v", result.RemovedModels)
	}
}

func TestBuildChannelSyncResultClassifiesUnchangedAndFailedFetches(t *testing.T) {
	channel := model.Channel{ID: 9, Name: "secondary", Model: "claude-sonnet"}

	unchanged, _ := buildChannelSyncResult(channel, []string{"claude-sonnet"}, nil)
	if unchanged.Status != ChannelSyncStatusUnchanged {
		t.Fatalf("unchanged status = %q, want %q", unchanged.Status, ChannelSyncStatusUnchanged)
	}

	failed, models := buildChannelSyncResult(channel, nil, errors.New("upstream timeout"))
	if failed.Status != ChannelSyncStatusFailed {
		t.Fatalf("failed status = %q, want %q", failed.Status, ChannelSyncStatusFailed)
	}
	if failed.Error != "upstream timeout" {
		t.Fatalf("failure reason = %q", failed.Error)
	}
	if models != nil {
		t.Fatalf("failed fetch models = %#v, want nil", models)
	}
}
