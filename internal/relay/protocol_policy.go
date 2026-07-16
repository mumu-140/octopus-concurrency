package relay

import (
	"encoding/json"
	"slices"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
)

func buildProtocolPolicySnapshot(state *dbmodel.ProtocolPolicyState, groupID int) (protocolroute.PolicySnapshot, protocolroute.RoutingMode) {
	if state == nil || !state.Config.ProtocolRoutingEnabled || state.Config.Mode == dbmodel.ProtocolRoutingModeLegacy {
		return protocolroute.LegacySnapshot(), protocolroute.RoutingLegacy
	}

	mode := protocolroute.RoutingMode(state.Config.Mode)
	if mode == protocolroute.RoutingAdaptive && !slices.Contains(state.Config.AdaptiveGroupAllowlist, groupID) {
		mode = protocolroute.RoutingObserve
	}
	snapshot := protocolroute.PolicySnapshot{
		ConfigRevision:      state.ActiveRevision,
		Mode:                mode,
		ConversionEnabled:   state.Config.ProtocolConversionEnabled,
		LearningReadEnabled: state.Config.ProtocolLearningReadEnabled,
		Group:               protocolroute.ScopedRule{Mode: protocolroute.ModeInherit},
		KeyModelRules:       make(map[protocolroute.KeyModelScopeKey]protocolroute.ScopedRule),
		ChanModelRules:      make(map[protocolroute.ChanModelScopeKey]protocolroute.ScopedRule),
		EnabledProfiles:     make(map[int]map[protocol.Protocol]bool),
	}
	for _, group := range state.Groups {
		if group.GroupID == groupID {
			snapshot.Group = scopedProtocolRule(group.Mode, group.PreferredProtocols)
			break
		}
	}
	for _, channel := range state.Channels {
		if len(channel.Profiles) > 0 {
			profiles := make(map[protocol.Protocol]bool, len(channel.Profiles))
			for _, profile := range channel.Profiles {
				profiles[protocol.Protocol(profile.Protocol)] = profile.Enabled
			}
			snapshot.EnabledProfiles[channel.ChannelID] = profiles
		}
		for _, override := range channel.Overrides {
			if !override.Enabled {
				continue
			}
			rule := scopedProtocolRule(override.Mode, override.PreferredProtocols)
			if override.ChannelKeyID > 0 {
				snapshot.KeyModelRules[protocolroute.KeyModelScopeKey{
					ChannelKeyID:  override.ChannelKeyID,
					UpstreamModel: override.UpstreamModel,
				}] = rule
				continue
			}
			snapshot.ChanModelRules[protocolroute.ChanModelScopeKey{
				ChannelID:     channel.ChannelID,
				UpstreamModel: override.UpstreamModel,
			}] = rule
		}
	}
	return snapshot, mode
}

func protocolPolicyForChannel(state *dbmodel.ProtocolPolicyState, channelID int) dbmodel.ChannelProtocolPolicy {
	if state != nil {
		for _, channel := range state.Channels {
			if channel.ChannelID == channelID {
				return channel
			}
		}
	}
	return dbmodel.ChannelProtocolPolicy{ChannelID: channelID}
}

func scopedProtocolRule(mode dbmodel.ProtocolPolicyMode, values []string) protocolroute.ScopedRule {
	protocols := make([]protocol.Protocol, 0, len(values))
	for _, value := range values {
		protocols = append(protocols, protocol.Protocol(value))
	}
	return protocolroute.ScopedRule{
		Mode:      protocolroute.PolicyMode(mode),
		Protocols: protocols,
	}
}

func buildAttemptConfigs(channel *dbmodel.Channel, policy dbmodel.ChannelProtocolPolicy) (protocolroute.AttemptConfig, map[protocol.Protocol]protocolroute.AttemptConfig) {
	legacy := protocolroute.AttemptConfig{
		BaseURL:       channel.GetBaseUrl(),
		HeaderPolicy:  protocolroute.HeaderPolicy{Set: customHeaderMap(channel.CustomHeader)},
		ParamOverride: rawParamOverride(channel.ParamOverride),
	}
	profiles := make(map[protocol.Protocol]protocolroute.AttemptConfig, len(policy.Profiles))
	channelProtocol := protocol.FromOutboundType(channel.Type)
	for _, profile := range policy.Profiles {
		if !profile.Enabled {
			continue
		}
		profileProtocol := protocol.Protocol(profile.Protocol)
		headers := customHeaderMap(channel.CustomHeader)
		for key, value := range customHeaderMap(profile.CustomHeaders) {
			headers[key] = value
		}
		paramOverride := rawParamOverride(profile.ParamOverride)
		if profileProtocol == channelProtocol && profile.ParamOverride == nil {
			paramOverride = legacy.ParamOverride
		}
		profiles[profileProtocol] = protocolroute.AttemptConfig{
			BaseURL:       bestBaseURL(profile.BaseUrls, legacy.BaseURL),
			HeaderPolicy:  protocolroute.HeaderPolicy{Set: headers},
			ParamOverride: paramOverride,
		}
	}
	if _, ok := profiles[channelProtocol]; !ok {
		profiles[channelProtocol] = legacy
	}
	return legacy, profiles
}

func customHeaderMap(headers []dbmodel.CustomHeader) map[string]string {
	result := make(map[string]string, len(headers))
	for _, header := range headers {
		result[header.HeaderKey] = header.HeaderValue
	}
	return result
}

func rawParamOverride(value *string) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), (*value)...)
}

func bestBaseURL(baseURLs []dbmodel.BaseUrl, fallback string) string {
	bestURL := fallback
	bestDelay := 0
	found := false
	for _, baseURL := range baseURLs {
		if baseURL.URL == "" {
			continue
		}
		if !found || baseURL.Delay < bestDelay {
			bestURL = baseURL.URL
			bestDelay = baseURL.Delay
			found = true
		}
	}
	return bestURL
}
