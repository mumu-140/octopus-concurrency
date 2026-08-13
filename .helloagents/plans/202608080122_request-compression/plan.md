# 方案：请求压缩模块（request-compression）

## 架构

```
gin router → relay.Handler(inboundType, c)
  → parseRequest() → *model.InternalLLMRequest      (relay_parse.go:15)
  → op.GroupGetEnabledMap(...) → group               (relay_handler.go:55)
  → 【新挂点】compress.MaybeApply(request, group)     (relay_handler.go ~L56)
  → prepareHTTPReplay / newRelayIterator / outbound…  (不变)
```

单一挂点覆盖全部入站协议（OpenAI/Anthropic/Gemini 在 parseRequest 已收敛为规范模型）。

## 文件结构（新增一个目录，侵入 ~10 行）

```
internal/relay/compress/
  config.go        # GroupCompressConfig 结构 + 全局 master SettingKey
  engine.go        # Engine 接口 + fail-open 包装（recover + 长度校验）
  apply.go         # MaybeApply 编排：开关判断→阈值过滤→按序执行引擎→统计日志
  lite.go          # Lite 保尾版
  headroom.go      # JSON 数组 → 列式表
  output_style.go  # system prompt 注入（幂等标记）
  lite_test.go / headroom_test.go / output_style_test.go / apply_test.go
  bench_test.go    # 关闭路径开销 + 各引擎 P99 预算
```

改动既有文件（各 ≤5 行）：
- `internal/model/group.go`：Group 加 `CompressConfig *compress.GroupCompressConfig \`json:"compress_config,omitempty" gorm:"serializer:json"\``
  （若 model→relay 反向依赖不允许，则 config 类型下沉到 `internal/model`，compress 包引用之——以实现时 import 方向为准）
- `internal/model/setting.go`：加 `SettingKeyCompressMasterEnabled = "compress_master_enabled"`
- `internal/relay/relay_handler.go`：挂点调用 + import

## 引擎规格

### 1. Lite 保尾版（对照 OmniRoute lite.ts 修正）
- `collapseWhitespace`：>2 连续换行折叠为 2；行尾空白删除。**不动**代码围栏内部
- `dedupSystemPrompt`：多条 system 消息内容重复时去重
- `compressToolResults`（**保尾修正**）：role=tool 且长度 >3000 字符 → 保留头 2000 + 尾 800，中间替换为 `\n...[truncated N chars]...\n`。
  原 OmniRoute 只保头 2000，实测丢失 npm 结尾 summary（KEY_LINES_DROPPED）；保尾是本期核心修正
- 跳过：单条消息 <500 字符不处理

### 2. Headroom（移植 OmniRoute headroom 算法）
- 在消息字符串内探测同构 JSON 数组（≥8 行、扁平对象、键集一致）
- 转为列式文本表（表头 + 每行值），信息无损；模型直接阅读，无需重建
- 测试要求：表可解析回原行集（round-trip）

### 3. Output styles
- 静态指令文本，注入/合并进 system prompt；幂等标记 `[Octopus Output Style]`，重复注入去重
- 目录：`terse-prose`（en）、`terse-cjk`（zh）
- 不开启时零字符变化；不属于压缩，属于行为塑形

## 配置

```jsonc
// Group.compress_config（nil = 关闭）
{
  "enabled": true,
  "lite": true,
  "headroom": true,
  "output_style": "terse-prose"   // "" | "terse-prose" | "terse-cjk"
}
```

- 全局急停：`compress_master_enabled`（SettingKey，默认 false；部署时置 true）
- 生效条件 = master ON && group.compress_config.enabled

## 数据流与错误处理

- 引擎链顺序：Lite → Headroom → Output styles
- 每个引擎经 fail-open 包装：`recover()` + error → 记 warn 日志，保留上一次良好状态继续后续引擎
- 整链完成后校验：总长度 ≥ 原长度 → 整体丢弃，记 info 日志
- 统计：每请求一行 debug 日志（引擎命中、原长/新长、savedPct、耗时 μs）；不接 metrics 管道（一期）

## 验证策略

- 单测：每引擎 fixture 测试（npm 输出保尾、JSON 数组 round-trip、幂等性、代码围栏不动）
- fail-open 测试：注入 panic 引擎，断言原始请求字节级不变
- bench：`BenchmarkMaybeApplyDisabled`（<1μs）、各引擎典型负载
- 仓库级：`go build ./... && go test ./...`、`scripts/check-governance.sh --repo`
- 部署：按 `docs/octopus-production.md` 后台构建+切换，回滚容器待命；先对 1 个测试分组开启观察

## 完成定义（DoD）

1. 关闭路径 benchmark <1μs，现有测试全绿
2. Lite 保尾：npm fixture 尾部 summary 行保留；幂等
3. Headroom：30 行 JSON fixture ≥60% 节省且 round-trip 通过
4. Output styles：注入一次且仅一次（+≤600 字符）
5. 分组 nil 配置零行为变化；master 关闭全局零行为变化
6. fail-open panic 测试通过
7. governance 检查通过；后台部署 + 回滚方案就绪

qaMode: standard
qaFocus: 压缩保真（保尾/round-trip/幂等）、fail-open、关闭路径零开销、分组隔离
