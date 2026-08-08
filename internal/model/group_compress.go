package model

// GroupCompressConfig 分组级请求压缩配置。
// Group.CompressConfig 为 nil 表示该分组未启用压缩（默认）。
// 生效条件: 全局 compress_master_enabled 开启 && 分组配置 Enabled 为 true。
type GroupCompressConfig struct {
	Enabled     bool   `json:"enabled"`      // 分组压缩总开关
	Lite        bool   `json:"lite"`         // 空白折叠 + system 去重 + tool 结果保头保尾截断
	Headroom    bool   `json:"headroom"`     // 同构 JSON 数组转列式表(信息无损)
	OutputStyle string `json:"output_style"` // 输出风格注入: "" | "terse-prose" | "terse-cjk"
}
