package model

import "time"

// DBDump is a full-database JSON export format for Octopus.
// Import uses incremental semantics (insert new rows, and upsert on certain key-based tables).
type DBDump struct {
	Version      int       `json:"version"`
	ExportedAt   time.Time `json:"exported_at"`
	IncludeLogs  bool      `json:"include_logs"`
	IncludeStats bool      `json:"include_stats"`

	Channels            []Channel            `json:"channels,omitempty"`
	ChannelKeys         []ChannelKey         `json:"channel_keys,omitempty"`
	ProxyConfigurations []ProxyConfiguration `json:"proxy_configurations,omitempty"`
	Sites               []Site               `json:"sites,omitempty"`
	SiteAccounts        []SiteAccount        `json:"site_accounts,omitempty"`
	SiteTokens          []SiteToken          `json:"site_tokens,omitempty"`
	SiteUserGroups      []SiteUserGroup      `json:"site_user_groups,omitempty"`
	SiteModels          []SiteModel          `json:"site_models,omitempty"`
	SiteChannelBindings []SiteChannelBinding `json:"site_channel_bindings,omitempty"`
	Groups              []Group              `json:"groups,omitempty"`
	GroupItems          []GroupItem          `json:"group_items,omitempty"`
	GroupPresets        []GroupPreset        `json:"group_presets,omitempty"`
	LLMInfos            []LLMInfo            `json:"llm_infos,omitempty"`
	APIKeys             []APIKey             `json:"api_keys,omitempty"`
	Settings            []Setting            `json:"settings,omitempty"`

	ProtocolRoutingConfigs        []ProtocolRoutingConfig        `json:"protocol_routing_configs,omitempty"`
	ProtocolPolicyRevisions       []ProtocolPolicyRevision       `json:"protocol_policy_revisions,omitempty"`
	ChannelModelProtocolOverrides []ChannelModelProtocolOverride `json:"channel_model_protocol_overrides,omitempty"`
	ChannelProtocolProfiles       []ChannelProtocolProfile       `json:"channel_protocol_profiles,omitempty"`

	StatsTotal           []StatsTotal           `json:"stats_total,omitempty"`
	StatsDaily           []StatsDaily           `json:"stats_daily,omitempty"`
	StatsHourly          []StatsHourly          `json:"stats_hourly,omitempty"`
	StatsModel           []StatsModel           `json:"stats_model,omitempty"`
	StatsChannel         []StatsChannel         `json:"stats_channel,omitempty"`
	StatsAPIKey          []StatsAPIKey          `json:"stats_api_key,omitempty"`
	StatsSiteModelHourly []StatsSiteModelHourly `json:"stats_site_model_hourly,omitempty"`
	StatsDimensionHourly []StatsDimensionHourly `json:"stats_dimension_hourly,omitempty"`

	RelayLogs []RelayLog `json:"relay_logs,omitempty"`
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}
