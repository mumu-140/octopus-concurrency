package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestGroupProtocolPolicyLifecycleUsesGroupCacheAsSource(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	group := &model.Group{Name: "group-policy-lifecycle", Mode: model.GroupModeFailover}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if group.ProtocolMode != model.ProtocolPolicyModeFollow || len(group.PreferredProtocols) != 0 {
		t.Fatalf("new group policy = %q %v, want follow []", group.ProtocolMode, group.PreferredProtocols)
	}

	auto := model.ProtocolPolicyModeAuto
	preferred := []string{"openai_response", "anthropic"}
	updated, err := GroupUpdate(&model.GroupUpdateRequest{
		ID:                 group.ID,
		ProtocolMode:       &auto,
		PreferredProtocols: &preferred,
	}, ctx)
	if err != nil {
		t.Fatalf("GroupUpdate failed: %v", err)
	}
	if updated.ProtocolMode != auto || !sameStrings(updated.PreferredProtocols, preferred) {
		t.Fatalf("updated group policy = %q %v", updated.ProtocolMode, updated.PreferredProtocols)
	}

	preset, err := GroupPresetCreate(group.ID, "policy snapshot", ctx)
	if err != nil {
		t.Fatalf("GroupPresetCreate failed: %v", err)
	}
	if preset.ProtocolMode != auto || !sameStrings(preset.PreferredProtocols, preferred) {
		t.Fatalf("preset policy = %q %v", preset.ProtocolMode, preset.PreferredProtocols)
	}
	if err := GroupPresetActivate(preset.ID, ctx); err != nil {
		t.Fatalf("GroupPresetActivate failed: %v", err)
	}

	override := model.ProtocolPolicyModeOverride
	overrideOrder := []string{"anthropic", "openai_chat", "openai_response"}
	if _, err := GroupPresetUpdate(preset.ID, &model.GroupPresetUpdateRequest{
		ProtocolMode:       &override,
		PreferredProtocols: &overrideOrder,
	}, ctx); err != nil {
		t.Fatalf("GroupPresetUpdate active policy failed: %v", err)
	}
	cached, err := GroupGet(group.ID, ctx)
	if err != nil {
		t.Fatalf("GroupGet failed: %v", err)
	}
	if cached.ProtocolMode != override || !sameStrings(cached.PreferredProtocols, overrideOrder) {
		t.Fatalf("active preset did not refresh cache: %q %v", cached.ProtocolMode, cached.PreferredProtocols)
	}
}

func TestGroupProtocolPolicyRejectsInvalidOrderWithoutMutation(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	group := &model.Group{Name: "group-policy-validation", Mode: model.GroupModeFailover}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}

	override := model.ProtocolPolicyModeOverride
	invalid := []string{"anthropic", "anthropic"}
	if _, err := GroupUpdate(&model.GroupUpdateRequest{
		ID:                 group.ID,
		ProtocolMode:       &override,
		PreferredProtocols: &invalid,
	}, ctx); err == nil {
		t.Fatal("duplicate protocol order was accepted")
	}
	cached, err := GroupGet(group.ID, ctx)
	if err != nil {
		t.Fatalf("GroupGet failed: %v", err)
	}
	if cached.ProtocolMode != model.ProtocolPolicyModeFollow || len(cached.PreferredProtocols) != 0 {
		t.Fatalf("invalid update mutated cache: %q %v", cached.ProtocolMode, cached.PreferredProtocols)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
