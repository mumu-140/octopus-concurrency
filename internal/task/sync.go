package task

import (
	"context"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/helper"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/diff"
	"github.com/bestruirui/octopus/internal/utils/log"
)

var lastSyncModelsTime = time.Now()

// SyncModelsTask keeps the scheduled task entrypoint backward compatible.
func SyncModelsTask() {
	_ = SyncModelsTaskWithReport()
}

// SyncModelsTaskWithReport refreshes every auto-synced channel and records the
// per-channel model delta or failure for the manual sync UI.
func SyncModelsTaskWithReport() (report SyncModelsReport) {
	report.StartedAt = time.Now()
	report.Results = make([]ChannelSyncResult, 0)
	defer func() {
		report.CompletedAt = time.Now()
		log.Debugf("sync models task finished: checked=%d updated=%d unchanged=%d failed=%d duration=%s", report.Checked, report.Updated, report.Unchanged, report.Failed, time.Since(report.StartedAt))
	}()

	log.Debugf("sync models task started")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	channels, err := op.ChannelList(ctx)
	if err != nil {
		report.Error = err.Error()
		log.Errorf("failed to list channels: %v", err)
		return report
	}

	totalNewModels := make([]string, 0, 128)
	seenTotalNewModels := make(map[string]struct{}, 128)
	for _, channel := range channels {
		if !channel.AutoSync {
			continue
		}

		report.Checked++
		fetchedModels, fetchErr := helper.FetchModels(ctx, channel)
		result, newModels := buildChannelSyncResult(channel, fetchedModels, fetchErr)
		if fetchErr != nil {
			report.Failed++
			report.Results = append(report.Results, result)
			log.Warnf("failed to fetch models for channel %s: %v", channel.Name, fetchErr)
			continue
		}

		for _, name := range newModels {
			normalized := strings.ToLower(strings.TrimSpace(name))
			if normalized == "" {
				continue
			}
			if _, exists := seenTotalNewModels[normalized]; exists {
				continue
			}
			seenTotalNewModels[normalized] = struct{}{}
			totalNewModels = append(totalNewModels, normalized)
		}

		if result.Status == ChannelSyncStatusUpdated {
			fetchModelStr := strings.Join(newModels, ",")
			if _, err := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Model: &fetchModelStr}, ctx); err != nil {
				result.Status = ChannelSyncStatusFailed
				result.Error = err.Error()
				report.Failed++
				report.Results = append(report.Results, result)
				log.Errorf("failed to update channel %s: %v", channel.Name, err)
				continue
			}
			report.Updated++

			if len(result.RemovedModels) > 0 {
				log.Infof("deleted channel %s models: %v", channel.Name, result.RemovedModels)
				keys := make([]model.GroupIDAndLLMName, len(result.RemovedModels))
				for i, name := range result.RemovedModels {
					keys[i] = model.GroupIDAndLLMName{ChannelID: channel.ID, ModelName: name}
				}
				if err := op.GroupItemBatchDelByChannelAndModels(keys, ctx); err != nil {
					log.Errorf("failed to batch delete group items for channel %s: %v", channel.Name, err)
				}
			}
		} else {
			report.Unchanged++
		}

		if len(newModels) > 0 {
			helper.ChannelAutoGroup(&channel, ctx)
		}
		report.Results = append(report.Results, result)
	}

	llmPrice, err := op.LLMList(ctx)
	if err != nil {
		report.Error = err.Error()
		log.Errorf("failed to list models price: %v", err)
		return report
	}
	llmPriceNames := make([]string, 0, len(llmPrice))
	for _, price := range llmPrice {
		llmPriceNames = append(llmPriceNames, price.Name)
	}

	deletedNorm, addedNorm := diff.Diff(llmPriceNames, totalNewModels)
	if len(deletedNorm) > 0 {
		if err := helper.LLMPriceDeleteFromDBWithNoPrice(deletedNorm, ctx); err != nil {
			report.Error = err.Error()
			log.Errorf("failed to batch delete models price: %v", err)
		}
	}
	if len(addedNorm) > 0 {
		if err := helper.LLMPriceAddToDB(addedNorm, ctx); err != nil {
			report.Error = err.Error()
			log.Errorf("failed to add models price: %v", err)
		}
	}
	lastSyncModelsTime = time.Now()
	return report
}

func GetLastSyncModelsTime() time.Time {
	return lastSyncModelsTime
}
