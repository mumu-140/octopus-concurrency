package task

import (
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/diff"
	"github.com/bestruirui/octopus/internal/utils/xstrings"
)

// ChannelSyncStatus describes the observed result for one auto-synced channel.
type ChannelSyncStatus string

const (
	ChannelSyncStatusUpdated   ChannelSyncStatus = "updated"
	ChannelSyncStatusUnchanged ChannelSyncStatus = "unchanged"
	ChannelSyncStatusFailed    ChannelSyncStatus = "failed"
)

// ChannelSyncResult contains the user-visible outcome of one channel model sync.
type ChannelSyncResult struct {
	ChannelID     int               `json:"channel_id"`
	ChannelName   string            `json:"channel_name"`
	Status        ChannelSyncStatus `json:"status"`
	AddedModels   []string          `json:"added_models"`
	RemovedModels []string          `json:"removed_models"`
	Error         string            `json:"error,omitempty"`
}

// SyncModelsReport is returned by a manual sync so operators can inspect the
// exact per-channel model changes without reading server logs.
type SyncModelsReport struct {
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt time.Time           `json:"completed_at"`
	Checked     int                 `json:"checked"`
	Updated     int                 `json:"updated"`
	Unchanged   int                 `json:"unchanged"`
	Failed      int                 `json:"failed"`
	Results     []ChannelSyncResult `json:"results"`
	Error       string              `json:"error,omitempty"`
}

func buildChannelSyncResult(channel model.Channel, fetchedModels []string, fetchErr error) (ChannelSyncResult, []string) {
	result := ChannelSyncResult{
		ChannelID:     channel.ID,
		ChannelName:   channel.Name,
		AddedModels:   []string{},
		RemovedModels: []string{},
	}
	if fetchErr != nil {
		result.Status = ChannelSyncStatusFailed
		result.Error = fetchErr.Error()
		return result, nil
	}

	oldModels := xstrings.SplitTrimCompact(",", channel.Model)
	normalizedModels := xstrings.TrimCompact(fetchedModels)
	removedModels, addedModels := diff.Diff(oldModels, normalizedModels)
	result.AddedModels = addedModels
	result.RemovedModels = removedModels
	if len(addedModels) == 0 && len(removedModels) == 0 {
		result.Status = ChannelSyncStatusUnchanged
		return result, normalizedModels
	}
	result.Status = ChannelSyncStatusUpdated
	return result, normalizedModels
}
