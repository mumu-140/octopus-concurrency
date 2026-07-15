package migrate

import (
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"gorm.io/gorm"
)

// 阶段 B 列迁移（设计 §10.2-§10.5）：
//   - Channel.model_discovery_protocol
//   - Group / GroupPreset.protocol_mode / preferred_protocols / policy_revision
//   - ChannelKey.credential_revision
//   - 播种 ProtocolRoutingConfig 单例（默认 legacy）
//
// 只做 ADD COLUMN + 回填，绝不触发 glebarez/sqlite 的 AlterColumn→recreateTable
// 全表拷贝（沿用 015/016/017 的 HasColumn+AddColumn 幂等惯例）。
func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 2026071601,
		Up:      migrateProtocolRouteStageB,
	})
}

// outboundTypeToProtocolString 把整数 Channel.Type 映射为协议字符串。
// 与 internal/protocol 的映射一致，但 model/migrate 是叶子层，这里用本地常量表，
// 不反向依赖 protocol 包。
func outboundTypeToProtocolString(t outbound.OutboundType) string {
	switch t {
	case outbound.OutboundTypeOpenAIChat:
		return "openai_chat"
	case outbound.OutboundTypeOpenAIResponse:
		return "openai_response"
	case outbound.OutboundTypeAnthropic:
		return "anthropic"
	case outbound.OutboundTypeGemini:
		return "gemini"
	case outbound.OutboundTypeVolcengine:
		return "volcengine"
	case outbound.OutboundTypeOpenAIEmbedding:
		return "openai_embedding"
	default:
		return "openai_chat"
	}
}

func migrateProtocolRouteStageB(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// 1) Channel.model_discovery_protocol
	if db.Migrator().HasTable(&model.Channel{}) {
		if !db.Migrator().HasColumn(&model.Channel{}, "model_discovery_protocol") {
			if err := db.Migrator().AddColumn(&model.Channel{}, "ModelDiscoveryProtocol"); err != nil {
				return fmt.Errorf("add channels.model_discovery_protocol: %w", err)
			}
		}
		// 回填：按现有 Channel.Type 无条件映射协议字符串。
		// 无条件的原因：正常启动流程里 AutoMigrate 先于本迁移把列建好，
		// 存量行全部带上 tag 默认占位值 'openai_chat'（不是 NULL/空），
		// "只填空值"会漏掉全部 type≠openai_chat 的渠道；且本迁移由
		// MigrationRecord 门控只成功一次、失败会阻止服务启动，运行时
		// 不存在需要保留的人工编辑。
		var chans []struct {
			ID   int
			Type outbound.OutboundType
		}
		if err := db.Table("channels").Select("id, type").Find(&chans).Error; err != nil {
			return fmt.Errorf("read channels for discovery protocol backfill: %w", err)
		}
		for _, ch := range chans {
			proto := outboundTypeToProtocolString(ch.Type)
			if err := db.Table("channels").Where("id = ?", ch.ID).
				Update("model_discovery_protocol", proto).Error; err != nil {
				return fmt.Errorf("backfill channels.model_discovery_protocol id=%d: %w", ch.ID, err)
			}
		}
	}

	// 2) ChannelKey.credential_revision（新 Key 从 1 开始）
	if db.Migrator().HasTable(&model.ChannelKey{}) {
		if !db.Migrator().HasColumn(&model.ChannelKey{}, "credential_revision") {
			if err := db.Migrator().AddColumn(&model.ChannelKey{}, "CredentialRevision"); err != nil {
				return fmt.Errorf("add channel_keys.credential_revision: %w", err)
			}
		}
		if err := db.Model(&model.ChannelKey{}).
			Where("credential_revision IS NULL OR credential_revision < 1").
			Update("credential_revision", 1).Error; err != nil {
			return fmt.Errorf("backfill channel_keys.credential_revision: %w", err)
		}
	}

	// 3) Group 策略列
	if err := ensureProtocolPolicyColumns(db, &model.Group{}, "groups"); err != nil {
		return err
	}
	// 4) GroupPreset 策略列
	if err := ensureProtocolPolicyColumns(db, &model.GroupPreset{}, "group_presets"); err != nil {
		return err
	}

	// 5) 播种 ProtocolRoutingConfig 单例（默认 legacy；legacy_site_route_learning_enabled
	//    升级时以 true 初始化，保留旧 managed-site 学习行为）。
	if db.Migrator().HasTable(&model.ProtocolRoutingConfig{}) {
		var count int64
		if err := db.Model(&model.ProtocolRoutingConfig{}).Count(&count).Error; err != nil {
			return fmt.Errorf("count protocol_routing_config: %w", err)
		}
		if count == 0 {
			cfg := model.ProtocolRoutingConfig{
				ID:                             1,
				ActiveRevision:                 0,
				ProtocolRoutingEnabled:         false,
				Mode:                           model.ProtocolRoutingModeLegacy,
				ProtocolFallbackEnabled:        false,
				ProtocolLearningReadEnabled:    false,
				ProtocolLearningWriteEnabled:   false,
				ProtocolConversionEnabled:      false,
				LegacySiteRouteLearningEnabled: true,
				UpdatedAt:                      time.Now().UTC(),
			}
			if err := db.Create(&cfg).Error; err != nil {
				return fmt.Errorf("seed protocol_routing_config: %w", err)
			}
		}
	}

	return nil
}

// ensureProtocolPolicyColumns 给 groups / group_presets 补齐三个策略列并回填默认值。
func ensureProtocolPolicyColumns(db *gorm.DB, m interface{}, table string) error {
	if !db.Migrator().HasTable(m) {
		return nil
	}
	cols := map[string]string{
		"protocol_mode":       "ProtocolMode",
		"preferred_protocols": "PreferredProtocols",
		"policy_revision":     "PolicyRevision",
	}
	for dbCol, field := range cols {
		if !db.Migrator().HasColumn(m, dbCol) {
			if err := db.Migrator().AddColumn(m, field); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, dbCol, err)
			}
		}
	}
	// 回填 protocol_mode 默认 inherit（空值/NULL）
	if err := db.Table(table).
		Where("protocol_mode IS NULL OR protocol_mode = ?", "").
		Update("protocol_mode", string(model.ProtocolPolicyModeInherit)).Error; err != nil {
		return fmt.Errorf("backfill %s.protocol_mode: %w", table, err)
	}
	return nil
}
