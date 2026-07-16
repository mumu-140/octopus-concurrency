package op

import (
	"encoding/json"
	"sync/atomic"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocolroute"
)

var protocolPolicyRuntime atomic.Pointer[model.ProtocolPolicyState]

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
	protocolroute.SetObserveEnabled(
		cloned != nil && cloned.Config.ProtocolRoutingEnabled && cloned.Config.Mode == model.ProtocolRoutingModeObserve,
	)
}

func protocolPolicyRuntimeReset() {
	protocolPolicyRuntime.Store(nil)
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
