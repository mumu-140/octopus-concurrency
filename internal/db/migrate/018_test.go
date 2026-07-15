package migrate

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newStageBTestDB 构造"旧 schema"数据库：表存在但没有阶段 B 新列，
// 模拟 v0.10.x 数据库升级到 v0.11 的真实路径。
func newStageBTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 用裸 SQL 建旧表（不含新列），避免直接 AutoMigrate 当前 model 把新列一并建出来。
	stmts := []string{
		`CREATE TABLE channels (
			id integer PRIMARY KEY AUTOINCREMENT,
			name text NOT NULL UNIQUE,
			type integer,
			enabled numeric DEFAULT true
		)`,
		`CREATE TABLE channel_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			channel_id integer,
			enabled numeric DEFAULT true,
			channel_key text,
			status_code integer,
			last_use_time_stamp integer,
			total_cost real,
			remark text
		)`,
		`CREATE TABLE groups (
			id integer PRIMARY KEY AUTOINCREMENT,
			name text NOT NULL UNIQUE,
			mode integer NOT NULL
		)`,
		`CREATE TABLE group_presets (
			id integer PRIMARY KEY AUTOINCREMENT,
			group_id integer NOT NULL,
			name text NOT NULL,
			mode integer NOT NULL,
			items text,
			created_at datetime,
			updated_at datetime
		)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}
	// 新表由 AutoMigrate 建（对应 db.go 的 models 列表新增项）
	if err := db.AutoMigrate(&model.ProtocolRoutingConfig{}); err != nil {
		t.Fatalf("AutoMigrate ProtocolRoutingConfig: %v", err)
	}
	return db
}

func TestMigrateProtocolRouteStageB(t *testing.T) {
	db := newStageBTestDB(t)

	// 播种旧数据
	if err := db.Exec(`INSERT INTO channels (name, type) VALUES
		('chat-ch', 0), ('resp-ch', 1), ('anthropic-ch', 2), ('gemini-ch', 3)`).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := db.Exec(`INSERT INTO channel_keys (channel_id, channel_key) VALUES (1,'k1'),(1,'k2'),(2,'k3')`).Error; err != nil {
		t.Fatalf("seed keys: %v", err)
	}
	if err := db.Exec(`INSERT INTO groups (name, mode) VALUES ('g1', 1)`).Error; err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	if err := db.Exec(`INSERT INTO group_presets (group_id, name, mode, items) VALUES (1,'p1',1,'[]')`).Error; err != nil {
		t.Fatalf("seed group_presets: %v", err)
	}

	if err := migrateProtocolRouteStageB(db); err != nil {
		t.Fatalf("migrateProtocolRouteStageB: %v", err)
	}

	// 1) Channel.model_discovery_protocol 按 Type 回填
	type chRow struct {
		Name                   string
		ModelDiscoveryProtocol string
	}
	var chans []chRow
	if err := db.Table("channels").Select("name, model_discovery_protocol").Order("id").Find(&chans).Error; err != nil {
		t.Fatalf("read channels: %v", err)
	}
	want := map[string]string{
		"chat-ch":      "openai_chat",
		"resp-ch":      "openai_response",
		"anthropic-ch": "anthropic",
		"gemini-ch":    "gemini",
	}
	for _, c := range chans {
		if want[c.Name] != c.ModelDiscoveryProtocol {
			t.Errorf("channel %s discovery protocol = %q, want %q", c.Name, c.ModelDiscoveryProtocol, want[c.Name])
		}
	}

	// 2) ChannelKey.credential_revision 回填为 1
	var minRev, maxRev int
	if err := db.Table("channel_keys").Select("MIN(credential_revision)").Scan(&minRev).Error; err != nil {
		t.Fatalf("read min credential_revision: %v", err)
	}
	if err := db.Table("channel_keys").Select("MAX(credential_revision)").Scan(&maxRev).Error; err != nil {
		t.Fatalf("read max credential_revision: %v", err)
	}
	if minRev != 1 || maxRev != 1 {
		t.Errorf("credential_revision range = [%d,%d], want [1,1]", minRev, maxRev)
	}

	// 3) Group / GroupPreset protocol_mode 回填 inherit
	var gMode, pMode string
	if err := db.Table("groups").Select("protocol_mode").Where("id = 1").Scan(&gMode).Error; err != nil {
		t.Fatalf("read group protocol_mode: %v", err)
	}
	if err := db.Table("group_presets").Select("protocol_mode").Where("id = 1").Scan(&pMode).Error; err != nil {
		t.Fatalf("read preset protocol_mode: %v", err)
	}
	if gMode != string(model.ProtocolPolicyModeInherit) || pMode != string(model.ProtocolPolicyModeInherit) {
		t.Errorf("protocol_mode = (%q, %q), want inherit", gMode, pMode)
	}

	// 4) ProtocolRoutingConfig 单例播种：默认 legacy、路由关闭、旧学习保留
	var cfg model.ProtocolRoutingConfig
	if err := db.First(&cfg, 1).Error; err != nil {
		t.Fatalf("read protocol_routing_config: %v", err)
	}
	if cfg.Mode != model.ProtocolRoutingModeLegacy {
		t.Errorf("config mode = %q, want legacy", cfg.Mode)
	}
	if cfg.ProtocolRoutingEnabled || cfg.ProtocolFallbackEnabled || cfg.ProtocolLearningReadEnabled ||
		cfg.ProtocolLearningWriteEnabled || cfg.ProtocolConversionEnabled {
		t.Errorf("config switches should all default false: %+v", cfg)
	}
	if !cfg.LegacySiteRouteLearningEnabled {
		t.Errorf("legacy_site_route_learning_enabled should default true on upgrade")
	}
	if cfg.ActiveRevision != 0 {
		t.Errorf("active_revision = %d, want 0", cfg.ActiveRevision)
	}
}

func TestMigrateProtocolRouteStageBIdempotent(t *testing.T) {
	db := newStageBTestDB(t)
	if err := db.Exec(`INSERT INTO channels (name, type) VALUES ('c', 2)`).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Exec(`INSERT INTO channel_keys (channel_id, channel_key) VALUES (1,'k')`).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := migrateProtocolRouteStageB(db); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	// 单例不重复播种
	var count int64
	if err := db.Model(&model.ProtocolRoutingConfig{}).Count(&count).Error; err != nil {
		t.Fatalf("count config: %v", err)
	}
	if count != 1 {
		t.Errorf("config rows = %d, want 1 (idempotent seed)", count)
	}

	// 收敛性：函数级重跑总是按 Channel.Type 重新推导（本迁移由
	// MigrationRecord 门控只成功一次；运行期人工编辑发生在成功之后，
	// 不会再经过本函数）。渠道 type=2(anthropic)，任何脏值重跑后收敛。
	if err := db.Exec(`UPDATE channels SET model_discovery_protocol = 'openai_response' WHERE id = 1`).Error; err != nil {
		t.Fatalf("manual update: %v", err)
	}
	if err := migrateProtocolRouteStageB(db); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	var proto string
	if err := db.Table("channels").Select("model_discovery_protocol").Where("id = 1").Scan(&proto).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if proto != "anthropic" {
		t.Errorf("re-run did not converge to type-derived value: got %q, want anthropic", proto)
	}
}

func TestOutboundTypeToProtocolStringMapping(t *testing.T) {
	cases := map[outbound.OutboundType]string{
		outbound.OutboundTypeOpenAIChat:      "openai_chat",
		outbound.OutboundTypeOpenAIResponse:  "openai_response",
		outbound.OutboundTypeAnthropic:       "anthropic",
		outbound.OutboundTypeGemini:          "gemini",
		outbound.OutboundTypeVolcengine:      "volcengine",
		outbound.OutboundTypeOpenAIEmbedding: "openai_embedding",
		outbound.OutboundType(99):            "openai_chat", // 未知类型回退
	}
	for in, want := range cases {
		if got := outboundTypeToProtocolString(in); got != want {
			t.Errorf("outboundTypeToProtocolString(%d) = %q, want %q", in, got, want)
		}
	}
}
