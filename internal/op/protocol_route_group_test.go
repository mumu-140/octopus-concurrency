package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/apperror"
	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestGroupProtocolPolicyUpdateAndActivePresetMirror(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	group := createProtocolRouteTestGroup(t, "group-policy")
	preset := &model.GroupPreset{
		GroupID:      group.ID,
		Name:         "active",
		Mode:         model.GroupModeRoundRobin,
		ProtocolMode: model.ProtocolPolicyModeInherit,
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.Group{}).Where("id = ?", group.ID).Update("active_preset_id", preset.ID).Error; err != nil {
		t.Fatalf("activate preset marker: %v", err)
	}

	state, err := GroupProtocolPolicyUpdate(group.ID, &model.ScopedProtocolPolicyUpdateRequest{
		ExpectedRevision:   0,
		Mode:               model.ProtocolPolicyModePrefer,
		PreferredProtocols: []string{"openai_response", "openai_chat"},
	}, "admin", ctx)
	if err != nil {
		t.Fatalf("update group policy: %v", err)
	}
	if state.ActiveRevision != 1 {
		t.Fatalf("active revision=%d, want 1", state.ActiveRevision)
	}

	state, err = GroupPresetProtocolPolicyUpdate(preset.ID, &model.ScopedProtocolPolicyUpdateRequest{
		ExpectedRevision:   1,
		Mode:               model.ProtocolPolicyModeForce,
		PreferredProtocols: []string{"anthropic"},
	}, "admin", ctx)
	if err != nil {
		t.Fatalf("update active preset policy: %v", err)
	}
	if state.ActiveRevision != 2 {
		t.Fatalf("active revision=%d, want 2", state.ActiveRevision)
	}

	var reloadedGroup model.Group
	if err := dbpkg.GetDB().WithContext(ctx).First(&reloadedGroup, group.ID).Error; err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if reloadedGroup.ProtocolMode != model.ProtocolPolicyModeForce || len(reloadedGroup.PreferredProtocols) != 1 || reloadedGroup.PreferredProtocols[0] != "anthropic" {
		t.Fatalf("active preset policy was not mirrored: %+v", reloadedGroup)
	}
	assertProtocolRevisionIntegrity(t, ctx, 2, 2)
}

func TestGroupProtocolPolicyRejectsInvalidRuleAndMissingResource(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	group := createProtocolRouteTestGroup(t, "invalid-policy")

	_, err := GroupProtocolPolicyUpdate(group.ID, &model.ScopedProtocolPolicyUpdateRequest{
		ExpectedRevision:   0,
		Mode:               model.ProtocolPolicyModeForce,
		PreferredProtocols: []string{"openai_chat", "anthropic"},
	}, "admin", ctx)
	if apperror.Code(err) != CodeProtocolRoutingValidation {
		t.Fatalf("expected validation error, got %v (%s)", err, apperror.Code(err))
	}

	_, err = GroupProtocolPolicyUpdate(9999, &model.ScopedProtocolPolicyUpdateRequest{
		ExpectedRevision: 0,
		Mode:             model.ProtocolPolicyModeInherit,
	}, "admin", ctx)
	if apperror.Code(err) != CodeProtocolRoutingNotFound {
		t.Fatalf("expected not found, got %v (%s)", err, apperror.Code(err))
	}
	assertProtocolRevisionIntegrity(t, ctx, 0, 0)
}

func TestGroupPresetLifecyclePreservesProtocolPolicy(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	group := &model.Group{
		Name:               "preset-policy",
		Mode:               model.GroupModeRoundRobin,
		ProtocolMode:       model.ProtocolPolicyModePrefer,
		PreferredProtocols: []string{"openai_response", "openai_chat"},
	}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatalf("create group: %v", err)
	}

	preset, err := GroupPresetCreate(group.ID, "snapshot", ctx)
	if err != nil {
		t.Fatalf("create preset: %v", err)
	}
	if preset.ProtocolMode != group.ProtocolMode || len(preset.PreferredProtocols) != 2 {
		t.Fatalf("create dropped protocol policy: %+v", preset)
	}
	clone, err := GroupPresetClone(preset.ID, "snapshot copy", ctx)
	if err != nil {
		t.Fatalf("clone preset: %v", err)
	}
	if clone.ProtocolMode != preset.ProtocolMode || len(clone.PreferredProtocols) != 2 {
		t.Fatalf("clone dropped protocol policy: %+v", clone)
	}

	reset := model.Group{ProtocolMode: model.ProtocolPolicyModeInherit, PreferredProtocols: []string{}}
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.Group{}).Where("id = ?", group.ID).
		Select("protocol_mode", "preferred_protocols").Updates(&reset).Error; err != nil {
		t.Fatalf("reset group policy: %v", err)
	}
	if err := GroupPresetActivate(preset.ID, ctx); err != nil {
		t.Fatalf("activate preset: %v", err)
	}
	var activated model.Group
	if err := dbpkg.GetDB().WithContext(ctx).First(&activated, group.ID).Error; err != nil {
		t.Fatalf("reload activated group: %v", err)
	}
	if activated.ProtocolMode != preset.ProtocolMode || len(activated.PreferredProtocols) != 2 {
		t.Fatalf("activation dropped protocol policy: %+v", activated)
	}
}

func createProtocolRouteTestGroup(t *testing.T, name string) *model.Group {
	t.Helper()
	group := &model.Group{Name: name, Mode: model.GroupModeRoundRobin, ProtocolMode: model.ProtocolPolicyModeInherit}
	if err := dbpkg.GetDB().Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	return group
}
