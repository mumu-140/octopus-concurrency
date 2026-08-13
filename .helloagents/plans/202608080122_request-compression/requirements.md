# 需求：请求压缩模块（request-compression）

## 背景

在 fwq57ys 上对 OmniRoute v3.8.46 压缩引擎源码做了实测（2026-08-06）：

| 引擎 | 实测压缩率 | 保真度 |
|---|---|---|
| RTK（shell 工具输出） | 96% | 保留 summary 行 |
| Headroom（JSON 数组） | 71.2% | 无损可逆 |
| Session-dedup（重复块） | 32.7% | 指针式，模型需回溯 |
| Lite（whitespace） | 66.7%（样本） | ❌ 2000 字符截头丢尾，丢 npm summary |
| Output styles | +320~566 字符 | 静态注入，cache 稳定 |
| Caveman zh/en | 5.6-6.2% / 27.9-36% | en full 有语义损伤，不采用 |

结论：收益集中在工具输出类流量；Lite 原版截断策略有保真缺陷需改写。

## 目标

在 Octopus（/home/yangs/API/octopus-mumu）内以**进程内门控模块**实现请求压缩：

1. **Lite 保尾版**：折叠空白 + 去重 system prompt + 工具结果截断改为**保头保尾**（修正 OmniRoute 截头丢尾缺陷）
2. **Headroom**：消息内容中 ≥8 行的同构 JSON 数组转列式文本表，信息无损、模型可直接读
3. **Output styles**：system prompt 静态指令注入（terse-prose / terse-cjk），幂等

## 已确认决策

- 形态：进程内 package + SettingKey 总开关 + 每分组 JSON 配置（非 sidecar、非 .so 插件）
- 开关粒度：每分组（Group 加 `compress_config` JSON 列，GORM 自动迁移，nil=关闭）
- 上线：不做 shadow 模式，单测保证后直接开启
- 一期不含：RTK（shell 输出过滤，二期）、Session-dedup（需跨消息状态，暂缓）、Caveman（不采用）

## 验收边界

- 关闭状态热路径开销 <1μs（仅一次原子读）
- 任何引擎 panic/error → fail-open 原始请求直通
- 压缩后 ≥ 原始长度则丢弃结果（保真兜底）
- 只改 `InternalLLMRequest.Messages` 字符串内容，不动其他字段
