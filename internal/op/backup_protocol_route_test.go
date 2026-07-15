package op

import (
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// TestBackupRoundtripProtocolRouteTables 验证 T09 新增的策略表和 GroupPreset
// 进入 JSON 备份并可恢复到干净库（tasks.md T09 完成标准）。
func TestBackupRoundtripProtocolRouteTables(t *testing.T) {
	ctx := setupBackupTestDB(t)
	conn := dbpkg.GetDB()

	// --- 源库播种 ---
	ch := model.Channel{Name: "rt-ch", Type: 2} // anthropic
	if err := conn.Create(&ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: ch.ID, ChannelKey: "sk-rt", CredentialRevision: 1}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	grp := model.Group{Name: "rt-group", Mode: model.GroupModeFailover,
		ProtocolMode: model.ProtocolPolicyModePrefer, PreferredProtocols: []string{"anthropic", "openai_chat"}}
	if err := conn.Create(&grp).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	preset := model.GroupPreset{GroupID: grp.ID, Name: "rt-preset", Mode: model.GroupModeFailover,
		ProtocolMode: model.ProtocolPolicyModeForce, PreferredProtocols: []string{"anthropic"},
		Items: []model.GroupPresetItem{{ChannelID: ch.ID, ModelName: "claude-x", Priority: 1, Weight: 1}}}
	if err := conn.Create(&preset).Error; err != nil {
		t.Fatalf("seed preset: %v", err)
	}
	rev := model.ProtocolPolicyRevision{Revision: 1, PayloadJSON: `{"schema":1}`, PayloadSHA256: "abc", Actor: "test"}
	if err := conn.Create(&rev).Error; err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	override := model.ChannelModelProtocolOverride{ChannelID: ch.ID, ChannelKeyID: key.ID,
		UpstreamModel: "claude-x", Mode: model.ProtocolPolicyModeForce, PreferredProtocols: []string{"anthropic"},
		Enabled: true, PolicyRevision: rev.Revision}
	if err := conn.Create(&override).Error; err != nil {
		t.Fatalf("seed override: %v", err)
	}
	profile := model.ChannelProtocolProfile{ChannelID: ch.ID, Protocol: "anthropic",
		Enabled: true, PolicyRevision: rev.Revision}
	if err := conn.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := conn.Model(&model.ProtocolRoutingConfig{}).Where("id = 1").
		Updates(map[string]any{"mode": model.ProtocolRoutingModeObserve, "active_revision": rev.Revision}).Error; err != nil {
		// 单例可能不存在（测试库跳过 migration seed），手动建
		cfg := model.ProtocolRoutingConfig{ID: 1, Mode: model.ProtocolRoutingModeObserve, ActiveRevision: rev.Revision,
			LegacySiteRouteLearningEnabled: true}
		if err2 := conn.Create(&cfg).Error; err2 != nil {
			t.Fatalf("seed config: %v / %v", err, err2)
		}
	}

	// --- 导出 ---
	dump, err := DBExportAll(ctx, false, false)
	if err != nil {
		t.Fatalf("DBExportAll: %v", err)
	}
	if len(dump.GroupPresets) != 1 {
		t.Fatalf("dump.GroupPresets = %d, want 1", len(dump.GroupPresets))
	}
	if len(dump.ProtocolPolicyRevisions) != 1 || len(dump.ChannelModelProtocolOverrides) != 1 ||
		len(dump.ChannelProtocolProfiles) != 1 || len(dump.ProtocolRoutingConfigs) != 1 {
		t.Fatalf("protocol tables missing from dump: rev=%d ov=%d prof=%d cfg=%d",
			len(dump.ProtocolPolicyRevisions), len(dump.ChannelModelProtocolOverrides),
			len(dump.ChannelProtocolProfiles), len(dump.ProtocolRoutingConfigs))
	}

	// --- 恢复到干净库 ---
	ctx2 := setupBackupTestDB(t)
	conn2 := dbpkg.GetDB()
	res, err := DBImportIncremental(ctx2, dump)
	if err != nil {
		t.Fatalf("DBImportIncremental: %v", err)
	}
	if res.RowsAffected["group_presets"] != 1 {
		t.Errorf("group_presets imported = %d, want 1", res.RowsAffected["group_presets"])
	}
	if res.RowsAffected["channel_model_protocol_overrides"] != 1 {
		t.Errorf("overrides imported = %d, want 1", res.RowsAffected["channel_model_protocol_overrides"])
	}
	if res.RowsAffected["channel_protocol_profiles"] != 1 {
		t.Errorf("profiles imported = %d, want 1", res.RowsAffected["channel_protocol_profiles"])
	}
	if res.RowsAffected["protocol_policy_revisions"] != 1 {
		t.Errorf("revisions imported = %d, want 1", res.RowsAffected["protocol_policy_revisions"])
	}

	// --- 恢复后完整性：preset 策略字段保留，override/profile 重映射到新 channel/key ---
	var gotPreset model.GroupPreset
	if err := conn2.Where("name = ?", "rt-preset").First(&gotPreset).Error; err != nil {
		t.Fatalf("read imported preset: %v", err)
	}
	if gotPreset.ProtocolMode != model.ProtocolPolicyModeForce || len(gotPreset.PreferredProtocols) != 1 {
		t.Errorf("preset policy fields lost: mode=%q protocols=%v", gotPreset.ProtocolMode, gotPreset.PreferredProtocols)
	}

	var gotOverride model.ChannelModelProtocolOverride
	if err := conn2.First(&gotOverride).Error; err != nil {
		t.Fatalf("read imported override: %v", err)
	}
	var newCh model.Channel
	if err := conn2.Where("name = ?", "rt-ch").First(&newCh).Error; err != nil {
		t.Fatalf("read imported channel: %v", err)
	}
	if gotOverride.ChannelID != newCh.ID {
		t.Errorf("override channel_id = %d, want remapped %d", gotOverride.ChannelID, newCh.ID)
	}
	var newKey model.ChannelKey
	if err := conn2.Where("channel_id = ? AND channel_key = ?", newCh.ID, "sk-rt").First(&newKey).Error; err != nil {
		t.Fatalf("read imported key: %v", err)
	}
	if gotOverride.ChannelKeyID != newKey.ID {
		t.Errorf("override channel_key_id = %d, want remapped %d", gotOverride.ChannelKeyID, newKey.ID)
	}

	var gotProfile model.ChannelProtocolProfile
	if err := conn2.First(&gotProfile).Error; err != nil {
		t.Fatalf("read imported profile: %v", err)
	}
	if gotProfile.ChannelID != newCh.ID {
		t.Errorf("profile channel_id = %d, want remapped %d", gotProfile.ChannelID, newCh.ID)
	}

	// --- 幂等：重复导入不产生重复行 ---
	if _, err := DBImportIncremental(ctx2, dump); err != nil {
		t.Fatalf("second import: %v", err)
	}
	var presetCount, ovCount, profCount int64
	conn2.Model(&model.GroupPreset{}).Count(&presetCount)
	conn2.Model(&model.ChannelModelProtocolOverride{}).Count(&ovCount)
	conn2.Model(&model.ChannelProtocolProfile{}).Count(&profCount)
	if presetCount != 1 || ovCount != 1 || profCount != 1 {
		t.Errorf("duplicate rows after re-import: presets=%d overrides=%d profiles=%d", presetCount, ovCount, profCount)
	}
}
