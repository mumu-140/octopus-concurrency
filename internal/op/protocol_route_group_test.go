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
	mode := model.ProtocolPolicyModeAuto
	preferred := []string{"openai_response", "openai_chat"}
	updated, err := GroupUpdate(&model.GroupUpdateRequest{
		ID:                 group.ID,
		ProtocolMode:       &mode,
		PreferredProtocols: &preferred,
	}, ctx)
	if err != nil {
		t.Fatalf("update group policy: %v", err)
	}
	if updated.ProtocolMode != mode || len(updated.PreferredProtocols) != 2 {
		t.Fatalf("group policy = %q %v", updated.ProtocolMode, updated.PreferredProtocols)
	}
	preset, err := GroupPresetCreate(group.ID, "active", ctx)
	if err != nil {
		t.Fatalf("create preset: %v", err)
	}
	if err := GroupPresetActivate(preset.ID, ctx); err != nil {
		t.Fatalf("activate preset: %v", err)
	}
	override := model.ProtocolPolicyModeOverride
	overrideOrder := []string{"anthropic"}
	if _, err := GroupPresetUpdate(preset.ID, &model.GroupPresetUpdateRequest{
		ProtocolMode:       &override,
		PreferredProtocols: &overrideOrder,
	}, ctx); err != nil {
		t.Fatalf("update active preset policy: %v", err)
	}
	cached, err := GroupGet(group.ID, ctx)
	if err != nil {
		t.Fatalf("GroupGet failed: %v", err)
	}
	if cached.ProtocolMode != override || len(cached.PreferredProtocols) != 1 || cached.PreferredProtocols[0] != "anthropic" {
		t.Fatalf("active preset did not mirror group policy: %q %v", cached.ProtocolMode, cached.PreferredProtocols)
	}
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
		ProtocolMode:       model.ProtocolPolicyModeAuto,
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
