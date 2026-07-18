package migrate

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMigrateGroupProtocolFallbackResetsPolicyAndKeepsLegacyTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Group{},
		&model.GroupPreset{},
		&model.ProtocolRoutingConfig{},
		&model.ProtocolPolicyRevision{},
		&model.ChannelProtocolProfile{},
		&model.ChannelModelProtocolOverride{},
	); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	group := model.Group{
		Name:               "legacy-group",
		Mode:               model.GroupModeFailover,
		ProtocolMode:       model.ProtocolPolicyModeForce,
		PreferredProtocols: []string{"anthropic", "openai_response"},
		PolicyRevision:     4,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	preset := model.GroupPreset{
		GroupID:            group.ID,
		Name:               "legacy-preset",
		Mode:               model.GroupModeFailover,
		ProtocolMode:       model.ProtocolPolicyModePrefer,
		PreferredProtocols: []string{"openai_response"},
		PolicyRevision:     4,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	config := model.ProtocolRoutingConfig{
		ID:                        1,
		ActiveRevision:            4,
		ProtocolRoutingEnabled:    true,
		Mode:                      model.ProtocolRoutingModeAdaptive,
		ProtocolFallbackEnabled:   true,
		ProtocolConversionEnabled: true,
		AdaptiveGroupAllowlist:    []int{group.ID},
		RankingSignalOrder:        []string{"ingress", "group_prefer"},
		UpdatedAt:                 time.Now(),
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create config: %v", err)
	}
	revision := model.ProtocolPolicyRevision{
		Revision:       4,
		ParentRevision: 3,
		SchemaVersion:  1,
		PayloadJSON:    `{"schema_version":1}`,
		PayloadSHA256:  "deadbeef",
		Actor:          "test",
		CreatedAt:      time.Now(),
	}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	profile := model.ChannelProtocolProfile{
		ChannelID:      7,
		Protocol:       "anthropic",
		Enabled:        true,
		PolicyRevision: 4,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	override := model.ChannelModelProtocolOverride{
		ChannelID:          7,
		UpstreamModel:      "upstream-model",
		Mode:               model.ProtocolPolicyModeForce,
		PreferredProtocols: []string{"anthropic"},
		Enabled:            true,
		PolicyRevision:     4,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := db.Create(&override).Error; err != nil {
		t.Fatalf("create override: %v", err)
	}

	if err := migrateGroupProtocolFallback(db); err != nil {
		t.Fatalf("first migration failed: %v", err)
	}
	if err := migrateGroupProtocolFallback(db); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}

	var gotGroup model.Group
	if err := db.First(&gotGroup, group.ID).Error; err != nil {
		t.Fatalf("load group: %v", err)
	}
	if gotGroup.ProtocolMode != model.ProtocolPolicyModeFollow || len(gotGroup.PreferredProtocols) != 0 {
		t.Fatalf("group policy = %q %v", gotGroup.ProtocolMode, gotGroup.PreferredProtocols)
	}
	var gotPreset model.GroupPreset
	if err := db.First(&gotPreset, preset.ID).Error; err != nil {
		t.Fatalf("load preset: %v", err)
	}
	if gotPreset.ProtocolMode != model.ProtocolPolicyModeFollow || len(gotPreset.PreferredProtocols) != 0 {
		t.Fatalf("preset policy = %q %v", gotPreset.ProtocolMode, gotPreset.PreferredProtocols)
	}
	var gotConfig model.ProtocolRoutingConfig
	if err := db.First(&gotConfig, 1).Error; err != nil {
		t.Fatalf("load config: %v", err)
	}
	if gotConfig.ProtocolRoutingEnabled || gotConfig.Mode != model.ProtocolRoutingModeLegacy || gotConfig.ProtocolFallbackEnabled {
		t.Fatalf("config switch not reset: %+v", gotConfig)
	}

	for _, name := range []string{
		"protocol_policy_revisions",
		"channel_protocol_profiles",
		"channel_model_protocol_overrides",
	} {
		if !db.Migrator().HasTable(name) {
			t.Fatalf("legacy table %s was dropped", name)
		}
	}
	var revisionCount, profileCount, overrideCount int64
	db.Model(&model.ProtocolPolicyRevision{}).Count(&revisionCount)
	db.Model(&model.ChannelProtocolProfile{}).Count(&profileCount)
	db.Model(&model.ChannelModelProtocolOverride{}).Count(&overrideCount)
	if revisionCount != 1 || profileCount != 1 || overrideCount != 1 {
		t.Fatalf("legacy rows deleted: revision=%d profile=%d override=%d", revisionCount, profileCount, overrideCount)
	}
}
