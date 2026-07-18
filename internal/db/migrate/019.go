package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 2026071801,
		Up:      migrateGroupProtocolFallback,
	})
}

func migrateGroupProtocolFallback(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if err := resetGroupProtocolPolicyTable(db, "groups"); err != nil {
		return err
	}
	if err := resetGroupProtocolPolicyTable(db, "group_presets"); err != nil {
		return err
	}
	if db.Migrator().HasTable(&model.ProtocolRoutingConfig{}) {
		if err := db.Model(&model.ProtocolRoutingConfig{}).Where("id = ?", 1).Updates(map[string]any{
			"protocol_routing_enabled":        false,
			"mode":                            model.ProtocolRoutingModeLegacy,
			"protocol_fallback_enabled":       false,
			"protocol_learning_read_enabled":  false,
			"protocol_learning_write_enabled": false,
			"protocol_conversion_enabled":     false,
			"adaptive_group_allowlist":        gorm.Expr("NULL"),
			"ranking_signal_order":            gorm.Expr("NULL"),
		}).Error; err != nil {
			return fmt.Errorf("reset protocol routing switch: %w", err)
		}
	}
	return nil
}

func resetGroupProtocolPolicyTable(db *gorm.DB, table string) error {
	if !db.Migrator().HasTable(table) {
		return nil
	}
	if err := db.Table(table).Where("1 = 1").Updates(map[string]any{
		"protocol_mode":       string(model.ProtocolPolicyModeFollow),
		"preferred_protocols": "[]",
	}).Error; err != nil {
		return fmt.Errorf("reset %s protocol policy: %w", table, err)
	}
	return nil
}
