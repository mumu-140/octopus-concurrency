package op

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
)

var groupProtocols = map[string]struct{}{
	"openai_chat":     {},
	"openai_response": {},
	"anthropic":       {},
}

func normalizeGroupProtocolPolicy(mode model.ProtocolPolicyMode, protocols []string) (model.ProtocolPolicyMode, []string, error) {
	if mode == "" || mode == model.ProtocolPolicyModeInherit {
		mode = model.ProtocolPolicyModeFollow
	}
	if mode == model.ProtocolPolicyModeFollow {
		return mode, []string{}, nil
	}
	if mode != model.ProtocolPolicyModeAuto && mode != model.ProtocolPolicyModeOverride {
		return "", nil, fmt.Errorf("unsupported protocol mode: %s", mode)
	}
	if len(protocols) == 0 || len(protocols) > len(groupProtocols) {
		return "", nil, fmt.Errorf("protocol order must contain 1-%d protocols", len(groupProtocols))
	}

	result := make([]string, 0, len(protocols))
	seen := make(map[string]struct{}, len(protocols))
	for _, value := range protocols {
		if _, ok := groupProtocols[value]; !ok {
			return "", nil, fmt.Errorf("unsupported protocol: %s", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return "", nil, fmt.Errorf("duplicate protocol: %s", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return mode, result, nil
}

func groupProtocolPolicyForUpdate(group model.Group, req *model.GroupUpdateRequest) (model.ProtocolPolicyMode, []string, error) {
	mode := group.ProtocolMode
	protocols := group.PreferredProtocols
	if req.ProtocolMode != nil {
		mode = *req.ProtocolMode
	}
	if req.PreferredProtocols != nil {
		protocols = *req.PreferredProtocols
	}
	return normalizeGroupProtocolPolicy(mode, protocols)
}
