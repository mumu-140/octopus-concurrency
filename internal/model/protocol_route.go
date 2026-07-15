package model

import "time"

// 协议路由策略持久化模型（设计 §10.1-§10.5，阶段 A/B）。
//
// model 是叶子包，不能引入 internal/protocol 或 internal/protocolroute
// （它们反向依赖 model）。因此协议值、策略模式在本层以稳定字符串存储，
// 由上层包做枚举校验与映射。

// ProtocolPolicyMode 是 Group / GroupPreset / 模型覆盖使用的策略模式字符串。
// 取值 inherit / auto / prefer / force，与 protocolroute.PolicyMode 对齐。
type ProtocolPolicyMode string

const (
	ProtocolPolicyModeInherit ProtocolPolicyMode = "inherit"
	ProtocolPolicyModeAuto    ProtocolPolicyMode = "auto"
	ProtocolPolicyModePrefer  ProtocolPolicyMode = "prefer"
	ProtocolPolicyModeForce   ProtocolPolicyMode = "force"
)

// ProtocolRoutingMode 是全局运行模式字符串，与 protocolroute.RoutingMode 对齐。
type ProtocolRoutingMode string

const (
	ProtocolRoutingModeLegacy   ProtocolRoutingMode = "legacy"
	ProtocolRoutingModeObserve  ProtocolRoutingMode = "observe"
	ProtocolRoutingModeAdaptive ProtocolRoutingMode = "adaptive"
)

// ProtocolRoutingConfig 是全局单例配置表（设计 §10.1）。
// active_revision 是唯一 CAS 锚点；其余字段都是 active revision payload 的
// 查询投影，resolver 不直接拼接这些投影，只按 active_revision 读取 payload。
type ProtocolRoutingConfig struct {
	ID                             int                 `json:"id" gorm:"primaryKey"`
	ActiveRevision                 int64               `json:"active_revision" gorm:"not null;default:0"`
	ProtocolRoutingEnabled         bool                `json:"protocol_routing_enabled" gorm:"not null;default:false"`
	Mode                           ProtocolRoutingMode `json:"mode" gorm:"type:varchar(16);not null;default:'legacy'"`
	ProtocolFallbackEnabled        bool                `json:"protocol_fallback_enabled" gorm:"not null;default:false"`
	ProtocolLearningReadEnabled    bool                `json:"protocol_learning_read_enabled" gorm:"not null;default:false"`
	ProtocolLearningWriteEnabled   bool                `json:"protocol_learning_write_enabled" gorm:"not null;default:false"`
	ProtocolConversionEnabled      bool                `json:"protocol_conversion_enabled" gorm:"not null;default:false"`
	AdaptiveGroupAllowlist         []int               `json:"adaptive_group_allowlist" gorm:"serializer:json"`
	RankingSignalOrder             []string            `json:"ranking_signal_order" gorm:"serializer:json"`
	LegacySiteRouteLearningEnabled bool                `json:"legacy_site_route_learning_enabled" gorm:"not null;default:true"`
	UpdatedAt                      time.Time           `json:"updated_at"`
}

// ProtocolPolicyRevision 是追加式策略版本表（设计 §10.1）。
// payload_json 是策略唯一真源，其他策略字段/表只投影 active revision。
type ProtocolPolicyRevision struct {
	Revision       int64     `json:"revision" gorm:"primaryKey;autoIncrement:false"`
	ParentRevision int64     `json:"parent_revision" gorm:"not null;default:0"`
	SchemaVersion  int       `json:"schema_version" gorm:"not null;default:1"`
	PayloadJSON    string    `json:"payload_json" gorm:"type:text"`
	PayloadSHA256  string    `json:"payload_sha256" gorm:"type:varchar(64);index"`
	Actor          string    `json:"actor"`
	CreatedAt      time.Time `json:"created_at"`
}

// ChannelModelProtocolOverride 是渠道/Key 级模型协议覆盖（设计 §10.4）。
// 唯一约束 channel_id + channel_key_id + upstream_model；channel_key_id=0
// 表示该渠道全部 Key。首版只支持精确模型名。
type ChannelModelProtocolOverride struct {
	ID                 int                `json:"id" gorm:"primaryKey"`
	ChannelID          int                `json:"channel_id" gorm:"not null;index:idx_channel_model_protocol_override,unique"`
	ChannelKeyID       int                `json:"channel_key_id" gorm:"not null;default:0;index:idx_channel_model_protocol_override,unique"`
	UpstreamModel      string             `json:"upstream_model" gorm:"not null;index:idx_channel_model_protocol_override,unique"`
	Mode               ProtocolPolicyMode `json:"mode" gorm:"type:varchar(16);not null;default:'prefer'"`
	PreferredProtocols []string           `json:"preferred_protocols" gorm:"serializer:json"`
	Enabled            bool               `json:"enabled" gorm:"not null;default:true"`
	PolicyRevision     int64              `json:"policy_revision" gorm:"not null;default:0"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// ChannelProtocolProfile 是协议专用端点配置（设计 §10.5）。
// 唯一约束 channel_id + protocol。Channel.Type 对应协议在 revision payload
// 中形成隐式默认 Profile；其他 Adaptive 协议须显式创建启用。
type ChannelProtocolProfile struct {
	ID             int            `json:"id" gorm:"primaryKey"`
	ChannelID      int            `json:"channel_id" gorm:"not null;index:idx_channel_protocol_profile,unique"`
	Protocol       string         `json:"protocol" gorm:"type:varchar(32);not null;index:idx_channel_protocol_profile,unique"`
	Enabled        bool           `json:"enabled" gorm:"not null;default:false"`
	BaseUrls       []BaseUrl      `json:"base_urls" gorm:"serializer:json"`
	CustomHeaders  []CustomHeader `json:"custom_headers" gorm:"serializer:json"`
	ParamOverride  *string        `json:"param_override"`
	PolicyRevision int64          `json:"policy_revision" gorm:"not null;default:0"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}
