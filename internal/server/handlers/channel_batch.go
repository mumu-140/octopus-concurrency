package handlers

import (
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/helper"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func batchUpdateChannels(c *gin.Context) {
	var request model.ChannelBatchUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.IDs) == 0 {
		resp.InvalidJSON(c)
		return
	}
	if (request.MaxConcurrency != nil && *request.MaxConcurrency < 0) ||
		(request.MaxRPM != nil && *request.MaxRPM < 0) {
		resp.Error(c, http.StatusBadRequest, "limits must be greater than or equal to 0")
		return
	}
	result := model.ChannelBatchUpdateResult{Errors: make(map[int]string)}
	for _, id := range uniquePositiveIDs(request.IDs) {
		if err := updateChannelBatchItem(c, id, request, &result); err != nil {
			result.Errors[id] = err.Error()
		}
	}
	resp.Success(c, result)
}

func updateChannelBatchItem(c *gin.Context, id int, batch model.ChannelBatchUpdateRequest, result *model.ChannelBatchUpdateResult) error {
	channel, err := op.ChannelGet(id, c.Request.Context())
	if err != nil {
		return err
	}
	request := &model.ChannelUpdateRequest{
		ID: id, MaxConcurrency: batch.MaxConcurrency, MaxRPM: batch.MaxRPM,
		AutoSync: batch.AutoSync, BypassManagedCheck: true,
	}
	if batch.HeaderMode != "" || len(batch.HeaderUpserts) > 0 || len(batch.HeaderDeletes) > 0 {
		headers := applyHeaderPatch(channel.CustomHeader, batch.HeaderMode, batch.HeaderUpserts, batch.HeaderDeletes)
		request.CustomHeader = &headers
	}
	updated, err := op.ChannelUpdate(request, c.Request.Context())
	if err != nil {
		return err
	}
	result.Updated++
	if !batch.RefreshModels {
		return nil
	}
	models, err := helper.FetchModels(c.Request.Context(), *updated)
	if err != nil {
		return err
	}
	modelCSV := strings.Join(models, ",")
	_, err = op.ChannelUpdate(&model.ChannelUpdateRequest{
		ID: id, Model: &modelCSV, BypassManagedCheck: true,
	}, c.Request.Context())
	if err == nil {
		result.ModelsUpdated++
	}
	return err
}

func applyHeaderPatch(current []model.CustomHeader, mode string, upserts []model.CustomHeader, deletes []string) []model.CustomHeader {
	result := current
	if mode == "replace" {
		result = nil
	}
	deleted := make(map[string]struct{}, len(deletes))
	for _, key := range deletes {
		deleted[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	values := make(map[string]model.CustomHeader, len(result)+len(upserts))
	order := make([]string, 0, len(result)+len(upserts))
	put := func(header model.CustomHeader) {
		key := strings.ToLower(strings.TrimSpace(header.HeaderKey))
		if key == "" {
			return
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		header.HeaderKey = strings.TrimSpace(header.HeaderKey)
		values[key] = header
	}
	for _, header := range result {
		put(header)
	}
	for _, header := range upserts {
		put(header)
	}
	merged := make([]model.CustomHeader, 0, len(values))
	for _, key := range order {
		if _, remove := deleted[key]; remove {
			continue
		}
		merged = append(merged, values[key])
	}
	return merged
}

func uniquePositiveIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
