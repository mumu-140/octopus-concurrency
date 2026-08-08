// Package compress 提供请求级上下文压缩（借鉴 OmniRoute 压缩引擎实测结论的 Go 原生实现）。
//
// 设计约束：
//   - 非侵入：仅在 relay 转发前单点调用；master 开关或分组配置关闭时零行为变化
//   - fail-open：任何引擎 panic/error 不影响请求转发
//   - 只修改 Messages 文本内容，不动请求其他字段
//   - 保真兜底：引擎只在结果更短时替换（输出风格注入除外，其定位是行为塑形）
package compress
