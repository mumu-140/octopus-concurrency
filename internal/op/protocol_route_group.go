package op

import (
	"context"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// GroupProtocolPolicyUpdate updates one group policy with revision CAS.
func GroupProtocolPolicyUpdate(groupID int, req *model.ScopedProtocolPolicyUpdateRequest, actor string, ctx context.Context) (*model.ProtocolPolicyState, error) {
	if req == nil {
		return nil, protocolRoutingValidationError("group protocol policy request is nil")
	}
	if req.ExpectedRevision < 0 {
		return nil, protocolRoutingValidationError("expected_revision must not be negative")
	}
	if err := validateScopedProtocolRule(req.Mode, req.PreferredProtocols, false); err != nil {
		return nil, err
	}
	conn := db.GetDB().WithContext(ctx)
	if err := requireProtocolPolicyGroup(conn, groupID); err != nil {
		return nil, err
	}
	state, err := commitProtocolPolicyMutation(ctx, req.ExpectedRevision, actor, func(tx *gorm.DB, _ *model.ProtocolRoutingConfig, nextRevision int64) error {
		updates := model.Group{
			ProtocolMode:       req.Mode,
			PreferredProtocols: append([]string(nil), req.PreferredProtocols...),
			PolicyRevision:     nextRevision,
		}
		result := tx.Model(&model.Group{}).Where("id = ?", groupID).
			Select("protocol_mode", "preferred_protocols", "policy_revision").Updates(&updates)
		if result.Error != nil {
			return protocolRoutingDatabaseError("update group protocol policy", result.Error)
		}
		if result.RowsAffected != 1 {
			return protocolRoutingNotFoundError("group not found")
		}
		if err := syncActivePresetTx(tx, groupID); err != nil {
			return protocolRoutingDatabaseError("sync active group preset protocol policy", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := groupRefreshCacheByID(groupID, ctx); err != nil {
		return nil, protocolRoutingDatabaseError("refresh group protocol policy cache", err)
	}
	return state, nil
}

// GroupPresetProtocolPolicyUpdate updates one preset policy and mirrors active presets.
func GroupPresetProtocolPolicyUpdate(presetID int, req *model.ScopedProtocolPolicyUpdateRequest, actor string, ctx context.Context) (*model.ProtocolPolicyState, error) {
	if req == nil {
		return nil, protocolRoutingValidationError("group preset protocol policy request is nil")
	}
	if req.ExpectedRevision < 0 {
		return nil, protocolRoutingValidationError("expected_revision must not be negative")
	}
	if err := validateScopedProtocolRule(req.Mode, req.PreferredProtocols, false); err != nil {
		return nil, err
	}
	conn := db.GetDB().WithContext(ctx)
	var preset model.GroupPreset
	if err := conn.First(&preset, presetID).Error; err != nil {
		return nil, protocolRoutingNotFoundError("group preset not found")
	}
	mirrorGroupID := 0
	state, err := commitProtocolPolicyMutation(ctx, req.ExpectedRevision, actor, func(tx *gorm.DB, _ *model.ProtocolRoutingConfig, nextRevision int64) error {
		if err := tx.First(&preset, presetID).Error; err != nil {
			return protocolRoutingNotFoundError("group preset not found")
		}
		preset.ProtocolMode = req.Mode
		preset.PreferredProtocols = append([]string(nil), req.PreferredProtocols...)
		preset.PolicyRevision = nextRevision
		if err := tx.Select("protocol_mode", "preferred_protocols", "policy_revision").Save(&preset).Error; err != nil {
			return protocolRoutingDatabaseError("update group preset protocol policy", err)
		}
		groupID, _, err := mirrorPresetToActiveGroupTx(tx, &preset)
		if err != nil {
			return protocolRoutingDatabaseError("mirror active group preset protocol policy", err)
		}
		mirrorGroupID = groupID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if mirrorGroupID > 0 {
		if err := groupRefreshCacheByID(mirrorGroupID, ctx); err != nil {
			return nil, protocolRoutingDatabaseError("refresh active preset group cache", err)
		}
	}
	return state, nil
}

func requireProtocolPolicyGroup(conn *gorm.DB, groupID int) error {
	if groupID <= 0 {
		return protocolRoutingValidationError("group id must be positive")
	}
	var count int64
	if err := conn.Model(&model.Group{}).Where("id = ?", groupID).Count(&count).Error; err != nil {
		return protocolRoutingDatabaseError("load protocol policy group", err)
	}
	if count != 1 {
		return protocolRoutingNotFoundError("group not found")
	}
	return nil
}
