package op

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"gorm.io/gorm"
)

// ChannelProtocolPolicyGet returns the active policy slice for one channel.
func ChannelProtocolPolicyGet(channelID int, ctx context.Context) (*model.ChannelProtocolPolicy, error) {
	if err := requireProtocolPolicyChannel(db.GetDB().WithContext(ctx), channelID); err != nil {
		return nil, err
	}
	state, err := ProtocolPolicyGet(ctx)
	if err != nil {
		return nil, err
	}
	for _, channel := range state.Channels {
		if channel.ChannelID == channelID {
			return &channel, nil
		}
	}
	return &model.ChannelProtocolPolicy{
		ChannelID: channelID,
		Profiles:  []model.ProtocolProfilePolicy{},
		Overrides: []model.ModelProtocolOverridePolicy{},
	}, nil
}

// ChannelProtocolPolicyReplace atomically replaces profiles and overrides for one channel.
func ChannelProtocolPolicyReplace(channelID int, req *model.ChannelProtocolPolicyUpdateRequest, actor string, ctx context.Context) (*model.ProtocolPolicyState, error) {
	if req == nil {
		return nil, protocolRoutingValidationError("channel protocol policy request is nil")
	}
	if req.ExpectedRevision < 0 {
		return nil, protocolRoutingValidationError("expected_revision must not be negative")
	}
	conn := db.GetDB().WithContext(ctx)
	if err := validateChannelProtocolPolicy(conn, channelID, req); err != nil {
		return nil, err
	}
	return commitProtocolPolicyMutation(ctx, req.ExpectedRevision, actor, func(tx *gorm.DB, _ *model.ProtocolRoutingConfig, nextRevision int64) error {
		if err := tx.Where("channel_id = ?", channelID).Delete(&model.ChannelProtocolProfile{}).Error; err != nil {
			return protocolRoutingDatabaseError("delete channel protocol profiles", err)
		}
		if err := tx.Where("channel_id = ?", channelID).Delete(&model.ChannelModelProtocolOverride{}).Error; err != nil {
			return protocolRoutingDatabaseError("delete model protocol overrides", err)
		}
		profiles := make([]model.ChannelProtocolProfile, 0, len(req.Profiles))
		for _, profile := range req.Profiles {
			profiles = append(profiles, model.ChannelProtocolProfile{
				ChannelID:      channelID,
				Protocol:       strings.TrimSpace(profile.Protocol),
				Enabled:        profile.Enabled,
				BaseUrls:       profile.BaseUrls,
				CustomHeaders:  profile.CustomHeaders,
				ParamOverride:  profile.ParamOverride,
				PolicyRevision: nextRevision,
			})
		}
		if len(profiles) > 0 {
			if err := tx.Create(&profiles).Error; err != nil {
				return protocolRoutingDatabaseError("create channel protocol profiles", err)
			}
		}
		overrides := make([]model.ChannelModelProtocolOverride, 0, len(req.Overrides))
		for _, override := range req.Overrides {
			overrides = append(overrides, model.ChannelModelProtocolOverride{
				ChannelID:          channelID,
				ChannelKeyID:       override.ChannelKeyID,
				UpstreamModel:      strings.TrimSpace(override.UpstreamModel),
				Mode:               override.Mode,
				PreferredProtocols: override.PreferredProtocols,
				Enabled:            override.Enabled,
				PolicyRevision:     nextRevision,
			})
		}
		if len(overrides) > 0 {
			if err := tx.Create(&overrides).Error; err != nil {
				return protocolRoutingDatabaseError("create model protocol overrides", err)
			}
		}
		return nil
	})
}

func validateChannelProtocolPolicy(conn *gorm.DB, channelID int, req *model.ChannelProtocolPolicyUpdateRequest) error {
	if err := requireProtocolPolicyChannel(conn, channelID); err != nil {
		return err
	}
	seenProfiles := make(map[string]struct{}, len(req.Profiles))
	for _, profile := range req.Profiles {
		name := strings.TrimSpace(profile.Protocol)
		if err := validateAdaptiveProtocol(name); err != nil {
			return err
		}
		if _, ok := seenProfiles[name]; ok {
			return protocolRoutingValidationError("duplicate channel protocol profile: " + name)
		}
		seenProfiles[name] = struct{}{}
		if profile.Enabled && len(profile.BaseUrls) == 0 {
			return protocolRoutingValidationError("enabled protocol profiles require a base URL")
		}
		for _, baseURL := range profile.BaseUrls {
			if err := validateProtocolProfileURL(baseURL.URL); err != nil {
				return err
			}
		}
		for _, header := range profile.CustomHeaders {
			if err := validateProtocolProfileHeader(header.HeaderKey); err != nil {
				return err
			}
		}
		if err := validateProtocolParamOverride(profile.ParamOverride); err != nil {
			return err
		}
	}

	var channelKeys []model.ChannelKey
	if err := conn.Where("channel_id = ?", channelID).Find(&channelKeys).Error; err != nil {
		return protocolRoutingDatabaseError("load channel keys for protocol policy", err)
	}
	validKeyIDs := make(map[int]struct{}, len(channelKeys))
	for _, key := range channelKeys {
		validKeyIDs[key.ID] = struct{}{}
	}
	seenOverrides := make(map[string]struct{}, len(req.Overrides))
	for _, override := range req.Overrides {
		if override.ChannelKeyID < 0 {
			return protocolRoutingValidationError("channel_key_id must not be negative")
		}
		if override.ChannelKeyID > 0 {
			if _, ok := validKeyIDs[override.ChannelKeyID]; !ok {
				return protocolRoutingValidationError("channel key does not belong to channel")
			}
		}
		modelName := strings.TrimSpace(override.UpstreamModel)
		if modelName == "" {
			return protocolRoutingValidationError("upstream_model is required")
		}
		key := fmt.Sprintf("%d\x00%s", override.ChannelKeyID, modelName)
		if _, ok := seenOverrides[key]; ok {
			return protocolRoutingValidationError("duplicate model protocol override")
		}
		seenOverrides[key] = struct{}{}
		if err := validateScopedProtocolRule(override.Mode, override.PreferredProtocols, true); err != nil {
			return err
		}
	}
	return nil
}

func requireProtocolPolicyChannel(conn *gorm.DB, channelID int) error {
	if channelID <= 0 {
		return protocolRoutingValidationError("channel id must be positive")
	}
	var count int64
	if err := conn.Model(&model.Channel{}).Where("id = ?", channelID).Count(&count).Error; err != nil {
		return protocolRoutingDatabaseError("load protocol policy channel", err)
	}
	if count != 1 {
		return protocolRoutingNotFoundError("channel not found")
	}
	return nil
}

func validateAdaptiveProtocol(value string) error {
	p := protocol.Protocol(strings.TrimSpace(value))
	if !p.IsAdaptive() {
		return protocolRoutingValidationError("unsupported adaptive protocol: " + value)
	}
	return nil
}

func validateScopedProtocolRule(mode model.ProtocolPolicyMode, protocols []string, override bool) error {
	switch mode {
	case model.ProtocolPolicyModeForce:
		if len(protocols) != 1 {
			return protocolRoutingValidationError("force mode requires exactly one protocol")
		}
	case model.ProtocolPolicyModePrefer:
		if len(protocols) == 0 {
			return protocolRoutingValidationError("prefer mode requires at least one protocol")
		}
	case model.ProtocolPolicyModeInherit, model.ProtocolPolicyModeAuto:
		if override {
			return protocolRoutingValidationError("model overrides support only prefer or force mode")
		}
		if len(protocols) != 0 {
			return protocolRoutingValidationError("inherit and auto modes must not include preferred protocols")
		}
	default:
		return protocolRoutingValidationError("invalid protocol policy mode")
	}
	seen := make(map[string]struct{}, len(protocols))
	for _, value := range protocols {
		name := strings.TrimSpace(value)
		if err := validateAdaptiveProtocol(name); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return protocolRoutingValidationError("duplicate preferred protocol: " + name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateProtocolProfileURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return protocolRoutingValidationError("protocol profile base URL must be an absolute HTTP(S) URL without credentials")
	}
	return nil
}

func validateProtocolProfileHeader(value string) error {
	name := strings.TrimSpace(value)
	if !validHTTPToken(name) {
		return protocolRoutingValidationError("invalid protocol profile header name")
	}
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "anthropic-api-key", "cookie", "set-cookie":
		return protocolRoutingValidationError("credential headers cannot be configured in protocol profiles")
	}
	return nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		}
		return false
	}
	return true
}

func validateProtocolParamOverride(value *string) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(*value), &object); err != nil || object == nil {
		return protocolRoutingValidationError("param_override must be a JSON object")
	}
	return nil
}
