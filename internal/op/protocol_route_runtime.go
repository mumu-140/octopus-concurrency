package op

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocolroute"
)

var protocolPolicyRuntime atomic.Pointer[model.ProtocolPolicyState]
var protocolRoutingSwitch atomic.Bool

// ProtocolRoutingEnabled returns the only global runtime protocol control.
func ProtocolRoutingEnabled() bool {
	return protocolRoutingSwitch.Load()
}

func protocolRoutingRefreshCache(ctx context.Context) error {
	var config model.ProtocolRoutingConfig
	if err := db.GetDB().WithContext(ctx).First(&config, 1).Error; err != nil {
		return err
	}
	protocolRoutingSwitch.Store(config.ProtocolRoutingEnabled)
	return nil
}

// ProtocolPolicyRuntimeSnapshot returns the immutable active payload for one relay request.
// Callers must treat the returned value as read-only; writers always publish a new clone.
func ProtocolPolicyRuntimeSnapshot() (*model.ProtocolPolicyState, bool) {
	state := protocolPolicyRuntime.Load()
	if state == nil {
		return nil, false
	}
	return state, true
}

func protocolPolicyRuntimeStore(state *model.ProtocolPolicyState) {
	cloned := cloneProtocolPolicyState(state)
	protocolPolicyRuntime.Store(cloned)
	protocolRoutingSwitch.Store(cloned != nil && cloned.Config.ProtocolRoutingEnabled)
	protocolroute.SetObserveEnabled(
		cloned != nil && cloned.Config.ProtocolRoutingEnabled && cloned.Config.Mode == model.ProtocolRoutingModeObserve,
	)
}

func protocolPolicyRuntimeReset() {
	protocolPolicyRuntime.Store(nil)
	protocolRoutingSwitch.Store(false)
	protocolroute.SetObserveEnabled(false)
}

func cloneProtocolPolicyState(state *model.ProtocolPolicyState) *model.ProtocolPolicyState {
	if state == nil {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		panic("protocol policy state contains unsupported data: " + err.Error())
	}
	var cloned model.ProtocolPolicyState
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic("protocol policy state clone failed: " + err.Error())
	}
	return &cloned
}
