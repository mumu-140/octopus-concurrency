package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocolroute"
)

func TestProtocolPolicyRuntimeSnapshotIsImmutable(t *testing.T) {
	protocolPolicyRuntimeReset()
	t.Cleanup(protocolPolicyRuntimeReset)
	state := &model.ProtocolPolicyState{
		ActiveRevision: 3,
		ProtocolPolicyPayload: model.ProtocolPolicyPayload{
			Config: model.ProtocolRoutingConfigPolicy{
				ProtocolRoutingEnabled: true,
				Mode:                   model.ProtocolRoutingModeObserve,
				AdaptiveGroupAllowlist: []int{7},
			},
			Channels: []model.ChannelProtocolPolicy{{
				ChannelID: 1,
				Profiles: []model.ProtocolProfilePolicy{{
					Protocol:      "anthropic",
					CustomHeaders: []model.CustomHeader{{HeaderKey: "X-Test", HeaderValue: "original"}},
				}},
			}},
		},
	}

	protocolPolicyRuntimeStore(state)
	state.Channels[0].Profiles[0].CustomHeaders[0].HeaderValue = "mutated-source"
	first, ok := ProtocolPolicyRuntimeSnapshot()
	if !ok {
		t.Fatal("runtime snapshot missing")
	}
	if got := first.Channels[0].Profiles[0].CustomHeaders[0].HeaderValue; got != "original" {
		t.Fatalf("stored header = %q", got)
	}
	second, _ := ProtocolPolicyRuntimeSnapshot()
	if got := second.Channels[0].Profiles[0].CustomHeaders[0].HeaderValue; got != "original" {
		t.Fatalf("reloaded header = %q", got)
	}
	if first != second {
		t.Fatal("runtime snapshot should be an atomic immutable pointer")
	}
	if !protocolroute.ObserveEnabled() {
		t.Fatal("observe runtime flag not synchronized")
	}
}
