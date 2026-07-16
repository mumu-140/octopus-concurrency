package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

var defaultProtocolRankingSignals = []string{
	"ingress",
	"group_prefer",
	"evidence",
	"key_model_prefer",
	"channel_model_prefer",
	"channel_type",
	"metadata_hint",
}

// ProtocolPolicyGet returns the active immutable revision payload.
func ProtocolPolicyGet(ctx context.Context) (*model.ProtocolPolicyState, error) {
	return protocolPolicyGetDB(db.GetDB().WithContext(ctx))
}

func protocolPolicyGetDB(conn *gorm.DB) (*model.ProtocolPolicyState, error) {
	var cfg model.ProtocolRoutingConfig
	if err := conn.First(&cfg, 1).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, protocolRoutingNotFoundError("protocol routing config not found")
		}
		return nil, protocolRoutingDatabaseError("load protocol routing config", err)
	}
	if cfg.ActiveRevision == 0 {
		payload, err := loadProtocolPolicyProjection(conn)
		if err != nil {
			return nil, err
		}
		return &model.ProtocolPolicyState{ActiveRevision: 0, ProtocolPolicyPayload: *payload}, nil
	}

	var revision model.ProtocolPolicyRevision
	if err := conn.First(&revision, "revision = ?", cfg.ActiveRevision).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load active protocol policy revision", err)
	}
	sum := sha256.Sum256([]byte(revision.PayloadJSON))
	if revision.PayloadSHA256 != hex.EncodeToString(sum[:]) {
		return nil, protocolRoutingDatabaseError("active protocol policy payload hash mismatch", fmt.Errorf("revision %d", cfg.ActiveRevision))
	}
	var payload model.ProtocolPolicyPayload
	if err := json.Unmarshal([]byte(revision.PayloadJSON), &payload); err != nil {
		return nil, protocolRoutingDatabaseError("decode active protocol policy revision", err)
	}
	if payload.SchemaVersion != 1 {
		return nil, protocolRoutingDatabaseError("unsupported protocol policy schema", fmt.Errorf("schema %d", payload.SchemaVersion))
	}
	normalizeProtocolPolicyPayload(&payload)
	return &model.ProtocolPolicyState{ActiveRevision: cfg.ActiveRevision, ProtocolPolicyPayload: payload}, nil
}

func loadProtocolPolicyProjection(conn *gorm.DB) (*model.ProtocolPolicyPayload, error) {
	var cfg model.ProtocolRoutingConfig
	if err := conn.First(&cfg, 1).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load protocol routing projection", err)
	}
	payload := &model.ProtocolPolicyPayload{
		SchemaVersion: 1,
		Config:        protocolConfigPolicyFromModel(cfg),
		Channels:      []model.ChannelProtocolPolicy{},
		Groups:        []model.GroupProtocolPolicy{},
		GroupPresets:  []model.GroupPresetProtocolPolicy{},
	}

	var profiles []model.ChannelProtocolProfile
	if err := conn.Order("channel_id ASC, protocol ASC").Find(&profiles).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load channel protocol profiles", err)
	}
	var overrides []model.ChannelModelProtocolOverride
	if err := conn.Order("channel_id ASC, channel_key_id ASC, upstream_model ASC").Find(&overrides).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load model protocol overrides", err)
	}
	channelIndex := make(map[int]int)
	ensureChannel := func(channelID int) *model.ChannelProtocolPolicy {
		if index, ok := channelIndex[channelID]; ok {
			return &payload.Channels[index]
		}
		payload.Channels = append(payload.Channels, model.ChannelProtocolPolicy{
			ChannelID: channelID,
			Profiles:  []model.ProtocolProfilePolicy{},
			Overrides: []model.ModelProtocolOverridePolicy{},
		})
		index := len(payload.Channels) - 1
		channelIndex[channelID] = index
		return &payload.Channels[index]
	}
	for _, profile := range profiles {
		channel := ensureChannel(profile.ChannelID)
		channel.Profiles = append(channel.Profiles, model.ProtocolProfilePolicy{
			Protocol:      profile.Protocol,
			Enabled:       profile.Enabled,
			BaseUrls:      profile.BaseUrls,
			CustomHeaders: profile.CustomHeaders,
			ParamOverride: profile.ParamOverride,
		})
	}
	for _, override := range overrides {
		channel := ensureChannel(override.ChannelID)
		channel.Overrides = append(channel.Overrides, model.ModelProtocolOverridePolicy{
			ChannelKeyID:       override.ChannelKeyID,
			UpstreamModel:      override.UpstreamModel,
			Mode:               override.Mode,
			PreferredProtocols: override.PreferredProtocols,
			Enabled:            override.Enabled,
		})
	}

	var groups []model.Group
	if err := conn.Select("id", "protocol_mode", "preferred_protocols").Order("id ASC").Find(&groups).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load group protocol policies", err)
	}
	for _, group := range groups {
		payload.Groups = append(payload.Groups, model.GroupProtocolPolicy{
			GroupID:            group.ID,
			Mode:               group.ProtocolMode,
			PreferredProtocols: group.PreferredProtocols,
		})
	}
	var presets []model.GroupPreset
	if err := conn.Select("id", "protocol_mode", "preferred_protocols").Order("id ASC").Find(&presets).Error; err != nil {
		return nil, protocolRoutingDatabaseError("load group preset protocol policies", err)
	}
	for _, preset := range presets {
		payload.GroupPresets = append(payload.GroupPresets, model.GroupPresetProtocolPolicy{
			GroupPresetID:      preset.ID,
			Mode:               preset.ProtocolMode,
			PreferredProtocols: preset.PreferredProtocols,
		})
	}
	normalizeProtocolPolicyPayload(payload)
	return payload, nil
}

func protocolConfigPolicyFromModel(cfg model.ProtocolRoutingConfig) model.ProtocolRoutingConfigPolicy {
	return model.ProtocolRoutingConfigPolicy{
		ProtocolRoutingEnabled:         cfg.ProtocolRoutingEnabled,
		Mode:                           cfg.Mode,
		ProtocolFallbackEnabled:        cfg.ProtocolFallbackEnabled,
		ProtocolLearningReadEnabled:    cfg.ProtocolLearningReadEnabled,
		ProtocolLearningWriteEnabled:   cfg.ProtocolLearningWriteEnabled,
		ProtocolConversionEnabled:      cfg.ProtocolConversionEnabled,
		AdaptiveGroupAllowlist:         append([]int(nil), cfg.AdaptiveGroupAllowlist...),
		RankingSignalOrder:             append([]string(nil), cfg.RankingSignalOrder...),
		LegacySiteRouteLearningEnabled: cfg.LegacySiteRouteLearningEnabled,
	}
}

func normalizeProtocolPolicyPayload(payload *model.ProtocolPolicyPayload) {
	if payload.Config.AdaptiveGroupAllowlist == nil {
		payload.Config.AdaptiveGroupAllowlist = []int{}
	}
	if len(payload.Config.RankingSignalOrder) == 0 {
		payload.Config.RankingSignalOrder = append([]string(nil), defaultProtocolRankingSignals...)
	}
	if payload.Channels == nil {
		payload.Channels = []model.ChannelProtocolPolicy{}
	}
	if payload.Groups == nil {
		payload.Groups = []model.GroupProtocolPolicy{}
	}
	if payload.GroupPresets == nil {
		payload.GroupPresets = []model.GroupPresetProtocolPolicy{}
	}
}
