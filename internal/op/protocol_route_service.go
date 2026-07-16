package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type protocolPolicyMutation func(tx *gorm.DB, cfg *model.ProtocolRoutingConfig, nextRevision int64) error

// ProtocolRoutingConfigUpdate applies a partial global update using revision CAS.
func ProtocolRoutingConfigUpdate(req *model.ProtocolRoutingConfigUpdateRequest, actor string, ctx context.Context) (*model.ProtocolPolicyState, error) {
	if req == nil {
		return nil, protocolRoutingValidationError("protocol routing config request is nil")
	}
	if req.ExpectedRevision < 0 {
		return nil, protocolRoutingValidationError("expected_revision must not be negative")
	}
	state, err := commitProtocolPolicyMutation(ctx, req.ExpectedRevision, actor, func(tx *gorm.DB, cfg *model.ProtocolRoutingConfig, _ int64) error {
		applyProtocolConfigPatch(cfg, req)
		if err := validateProtocolRoutingConfig(tx, cfg); err != nil {
			return err
		}
		return tx.Model(&model.ProtocolRoutingConfig{}).Where("id = ?", cfg.ID).Select(
			"protocol_routing_enabled",
			"mode",
			"protocol_fallback_enabled",
			"protocol_learning_read_enabled",
			"protocol_learning_write_enabled",
			"protocol_conversion_enabled",
			"adaptive_group_allowlist",
			"ranking_signal_order",
			"legacy_site_route_learning_enabled",
		).Updates(cfg).Error
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

func commitProtocolPolicyMutation(ctx context.Context, expectedRevision int64, actor string, mutate protocolPolicyMutation) (*model.ProtocolPolicyState, error) {
	tx := db.GetDB().WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, protocolRoutingDatabaseError("begin protocol policy transaction", tx.Error)
	}
	defer tx.Rollback()

	var cfg model.ProtocolRoutingConfig
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cfg, 1).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, protocolRoutingNotFoundError("protocol routing config not found")
		}
		return nil, protocolRoutingDatabaseError("lock protocol routing config", err)
	}
	if cfg.ActiveRevision != expectedRevision {
		return nil, protocolRoutingConflictError(expectedRevision, cfg.ActiveRevision)
	}
	nextRevision := expectedRevision + 1
	if err := mutate(tx, &cfg, nextRevision); err != nil {
		return nil, err
	}

	payload, err := loadProtocolPolicyProjection(tx)
	if err != nil {
		return nil, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, protocolRoutingDatabaseError("encode protocol policy payload", err)
	}
	sum := sha256.Sum256(payloadJSON)
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "unknown"
	}
	revision := model.ProtocolPolicyRevision{
		Revision:       nextRevision,
		ParentRevision: expectedRevision,
		SchemaVersion:  payload.SchemaVersion,
		PayloadJSON:    string(payloadJSON),
		PayloadSHA256:  hex.EncodeToString(sum[:]),
		Actor:          actor,
		CreatedAt:      time.Now().UTC(),
	}
	if err := tx.Create(&revision).Error; err != nil {
		return nil, protocolRoutingDatabaseError("create protocol policy revision", err)
	}
	result := tx.Model(&model.ProtocolRoutingConfig{}).
		Where("id = ? AND active_revision = ?", cfg.ID, expectedRevision).
		Updates(map[string]any{"active_revision": nextRevision, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return nil, protocolRoutingDatabaseError("activate protocol policy revision", result.Error)
	}
	if result.RowsAffected != 1 {
		var current model.ProtocolRoutingConfig
		if err := tx.First(&current, cfg.ID).Error; err != nil {
			return nil, protocolRoutingDatabaseError("reload protocol policy revision", err)
		}
		return nil, protocolRoutingConflictError(expectedRevision, current.ActiveRevision)
	}
	if err := tx.Commit().Error; err != nil {
		return nil, protocolRoutingDatabaseError("commit protocol policy transaction", err)
	}
	state, err := ProtocolPolicyGet(ctx)
	if err != nil {
		return nil, err
	}
	protocolPolicyRuntimeStore(state)
	return state, nil
}

func applyProtocolConfigPatch(cfg *model.ProtocolRoutingConfig, req *model.ProtocolRoutingConfigUpdateRequest) {
	if req.ProtocolRoutingEnabled != nil {
		cfg.ProtocolRoutingEnabled = *req.ProtocolRoutingEnabled
	}
	if req.Mode != nil {
		cfg.Mode = *req.Mode
	}
	if req.ProtocolFallbackEnabled != nil {
		cfg.ProtocolFallbackEnabled = *req.ProtocolFallbackEnabled
	}
	if req.ProtocolLearningReadEnabled != nil {
		cfg.ProtocolLearningReadEnabled = *req.ProtocolLearningReadEnabled
	}
	if req.ProtocolLearningWriteEnabled != nil {
		cfg.ProtocolLearningWriteEnabled = *req.ProtocolLearningWriteEnabled
	}
	if req.ProtocolConversionEnabled != nil {
		cfg.ProtocolConversionEnabled = *req.ProtocolConversionEnabled
	}
	if req.AdaptiveGroupAllowlist != nil {
		cfg.AdaptiveGroupAllowlist = append([]int(nil), (*req.AdaptiveGroupAllowlist)...)
	}
	if req.RankingSignalOrder != nil {
		cfg.RankingSignalOrder = append([]string(nil), (*req.RankingSignalOrder)...)
	}
	if req.LegacySiteRouteLearningEnabled != nil {
		cfg.LegacySiteRouteLearningEnabled = *req.LegacySiteRouteLearningEnabled
	}
}

func validateProtocolRoutingConfig(tx *gorm.DB, cfg *model.ProtocolRoutingConfig) error {
	switch cfg.Mode {
	case model.ProtocolRoutingModeLegacy, model.ProtocolRoutingModeObserve, model.ProtocolRoutingModeAdaptive:
	default:
		return protocolRoutingValidationError("invalid protocol routing mode")
	}
	if cfg.ProtocolLearningReadEnabled || cfg.ProtocolLearningWriteEnabled {
		return protocolRoutingValidationError("protocol learning is not available in v0.11")
	}
	if cfg.ProtocolFallbackEnabled {
		return protocolRoutingValidationError("automatic protocol fallback is not available in v0.11")
	}
	if cfg.Mode == model.ProtocolRoutingModeAdaptive && !cfg.ProtocolRoutingEnabled {
		return protocolRoutingValidationError("adaptive mode requires protocol routing to be enabled")
	}
	if err := validateRankingSignalOrder(cfg.RankingSignalOrder); err != nil {
		return err
	}
	return validateAdaptiveGroupAllowlist(tx, cfg.AdaptiveGroupAllowlist)
}

func validateRankingSignalOrder(order []string) error {
	if len(order) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(defaultProtocolRankingSignals))
	for _, signal := range defaultProtocolRankingSignals {
		allowed[signal] = struct{}{}
	}
	seen := make(map[string]struct{}, len(order))
	for _, signal := range order {
		if _, ok := allowed[signal]; !ok {
			return protocolRoutingValidationError("unknown ranking signal: " + signal)
		}
		if _, ok := seen[signal]; ok {
			return protocolRoutingValidationError("duplicate ranking signal: " + signal)
		}
		seen[signal] = struct{}{}
	}
	return nil
}

func validateAdaptiveGroupAllowlist(tx *gorm.DB, groupIDs []int) error {
	if len(groupIDs) == 0 {
		return nil
	}
	ids := append([]int(nil), groupIDs...)
	sort.Ints(ids)
	for index, groupID := range ids {
		if groupID <= 0 {
			return protocolRoutingValidationError("adaptive group id must be positive")
		}
		if index > 0 && ids[index-1] == groupID {
			return protocolRoutingValidationError("duplicate adaptive group id")
		}
		var groupCount int64
		if err := tx.Model(&model.Group{}).Where("id = ?", groupID).Count(&groupCount).Error; err != nil {
			return protocolRoutingDatabaseError("validate adaptive group", err)
		}
		if groupCount != 1 {
			return protocolRoutingNotFoundError("adaptive group not found")
		}
		var managedCount int64
		if err := tx.Table("group_items AS gi").
			Joins("JOIN site_channel_bindings AS scb ON scb.channel_id = gi.channel_id").
			Where("gi.group_id = ?", groupID).
			Count(&managedCount).Error; err != nil {
			return protocolRoutingDatabaseError("validate adaptive group ownership", err)
		}
		if managedCount > 0 {
			return protocolRoutingValidationError("adaptive groups must contain only unmanaged channels")
		}
	}
	cfgIDs := append([]int(nil), ids...)
	copy(groupIDs, cfgIDs)
	return nil
}
