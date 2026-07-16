package model

// ProtocolRoutingConfigPolicy is the revisioned global protocol-routing payload.
type ProtocolRoutingConfigPolicy struct {
	ProtocolRoutingEnabled         bool                `json:"protocol_routing_enabled"`
	Mode                           ProtocolRoutingMode `json:"mode"`
	ProtocolFallbackEnabled        bool                `json:"protocol_fallback_enabled"`
	ProtocolLearningReadEnabled    bool                `json:"protocol_learning_read_enabled"`
	ProtocolLearningWriteEnabled   bool                `json:"protocol_learning_write_enabled"`
	ProtocolConversionEnabled      bool                `json:"protocol_conversion_enabled"`
	AdaptiveGroupAllowlist         []int               `json:"adaptive_group_allowlist"`
	RankingSignalOrder             []string            `json:"ranking_signal_order"`
	LegacySiteRouteLearningEnabled bool                `json:"legacy_site_route_learning_enabled"`
}

// ProtocolProfilePolicy stores an endpoint/header override for one channel protocol.
type ProtocolProfilePolicy struct {
	Protocol      string         `json:"protocol"`
	Enabled       bool           `json:"enabled"`
	BaseUrls      []BaseUrl      `json:"base_urls"`
	CustomHeaders []CustomHeader `json:"custom_headers"`
	ParamOverride *string        `json:"param_override,omitempty"`
}

// ModelProtocolOverridePolicy stores a manual model-level protocol rule.
type ModelProtocolOverridePolicy struct {
	ChannelKeyID       int                `json:"channel_key_id"`
	UpstreamModel      string             `json:"upstream_model"`
	Mode               ProtocolPolicyMode `json:"mode"`
	PreferredProtocols []string           `json:"preferred_protocols"`
	Enabled            bool               `json:"enabled"`
}

type ChannelProtocolPolicy struct {
	ChannelID int                           `json:"channel_id"`
	Profiles  []ProtocolProfilePolicy       `json:"profiles"`
	Overrides []ModelProtocolOverridePolicy `json:"overrides"`
}

type GroupProtocolPolicy struct {
	GroupID            int                `json:"group_id"`
	Mode               ProtocolPolicyMode `json:"mode"`
	PreferredProtocols []string           `json:"preferred_protocols"`
}

type GroupPresetProtocolPolicy struct {
	GroupPresetID      int                `json:"group_preset_id"`
	Mode               ProtocolPolicyMode `json:"mode"`
	PreferredProtocols []string           `json:"preferred_protocols"`
}

// ProtocolPolicyPayload is the sole source of truth for a committed revision.
type ProtocolPolicyPayload struct {
	SchemaVersion int                         `json:"schema_version"`
	Config        ProtocolRoutingConfigPolicy `json:"config"`
	Channels      []ChannelProtocolPolicy     `json:"channels"`
	Groups        []GroupProtocolPolicy       `json:"groups"`
	GroupPresets  []GroupPresetProtocolPolicy `json:"group_presets"`
}

type ProtocolPolicyState struct {
	ActiveRevision int64 `json:"active_revision"`
	ProtocolPolicyPayload
}

// ProtocolRoutingConfigUpdateRequest is a partial update guarded by ExpectedRevision.
type ProtocolRoutingConfigUpdateRequest struct {
	ExpectedRevision               int64                `json:"expected_revision" binding:"min=0"`
	ProtocolRoutingEnabled         *bool                `json:"protocol_routing_enabled,omitempty"`
	Mode                           *ProtocolRoutingMode `json:"mode,omitempty"`
	ProtocolFallbackEnabled        *bool                `json:"protocol_fallback_enabled,omitempty"`
	ProtocolLearningReadEnabled    *bool                `json:"protocol_learning_read_enabled,omitempty"`
	ProtocolLearningWriteEnabled   *bool                `json:"protocol_learning_write_enabled,omitempty"`
	ProtocolConversionEnabled      *bool                `json:"protocol_conversion_enabled,omitempty"`
	AdaptiveGroupAllowlist         *[]int               `json:"adaptive_group_allowlist,omitempty"`
	RankingSignalOrder             *[]string            `json:"ranking_signal_order,omitempty"`
	LegacySiteRouteLearningEnabled *bool                `json:"legacy_site_route_learning_enabled,omitempty"`
}

type ChannelProtocolPolicyUpdateRequest struct {
	ExpectedRevision int64                         `json:"expected_revision" binding:"min=0"`
	Profiles         []ProtocolProfilePolicy       `json:"profiles"`
	Overrides        []ModelProtocolOverridePolicy `json:"overrides"`
}

type ScopedProtocolPolicyUpdateRequest struct {
	ExpectedRevision   int64              `json:"expected_revision" binding:"min=0"`
	Mode               ProtocolPolicyMode `json:"mode" binding:"required"`
	PreferredProtocols []string           `json:"preferred_protocols"`
}
