# Omniroute 分组路由策略报告（21128 服务本体）

- **调研对象**：`http://222.28.118.57:21128` 上独立运行的 **omniroute v16.2.12**（Node 聚合网关），非 octopus 渠道里的引用。
- **源码位置**：`/home/yangs/.nvm/versions/node/v22.23.1/lib/node_modules/omniroute/`（`@omniroute/open-sse` 为仓库内包，`./open-sse/`）。
- **台账库**：`/home/yangs/.omniroute/storage.sqlite`（只读，combos 表）。
- **采集时间**：2026-08-12。
- **性质**：只读调研报告，供「哪些策略值得移植到 octopus」决策。**未做任何写入。**

---

## 0. 一句话结论

omniroute 21 个分组（combo）里 **20 个用 `auto`（智能路由），1 个用 `priority`**。auto 不是"黑盒"，而是一套完整可解释的决策链：**候选池 → 前置过滤 → 13 维加权打分 → bandit 探索选择 → 按序执行 + 熔断/冷却/重试/接替**。它对 octopus 最有借鉴价值的三件事是：

1. **健康优先的加权打分排序**（不是"从头开始"的纯优先级，也不是无脑轮询）——候选按实时健康度/延迟/成本打分后再排序尝试；
2. **打分开到顶后按档位加权轮换**（ScoreTierRotator）——避免"最优候选被盯死"，同档内轮换分摊流量；
3. **失败转移时的软惩罚与冷却退避**（指数退避 + 429 冷却 + 熔断跳过）——比 octopus 现在的"从头开始 + 硬失败"平滑。

---

## 1. 21 个 combo 的实况（台账提取）

| combo | strategy | models | maxRetries | retryDelay | handoff | 候选池 | 备注 |
|---|---|---|---|---|---|---|---|
| deepseek-v4-flash-cb | **auto** | 15 | 3 | 500ms | 0.85 | 9 | 显式 weights+ship-fast+rules+expl0.05 |
| deepseek-v4-pro | **auto** | 11 | 3 | 500ms | 0.85 | 6 | 显式 weights+ship-fast+rules+expl0.05 |
| glm-5.2 | **auto** | 8 | 3 | 500ms | 0.85 | 6 | 显式 weights+ship-fast+rules+expl0.05 |
| glm-4.7 | **auto** | 4 | 3 | 500ms | 0.85 | 4 | 显式 weights+ship-fast+rules+expl0.05 |
| claude-haiku-4.5 | **auto** | 2 | 3 | 500ms | 0.85 | 3 | 默认权重 |
| claude-sonnet-4.5 | **auto** | 4 | 3 | 500ms | 0.85 | 2 | 默认权重 |
| claude-sonnet-4.6 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选（无分流意义） |
| claude-sonnet-5 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| grok-4.20 | **auto** | 9 | 3 | 500ms | 0.85 | 7 | 默认权重 |
| grok-4.3 | **auto** | 8 | 3 | 500ms | 0.85 | 6 | 默认权重 |
| grok-4.5 | **auto** | 3 | 3 | 500ms | 0.85 | 3 | 默认权重 |
| kimi-k2.6 | **auto** | 8 | 3 | 500ms | 0.85 | 8 | 默认权重 |
| kimi-k2.7 | **auto** | 6 | 3 | 500ms | 0.85 | 5 | 默认权重 |
| kimi-k2.5 | **auto** | 3 | 3 | 500ms | 0.85 | 3 | 默认权重 |
| kimi-k3 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| gpt-5 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| gpt-5.4 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| gpt-5.5 | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| gpt-5.6-terra | **auto** | 1 | 3 | 500ms | 0.85 | 1 | 单候选 |
| deepseek_flash-0731-cb | **auto** | 7 | **1** | **2000ms** | 0.85 | 0 | 重试策略不同 |
| web-search | **priority** | 4 | — | — | — | 0 | 唯一非 auto |

**要点**：
- 单候选 combo（models=1）共 8 个：claude-sonnet-4.6/5、gpt-5/5.4/5.5/5.6-terra、kimi-k3。这些 auto 打分没有分流意义，只做「顺序尝试 + 故障转移」。
- 只有 4 个 combo 显式配了权重 + modePack（全部 ship-fast + rules + explorationRate 0.05）：deepseek-v4-flash-cb、deepseek-v4-pro、glm-5.2、glm-4.7。其余走默认 13 维权重。
- 「候选池」列（pool）≤ models 列：显式 models 就是候选池，运行时再按连接过滤后得到实际 pool。

---

## 2. 运行时主链路

```
handleChat (chat.ts:239)
 └─ 若模型名是 auto/xxx → createVirtualAutoCombo (autoRouting.ts)
 └─ handleComboChat (combo.ts:560)
     ├─ pinnedModel dispatch（prompt-cache pin）
     ├─ tryFusionDispatch（combo 引用组合）
     ├─ chaos（并行多模型，opt-in）
     ├─ runtimeUnit / round-robin 特例（handleRoundRobinCombo）
     └─ 主循环: resolveComboTargetPipeline (targetResolution.ts:668)
         └─ orderByStrategy (targetResolution.ts:407)
             ├─ strategy=="auto" → resolveAutoStrategyOrder (resolveAutoStrategy.ts:93)
             └─ 其他 16 种 → applyStrategyOrdering (applyStrategyOrdering.ts)
         └─ executeTarget 逐个尝试（熔断/冷却/超时/重试/接替）
```

关键：**combo 的 models 显式列表就是候选池**。`expandAutoComboCandidatePool` 只有在 `models` 为空（纯 auto 无显式列表）时才扩展到全量 provider；日常配置「显式列 15 个模型」= 这 15 个就是候选池。

---

## 3. auto 策略：完整决策链路

`resolveAutoStrategyOrder(deps)` 顺序（resolveAutoStrategy.ts:93）：

### 3.0 候选池构建（buildAutoCandidates, combo.ts:297）
为每个候选模型注入运行时数据：
- `quotaRemaining`（配额剩余 %）
- `circuitBreakerState`（熔断状态 CLOSED/HALF_OPEN/OPEN）
- `costPer1MTokens`（每百万 token 成本）
- `p95LatencyMs`、`latencyStdDev`（95 分位延迟 + 抖动）
- `errorRate`（错误率）
- `speedTelemetry`（速度遥测）
- `accountTier`（账户档位，**当前硬编码 "standard"**）
- `quotaResetIntervalSecs`（配额重置周期，**当前硬编码 86400**）
- `contextAffinity`、`resetWindowAffinity`、`connectionPoolSize`

连接状态分类：`credits_exhausted` / `rate_limited` / `banned` / `expired` → 进 `quotaCutoffBlocked` 或 `statusPenalty`。

### 3.1 前置过滤
1. **工具调用兼容过滤**（请求带 tools 时）：候选模型不支持工具 → fail-closed 排除（可 fail-open，默认 closed，#8488）。
2. **上下文窗口预过滤**：估算 tokens（约 4 chars/token），超过候选模型 context limit → 排除（#1808）。

### 3.2 配额 cutoff（**opt-in，默认关闭**）
`resilienceSettings.quotaPreflight.enabled===true` 才启用：
- 阈值：`defaultThresholdPercent=2`（硬 cutoff 线）、`warnThresholdPercent=20`（预警线）；可按 provider/connection 覆盖。
- cutoff 模式下配额耗尽候选直接被排除；非 cutoff 模式则只做**软惩罚**：
  - `QUOTA_SOFT_DEPRIORITIZE_FACTOR=0.7`（配额不足的候选最终分 ×0.7）
  - `STATUS_SOFT_DEPRIORITIZE_FACTOR`（状态异常的候选降权）
- 参考文件：`combo/quotaExhaustionCutoff.ts`。

### 3.3 打分：13 维加权（scoring.ts）
`score = Σ(factor_i × weight_i)`，clamp 到 [0,1]。默认权重与因子：

| 维度 | 默认权重 | 因子公式 |
|---|---|---|
| quota | 0.15 | quotaRemaining / 100 |
| health | 0.20 | CLOSED=1.0 / HALF_OPEN=0.5 / OPEN=0.0 |
| costInv | 0.15 | 1 − cost/maxCost（越便宜越高） |
| latencyInv | 0.12 | 1 − p95/maxLatency（越快越高） |
| taskFit | 0.08 | getTaskFitness(model, taskType)（模型任务匹配度） |
| stability | 0.05 | 1 − stdDev/maxStdDev（延迟越稳越高） |
| tierPriority | 0.05 | calculateTierScore：ultra 1.0/pro 0.67/standard 0.33/free 0；resetBonus=max(0, 1−resetInterval/2592000)；最终 min(1, base×0.8 + resetBonus×0.2) |
| tierAffinity | 0.05 | 与 hint.recommendedMinTier 匹配：同档 1.0/差一档 0.7/其他 0.3 |
| specificityMatch | 0.05 | free:≤15$→0.9 否则 0.2；cheap:15-50→0.9 否则 0.4；premium:>50→0.9 否则 0.3 |
| contextAffinity | 0.05 | 候选自带 ?? 0.5 |
| cacheAffinity | 0 | prompt cache 亲和 ?? 0 |
| resetWindowAffinity | 0 | 配额重置窗口亲和 ?? 0.5 |
| connectionDensity | 0.05 | (poolSize−1)/10 |

**可见性：权重里 health 最重（0.2），其次是 quota/cost（各 0.15）、latency（0.12）。即"健康 > 便宜 > 快"是默认取向。** 但台账 4 个显式配置的 combo 用的 modePack=ship-fast 直接把 latencyInv 提到 0.32，改成"健康 > 快 > 便宜"。

### 3.4 选择机制（engine.ts selectProvider）
```
taskType 默认时 → classifyPromptIntent(最后一条 user 消息)
     code / reasoning / simple / medium
→ modePack 权重覆盖（见 §5）
→ 过滤：candidatePool 白名单 + SelfHealingManager.evaluate 排除
→ scorePool 打分
→ self-healing 二次评估
→ incidentMode 时 explorationRate=0（故障期不探索）
→ 选择：
   ├─ 命中探索（explorationRate，默认 0.05）→ 随机选一个
   └─ 否则 ScoreTierRotator.pick（§3.5）
→ budgetCap：超出预算 → 预算内候选里 rotator 选；无候选 → cheapest 或
   budgetFallback:"strict" 抛 BudgetExceededError
```

### 3.5 ScoreTierRotator：分档加权轮换
- 候选按分数分 **top / mid / rest** 三档。
- `TIER_PREFERENCES`（按 combo 名匹配权重向量，默认取 default）：
  - smart{0.5, 0.3, 0.2} / fast{0.3, 0.5, 0.2} / cheap{0.2, 0.3, 0.5} / coding{0.6, 0.25, 0.15} / default{0.45, 0.35, 0.2}
  - 含义：smart 倾向 top 档（高质量优先），fast 倾向 mid 档（速度优先），cheap 倾向 rest 档（便宜优先）。
- `CLEAR_WINNER_THRESHOLD=0.1`：**第一名比第二名分差 ≥0.1 时直接选 top 档**（明确赢家不轮换）；否则按档位偏好概率选档，再在同档内轮换。
- 效果：最优候选不被打满，同档候选分摊流量；分差大时又不浪费最优。

### 3.6 排序输出
`resolveAutoStrategyOrder` 返回顺序：**`[selectedTarget, ...rankedTargets, ...eligibleTargets]`**（去重）。
- selectedTarget：打分选出的最优；
- rankedTargets：其余按分数降序；
- eligibleTargets：通过过滤但没进 ranked 的兜底。
→ executeTarget 按此顺序尝试，**第一个成功即返回**。

---

## 4. 5 种 routerStrategy（combo 级覆盖选）

`combo.routingStrategy` 非 rules 时，跳过 selectProvider 改用 `selectWithStrategy`：

| strategy | 机制 |
|---|---|
| **rules**（默认） | 6 因子 scorePool（health/error/cost/latency/taskFit/stability），排除 OPEN 熔断 |
| **cost** | 健康候选中 `costPer1MTokens` 最小 |
| **latency** | rankBySpeed（速度排序） |
| **sla-aware** | latency×0.35 + errorRate×0.35 + health×0.15 + cost×0.1 + stability×0.05；配 hardConstraints 时按 violationScore 排序（p95/errorRate/cost 越界归一化累加） |
| **lkgp** | lastKnownGoodProvider 非 OPEN 则最优先，否则退回 rules |

台账中 4 个显式配置 combo 全部 `routerStrategy=rules`，其余走默认（rules）。

---

## 5. 4 种 modePack（权重包，按任务类型覆盖）

| modePack | quota | health | costInv | latencyInv | taskFit | stability | tierPriority | contextAffinity | connectionDensity |
|---|---|---|---|---|---|---|---|---|---|
| **ship-fast**（台账在用） | 0.14 | 0.28 | 0.05 | **0.32** | 0.10 | 0 | 0.05 | 0.01 | 0.05 |
| **cost-saver** | 0.14 | 0.19 | **0.37** | 0.05 | 0.10 | 0.05 | 0.05 | — | 0.05 |
| **quality-first** | 0.10 | 0.18 | 0.05 | 0.05 | **0.37** | 0.15 | 0.05 | — | 0.05 |
| **offline-friendly** | **0.37** | 0.28 | 0.10 | 0.05 | 0 | 0.10 | 0.05 | — | 0.05 |

意图分类 code/reasoning/simple/medium 后套用对应 modePack（代码→quality-first 倾向等）。

---

## 6. 非 auto 的 16 种排序策略（applyStrategyOrdering.ts）

| 策略 | 机制 | 适用场景 |
|---|---|---|
| priority | 按配置顺序/priority 排序，顺序尝试 | 显式主备（= octopus Failover） |
| **lkgp** | 上次成功 provider 提到最前 | 粘性成功通道 |
| strict-random | 无放回 deck 抽牌（key=combo:name）+ 余下洗牌 | 均匀分摊且不重复命中 |
| random | fisherYates 全洗 | 纯随机 |
| fill-first | 保持 priority 顺序 | 与 priority 类似 |
| **p2c** | Power-of-Two-Choices（随机取 2 个比并发，选并发低者） | 负载均衡最常用的好算法 |
| **least-used** | 按请求量升序（用最少的最优先） | 平衡流量 |
| cost-optimized | 按成本升序 + manifestRouting require-premium 过滤 | 省钱 |
| context-optimized | 按上下文窗口降序（大的优先） | 大上下文请求 |
| cache-optimized | prompt-cache 亲和 | 命中缓存省钱提速 |
| **headroom** | 空闲容量最多者优先 | 防单点过载 |
| reset-aware / reset-window | 配额重置窗口感知 | 卡点在配额重置前的组合 |
| **quota-share** | DRR（Deficit Round Robin）+ P2C in-flight + per-model bucket + per-connection 并发门控 | 精细流量配额 |
| weighted | 按权重概率 | = octopus Weighted |
| context-relay | 会话上下文转发（Global→Provider→Combo 3 层配置级联） | 延续上下文 |

---

## 7. executeTarget：失败转移链（combo.ts:890+）

```
按排序依次取 target：
 1. circuit breaker OPEN      → 跳过（熔断中）
 2. providerCooldown 生效中   → 跳过（指数退避冷却中）
 3. preScreenMap 预检         → 过滤
 4. exhaustedProviders/Connections → 跳过（用尽）
 5. predictive TTFT 熔断（config.predictiveTtftMs）→ 过快失败阈值防重试风暴
 6. 同 target 失败 → 按 retryDelayMs 重试（maxRetries 次）
 7. handleSingleModelWithTimeout（per-target 超时 DEFAULT_COMBO_TARGET_TIMEOUT_MS=120s）
 8. 成功 → 响应质量验证（handoffThreshold=0.85，质量低视为失败）
 9. 失败 → 下一个 target（i+1）
全部失败 → 全局 fallback provider / 报错
```

**冷却（resilience settings）**：
- `comboCooldownWait`：429 后短暂等待再重试（#7360）；
- `providerCooldown`：`minRetryCooldownMs × 2^(failures−1)` 指数退避，`maxRetryCooldownMs` 封顶；
- `connectionCooldown`：按 auth category 冷却。

**并发预检**：`PRE_SCREEN_CONCURRENCY=5` 并行预检候选，减少串行等待。

**autoPromote**：成功后把赢家模型提到组合最前（对后续请求形成「最近成功优先」正反馈，配合 lkgp）。

---

## 8. 与 octopus 现状对照 & 移植建议

### octopus 现有能力（internal/relay/balancer/）
| 能力 | octopus 现状 | omni 对应 |
|---|---|---|
| 模式 | RoundRobin / Random / Failover(priority) / Weighted（balancer.go） | priority/random/weighted |
| 熔断 | 全局熔断器，Closed/HalfOpen/Open，TripCount 指数退避（circuit.go） | circuitBreakerState 因子 |
| 粘性 | SessionKeepTime 粘性通道前置（session.go / iterator.go） | lkgp / session affinity |
| 并发 | 全局并发计数（concurrency.go） | connectionPoolSize / p2c in-flight |
| 限速 | channel 级限速（rate.go） | quota / rate_limited |
| 重试 | retry_enabled + max_retries（分组配置） | maxRetries + retryDelayMs |
| 首字超时 | first_token_time_out 分组配置 | per-target timeout |
| 健康探测 | 分组 health probe（group_health.go） | preScreen / self-healing |

### octopus 缺失（可移植性判断）

**A 类：纯排序逻辑，无新数据依赖，移植成本低、收益高** ⭐（已移植，见 §9）
| 项 | 说明 | 移植方式 | 状态 |
|---|---|---|---|
| **健康优先排序** | 候选按「熔断状态 + 最近失败/成功」排序，而非纯 priority | 新增 `GroupModeHealthFirst`(5) | ✅ 已移植（mode 5） |
| **同档加权轮换** | 最优几个候选同档内轮换，避免盯死最优 | 折叠进 HealthFirst 的同档轮换（不单独成模式） | ✅ 已移植（mode 5 内） |
| **lkgp（最近成功优先）** | 上次成功的通道提到最前 | 无需新增——octopus 现有 `Group.SessionKeepTime` 粘性就是 lkgp | ✅ 已等价（SessionKeepTime） |
| **least-used** | 按请求计数升序（octopus 有全局并发计数） | 新增 `GroupModeLeastUsed`(6)，用实时在途并发 | ✅ 已移植（mode 6） |
| **p2c** | 随机取 2 个比并发，选低的 | 新增 `GroupModeP2C`(7) | ✅ 已移植（mode 7） |
| **strict-random** | 无放回抽牌，比纯随机更均匀 | 新增 `GroupModeStrictRandom`(8) | ✅ 已移植（mode 8） |

**B 类：需要新增数据采集，成本中等** 
| 项 | 说明 | 移植方式 |
|---|---|---|
| **延迟打分** | 需要 p95 延迟统计（现 relay_logs 有耗时，可聚合） | 定时聚合 relay_logs → 内存延迟表 |
| **成本打分** | 需要每通道成本（relay_logs 有 cost 字段，可聚合） | 同上 |
| **任务类型感知** | 需要 prompt 意图分类（code/reasoning/simple） | 可选，浅分类器 |
| **配额感知** | 需要通道配额剩余（渠道方 API 数据，现无） | 暂不可行，除非接入渠道统计 |

**C 类：不建议移植（边界条件不匹配或收益小）**
| 项 | 说明 |
|---|---|
| quota cutoff（opt-in） | octopus 拿不到渠道真实配额，硬编码 standard 意义有限 |
| context-relay | 是转发层能力，非排序策略 |
| tierPriority/specificityMatch | 依赖账户档位元数据，octopus 无此概念 |
| 16 种策略全量 | 大部分是配置丰富度，核心就那几种 |

### 推荐落地组合（供决策）
> 纯排序层做 4 件事，即可覆盖 omni 80% 的路由价值：
> 1. **Failover 增强为健康优先排序**（熔断 Open 排最后、半开/冷却排后、最近失败降权）——不新增任何数据；
> 2. **新增 least-used / p2c 两个策略**（数据已有：全局并发计数）；
> 3. **同档轮换**：让最优的 2-3 个候选按权重轮换，避免盯死；
> 4. **最近成功缓存（lkgp）**：挂在现有 sticky 机制上，成功通道短期优先。

> 延迟/成本打分（B 类）建议后续叠加：聚合 relay_logs 的耗时与 cost，注入排序。

---

## 附：源码文件索引
| 文件 | 内容 |
|---|---|
| `src/sse/handlers/chat.ts:239` | handleChat 入口 |
| `src/sse/handlers/autoRouting.ts` | auto 模型名 → 虚拟 combo |
| `open-sse/services/combo.ts:560` | handleComboChat 主循环 |
| `open-sse/services/combo/targetResolution.ts:407,668` | orderByStrategy / resolveComboTargetPipeline |
| `open-sse/services/combo/resolveAutoStrategy.ts:93` | auto 策略决策链 |
| `open-sse/services/autoCombo/scoring.ts` | 13 维打分 |
| `open-sse/services/autoCombo/engine.ts` | selectProvider + ScoreTierRotator |
| `open-sse/services/autoCombo/routerStrategy.ts` | 5 种 routing strategy |
| `open-sse/services/autoCombo/modePacks.ts` | 4 种权重包 |
| `open-sse/services/combo/applyStrategyOrdering.ts` | 16 种非 auto 排序 |
| `open-sse/services/combo/quotaExhaustionCutoff.ts` | 配额 cutoff 阈值 |
| `src/lib/resilience/settings/types.ts` | 冷却/退避配置 |

---

## 9. A 类移植落地记录（2026-08-12）

已将 A 类 4 个策略移植为 octopus 的 4 个新 `GroupMode`，**非破坏**（旧 4 模式行为不变，分组各自 opt-in）：

| mode | enum | omni 对应 | 数据源（进程内已有，无新采集） | 实现文件 |
|---|---|---|---|---|
| 5 | `GroupModeHealthFirst` | auto 健康排序 + ScoreTierRotator 同档轮换 | `outlierwindow.Evaluate`（失败率/连续失败/最近成功） | `internal/relay/balancer/health_order.go` |
| 6 | `GroupModeLeastUsed` | `sortTargetsByUsage` | `CurrentChannelConcurrency`（实时在途并发，替代历史请求数） | `internal/relay/balancer/balancer.go` |
| 7 | `GroupModeP2C` | `orderTargetsByPowerOfTwoChoices` | `CurrentChannelConcurrency` | `internal/relay/balancer/balancer.go` |
| 8 | `GroupModeStrictRandom` | `getNextFromDeck`（无放回抽牌） | per-group deck（指纹键，无需 groupID） | `internal/relay/balancer/strict_random.go` |

**算法忠实度**：每个实现按对应 omni 源函数的算法重写（Go ≠ TypeScript，非字面 copy）。HealthFirst 用 `outlierwindow` 成败窗口代替 omni 13 维中不可得的 p95/cost/quota（B 类数据）；落在 A 类边界内。同档轮换的「不盯死最优」复刻 ScoreTierRotator 的 A 类本质，不含其 taskFit/modePack/bandit 探索（B 类）。

**lkgp 等价说明**：omni `lkgp`（最近成功 provider 置首）= octopus 现有 `Group.SessionKeepTime` 粘性会话（`balancer/iterator.go:52-69` + `session.go`），per-(apiKeyID,modelName) 最近成功通道前置。无需新增。

**未移植（B 类，待数据采集）**：完整 ScoreTierRotator 13 维打分（latencyInv/costInv/quota/taskFit 等）、routerStrategy 5 档、modePack 4 档——需先聚合 relay_logs 的耗时与 cost 到内存，再议。

**测试**：`internal/relay/balancer/{health_order,least_used,p2c,strict_random}_test.go`，覆盖健康排序/同档轮换/冷启动中性/并发升序/tie-break/P2C 集合完整/无放回不重复/指纹稳定。`go test ./internal/relay/balancer/...` 通过。

**前端**：picker 加 4 选项（`Card.tsx` 快切 + `Editor.tsx` 编辑），i18n 三语（en/zh_hans/zh_hant）补 `mode.healthFirst/leastUsed/p2c/strictRandom`。

