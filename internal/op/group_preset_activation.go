package op

import (
	"context"
	"errors"
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// mirrorPresetToActiveGroupTx mirrors an active preset into its owning group.
func mirrorPresetToActiveGroupTx(tx *gorm.DB, preset *model.GroupPreset) (groupID int, channelIDs []int, err error) {
	var group model.Group
	err = tx.Where("active_preset_id = ?", preset.ID).First(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("failed to find owning group: %w", err)
	}

	var oldItems []model.GroupItem
	if err = tx.Where("group_id = ?", group.ID).Find(&oldItems).Error; err != nil {
		return group.ID, nil, fmt.Errorf("failed to load old items: %w", err)
	}
	ids := make([]int, 0, len(oldItems)+len(preset.Items))
	for _, item := range oldItems {
		ids = append(ids, item.ChannelID)
	}
	for _, item := range preset.Items {
		ids = append(ids, item.ChannelID)
	}

	maxRetries := preset.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	updates := model.Group{
		Mode:               preset.Mode,
		MatchRegex:         preset.MatchRegex,
		FirstTokenTimeOut:  preset.FirstTokenTimeOut,
		SessionKeepTime:    preset.SessionKeepTime,
		RetryEnabled:       preset.RetryEnabled,
		MaxRetries:         maxRetries,
		ProtocolMode:       preset.ProtocolMode,
		PreferredProtocols: append([]string(nil), preset.PreferredProtocols...),
		PolicyRevision:     preset.PolicyRevision,
	}
	if err = tx.Model(&model.Group{}).Where("id = ?", group.ID).
		Select("mode", "match_regex", "first_token_time_out", "session_keep_time", "retry_enabled", "max_retries", "protocol_mode", "preferred_protocols", "policy_revision").
		Updates(&updates).Error; err != nil {
		return group.ID, ids, fmt.Errorf("failed to mirror preset to group: %w", err)
	}

	if err = tx.Where("group_id = ?", group.ID).Delete(&model.GroupItem{}).Error; err != nil {
		return group.ID, ids, fmt.Errorf("failed to clear old items: %w", err)
	}
	if len(preset.Items) > 0 {
		newItems := presetItemsForGroup(group.ID, preset.Items)
		if err = tx.Create(&newItems).Error; err != nil {
			return group.ID, ids, fmt.Errorf("failed to insert new items: %w", err)
		}
	}
	return group.ID, ids, nil
}

// syncActivePresetTx keeps the active preset equal to the group's live state.
func syncActivePresetTx(tx *gorm.DB, groupID int) error {
	var group model.Group
	if err := tx.First(&group, groupID).Error; err != nil {
		return fmt.Errorf("failed to load group for preset sync: %w", err)
	}
	if group.ActivePresetID == nil {
		return nil
	}
	presetID := *group.ActivePresetID

	var preset model.GroupPreset
	if err := tx.Where("id = ? AND group_id = ?", presetID, groupID).First(&preset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Model(&model.Group{}).
				Where("id = ? AND active_preset_id = ?", groupID, presetID).
				Update("active_preset_id", gorm.Expr("NULL")).Error
		}
		return fmt.Errorf("failed to load active preset: %w", err)
	}

	var items []model.GroupItem
	if err := tx.Where("group_id = ?", groupID).Order("priority ASC").Find(&items).Error; err != nil {
		return fmt.Errorf("failed to load group items: %w", err)
	}
	presetItems := make([]model.GroupPresetItem, 0, len(items))
	for _, item := range items {
		presetItems = append(presetItems, model.GroupPresetItem{
			ChannelID: item.ChannelID,
			ModelName: item.ModelName,
			Priority:  item.Priority,
			Weight:    item.Weight,
		})
	}

	preset.Mode = group.Mode
	preset.MatchRegex = group.MatchRegex
	preset.FirstTokenTimeOut = group.FirstTokenTimeOut
	preset.SessionKeepTime = group.SessionKeepTime
	preset.RetryEnabled = group.RetryEnabled
	preset.MaxRetries = group.MaxRetries
	preset.Items = presetItems
	preset.ProtocolMode = group.ProtocolMode
	preset.PreferredProtocols = append([]string(nil), group.PreferredProtocols...)
	preset.PolicyRevision = group.PolicyRevision

	if err := tx.Save(&preset).Error; err != nil {
		return fmt.Errorf("failed to sync active preset: %w", err)
	}
	return nil
}

// GroupPresetActivate applies a preset to its group's live configuration.
func GroupPresetActivate(presetID int, ctx context.Context) error {
	var preset model.GroupPreset
	if err := db.GetDB().WithContext(ctx).First(&preset, presetID).Error; err != nil {
		return fmt.Errorf("preset not found")
	}
	oldGroup, ok := groupCache.Get(preset.GroupID)
	if !ok {
		return fmt.Errorf("group not found")
	}
	if missing := missingPresetChannels(preset.Items); len(missing) > 0 {
		return fmt.Errorf("preset references missing channels: %v", missing)
	}

	channelIDs := make([]int, 0, len(oldGroup.Items)+len(preset.Items))
	for _, item := range oldGroup.Items {
		channelIDs = append(channelIDs, item.ChannelID)
	}
	for _, item := range preset.Items {
		channelIDs = append(channelIDs, item.ChannelID)
	}

	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", preset.GroupID).Delete(&model.GroupItem{}).Error; err != nil {
			return fmt.Errorf("failed to clear old items: %w", err)
		}
		if len(preset.Items) > 0 {
			newItems := presetItemsForGroup(preset.GroupID, preset.Items)
			if err := tx.Create(&newItems).Error; err != nil {
				return fmt.Errorf("failed to insert preset items: %w", err)
			}
		}
		return activatePresetGroupTx(tx, &preset)
	}); err != nil {
		return err
	}

	if err := groupRefreshCacheByID(preset.GroupID, ctx); err != nil {
		return fmt.Errorf("failed to refresh cache: %w", err)
	}
	resetBalancerStateForChannels(channelIDs...)
	return nil
}

func missingPresetChannels(items []model.GroupPresetItem) []int {
	missing := make([]int, 0)
	seen := make(map[int]struct{})
	for _, item := range items {
		if _, duplicate := seen[item.ChannelID]; duplicate {
			continue
		}
		seen[item.ChannelID] = struct{}{}
		if _, exists := channelCache.Get(item.ChannelID); !exists {
			missing = append(missing, item.ChannelID)
		}
	}
	return missing
}

func presetItemsForGroup(groupID int, items []model.GroupPresetItem) []model.GroupItem {
	result := make([]model.GroupItem, 0, len(items))
	for _, item := range items {
		result = append(result, model.GroupItem{
			GroupID:   groupID,
			ChannelID: item.ChannelID,
			ModelName: item.ModelName,
			Priority:  item.Priority,
			Weight:    item.Weight,
		})
	}
	return result
}

func activatePresetGroupTx(tx *gorm.DB, preset *model.GroupPreset) error {
	maxRetries := preset.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	activePresetID := preset.ID
	updates := model.Group{
		Mode:               preset.Mode,
		MatchRegex:         preset.MatchRegex,
		FirstTokenTimeOut:  preset.FirstTokenTimeOut,
		SessionKeepTime:    preset.SessionKeepTime,
		RetryEnabled:       preset.RetryEnabled,
		MaxRetries:         maxRetries,
		ActivePresetID:     &activePresetID,
		ProtocolMode:       preset.ProtocolMode,
		PreferredProtocols: append([]string(nil), preset.PreferredProtocols...),
		PolicyRevision:     preset.PolicyRevision,
	}
	if err := tx.Model(&model.Group{}).Where("id = ?", preset.GroupID).
		Select("mode", "match_regex", "first_token_time_out", "session_keep_time", "retry_enabled", "max_retries", "active_preset_id", "protocol_mode", "preferred_protocols", "policy_revision").
		Updates(&updates).Error; err != nil {
		return fmt.Errorf("failed to update group: %w", err)
	}
	return nil
}
