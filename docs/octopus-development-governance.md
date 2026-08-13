# Octopus 开发治理

本文件把根目录 `AGENTS.md` 的规则落实为可执行的开发手册，回答“修改什么、在哪里改、怎么改、
最低验证是什么、哪些做法禁止”。本手册只记录能由当前代码、Git、CI、生产状态或已复盘事故
确认的事实，不把历史目录、旧 tag 或口头推测写成现状。

## 规则优先级与真值

规则冲突时按以下顺序处理：

1. 根目录 `AGENTS.md`；
2. 本手册；
3. `docs/octopus-production.md` 中与生产、候选、数据和回滚有关的约束；
4. README、USAGE 和历史记录。

运行事实冲突时，以当前代码、Git 对象、`deploy/fwq57ys/production-state.json` 和 Docker
inspect 证据为准。以下四项必须分别记录，不得相互推导：

1. 当前 `main` commit；
2. 运行应用源码 commit；
3. 运行镜像 tag / image ID；
4. 运行容器 ID。

`main` 更新、Release 成功、镜像已存在和容器已切换是四个不同状态。

## 当前生产台账

`deploy/fwq57ys/production-state.json` 是当前生产运行指纹的唯一机器可读台账。最近一次已核验的
生产版本为 `v0.10.2-mumu.19`：应用源码提交为
`947e5e477c8b66d9f677783d475bc3f3fb5dd642`，生产状态提交为
`7effc8a0ced6ca3b35640ce896bbbfd4b71c1ee2`，运行镜像 ID 为
`sha256:31787cf29681bd2e884f567f8318af7c6c26d25701e5cf18f82516b1c4141810`，容器 ID 为
`c76ad77100fd85358eef8b539e33def1c63d4c15585589207a78ae12f8a0c0c9`。

本次切换后台任务 `v0.10.2-mumu.19-cutover-20260813T030000Z` 已完成；候选容器
`octopus-candidate-19` 及其候选数据副本（`octopus-candidate-18`、`octopus-candidate-19`、
`octopus-candidate-ci19`）已按授权精确清理。随后按授权收整：历史回滚容器 `.12`、`.13`
已删除，仅保留 `.17` 正式回滚容器作为安全网；历史版本与事故快照（`.11`、`.12`、`.13`、
`.17`、`pre-relay-content-null`、`pre-stale-container-cleanup`）已清理，仅保留一份最新的
`.19` 回滚快照。生产容器、生产 SQLite 均未删除。

| --- | --- | --- | --- |
| 唯一源码 | `/home/yangs/API/octopus-mumu/` | 开发、测试、构建、提交 | 不在第二份源码中继续工作 |
| 生产控制面 | `/home/yangs/API/octopus/` | Compose 副本、真实数据、备份、部署证据 | 不初始化 Git、不放源码、不构建 |
| 生产数据 | `/home/yangs/API/octopus/data/` | 仅获批的生产读写 | 不用于开发、单测或候选 |
| 历史源码 | `/home/yangs/API/octopus-src*` | 只读追溯 | 不恢复旧改动、不构建、不部署 |
| 构建缓存 | `/home/yangs/API/octopus-build-cache/` | 只作缓存 | 不视为源码或发布证据 |
| 生产声明 | `deploy/fwq57ys/compose.yaml` | 版本控制的目标 Compose | 不直接挂候选数据 |
| 发布目标与运行指纹 | `deploy/fwq57ys/production-state.json` | staging 记录目标 release/image，切换后记录 live 指纹 | 不把 staging 状态声称为已运行 |

仓库根目录没有通用生产 `docker-compose.yml`。开发环境必须使用独立配置和数据，不得为了
“方便”复用生产控制面。

## 修改路由

先按变更类型找到主文件，再检查同一行中的联动区域。Handler 不应绕过 `internal/op/`
直接实现业务规则；模型、迁移、备份和恢复必须作为一个数据契约审查。

| 变更类型 | 主要修改位置 | 必须同步检查 |
| --- | --- | --- |
| 配置、默认值、版本信息 | `internal/conf/` | `cmd/`、`web/src/lib/info.ts`、配置文档；发布时再检查版本矩阵 |
| 数据模型、索引、迁移 | `internal/model/`、`internal/db/migrate/` | `internal/db/`、`internal/op/backup.go`、迁移/备份测试 |
| 业务规则、缓存、查询 | `internal/op/` | 调用它的 Handler、缓存失效、并发和事务边界 |
| 管理 API、路由、响应 | `internal/server/handlers/`、`internal/server/router/` | `internal/server/resp/`、鉴权、中间件、`web/src/api/` |
| Relay、负载均衡、协议 | `internal/relay/`、`internal/protocolroute/`、`internal/transformer/` | Chat、Responses、Anthropic、流式/非流式、重试和计量路径 |
| 站点/渠道同步 | `internal/sitesync/` | 渠道模型、管理 API、同步报告和对应前端 |
| 价格与费用 | `internal/price/`、`scripts/updatePrice.py` | 文本、Responses、图片、输入估算和历史数据边界 |
| 三维统计与排行榜 | `internal/model/stats_leaderboard.go`、`internal/op/stats_leaderboard*.go` | `internal/relay/*metrics.go`、备份恢复、统计 Handler、Web 排行榜和覆盖提示 |
| Web API | `web/src/api/` | 后端响应契约、React Query 缓存键和错误/空状态 |
| Web 页面、状态、路由 | `web/src/components/modules/`、`web/src/stores/`、`web/src/route/`、`web/src/app/` | 三套 locale、桌面/窄屏、键盘、加载/空/错误状态 |
| 前端依赖 | `web/package.json`、`web/pnpm-lock.yaml` | `web/pnpm-workspace.yaml`、`Dockerfile.build`、CI pnpm 版本 |
| 生产构建 | `Dockerfile.build`、`scripts/build-production-image.sh` | OCI labels、固定摘要、源码 tree、前后端完整构建 |
| CI 与 Release | `.github/workflows/` | tag 触发、权限、GHCR、归档、治理 job |
| 生产声明 | `deploy/fwq57ys/compose.yaml`、`production-state.json` | 只在发布/部署各自阶段修改；不得把文本变更写成已部署 |
| 治理文档与守卫 | `AGENTS.md`、两份 Octopus 手册、`scripts/check-governance.sh` | README、USAGE、`CLAUDE.md` 的入口链接 |

### 发布与部署字段矩阵

当前流程分为三个提交/状态阶段，不得合并描述：

1. **应用源码与 Release**：功能源码 commit 经测试后创建新 tag；`Dockerfile.build` 通过
   `GIT_VERSION` 注入发布版本，因此 tag 所指源码中的默认版本字段可以仍是当前生产版本。
2. **部署 staging**：Release/GHCR 成功后，单独提交同步
   `internal/conf/version.go`、`web/package.json`、`web/src/lib/info.ts`、受管 Compose，
   并把 `production-state.json` 的目标 release/image 更新为新版本。此时 live 容器 ID、
   StartedAt 等字段仍是旧生产；`--repo` 应通过，`--live` 应因尚未切换而不通过。
3. **切换后运行证据**：后台切换成功后按 Docker inspect 更新 container ID、StartedAt、
   restart count、回滚快照等 live 字段，再提交运行状态；此时 `--repo` 和 `--live` 都应通过。

Release tag 指向应用源码 commit，不指向后续部署 staging commit。任何汇报必须标明当前处于
“源码/Release”“staging 待切换”还是“live 已核验”，不得把 staging 文件内容说成已部署。

## 通用开发流程

### 开始

```bash
cd /home/yangs/API/octopus-mumu
scripts/check-governance.sh --repo
git status --short --branch
git fetch origin
git switch -c codex/<topic> origin/main
```

工作树不干净、目录不是规范源码、`origin/main` 无法确认时停止。不得新建第二个克隆规避问题。

### 实现

1. 一个分支只处理一个目标；先写或补能暴露问题的测试。
2. 只改“修改路由”列出的必要文件，不顺带搬运历史分支或 `.10` 实现。
3. 数据结构变化同时处理迁移、备份/恢复和旧数据兼容。
4. 协议变化覆盖流式、非流式、失败、重试和计量，不只测成功响应。
5. Web 变化复用现有组件与状态模式，同时处理加载、空、错误、窄屏和键盘操作。
6. 每个提交保持可审查；数据库、凭据、构建产物、缓存和候选数据不得入 Git。

### 推送与晋级

```bash
git push -u origin codex/<topic>
# 等待该 SHA 的 GitHub CI 全部成功并完成审查
git switch main
git merge --ff-only codex/<topic>
OCTOPUS_MAIN_PROMOTION=1 git push origin main
```

只允许普通快进。禁止 `--no-verify`、force push、移动公开 tag、rebase 已发布主线或把失败
CI 的 SHA 晋级。发布和部署另行授权，合并 `main` 不自动触发生产切换。

## 按改动类型验证

下表是最低门禁，不替代针对缺陷新增的测试。fwq57ys 宿主当前没有 `go` 命令；
`go: command not found` 不是通过证据，后端全量测试必须由 GitHub CI 或等价的固定 Go 1.25
环境完成。

| 改动 | 最低本地/固定环境验证 | 额外证据 |
| --- | --- | --- |
| 仅文档/治理脚本 | `bash -n scripts/check-governance.sh`；`scripts/check-governance.sh --repo` | 新旧路径搜索、主题分支 governance CI |
| 普通 Go 逻辑 | 相关 package 测试；`go test -buildvcs=false ./...` | 失败用例先失败、修复后通过；backend CI |
| 模型/迁移/备份 | `go test -buildvcs=false ./internal/db/... ./internal/op/...`；全量 Go 测试 | 旧库副本迁移、导入导出、`quick_check` |
| Relay/协议 | 相关 `internal/relay/`、`protocolroute/`、`transformer/` 测试；全量 Go 测试 | 受影响协议的流式/非流式和失败重试样例 |
| 三维统计 | `go test -buildvcs=false ./internal/op ./internal/relay ./internal/server/handlers`；全量 Go 测试 | 独立数据副本回填；模型/最终渠道/请求分组逐项对账 |
| Web/API | `pnpm install --frozen-lockfile`、`pnpm lint`、`pnpm build`（均在 `web/`） | 桌面和 320px/390px 窄屏、键盘、空/错误状态截图或记录 |
| 构建/依赖/Release | shell 语法、治理守卫、前后端全量、使用计划中的新版本做完整旁路镜像构建 | OCI version/revision/source tree/build time；无生产挂载 |
| Compose/状态清单 | `docker compose -f deploy/fwq57ys/compose.yaml config`、`--repo` | 获批部署后再运行 `--live` 并提交真实 inspect 指纹 |

UI 或协议改动不能只以“编译通过”验收；统计或迁移不能只以“新表存在”验收。

## 价格与费用契约

价格单位为美元/百万 Token，当前统一规则为：

1. `actual_model_name` 有非零精确价格时，使用实际模型价格；
2. 实际模型缺价或只有全零占位时，回退到 `request_model_name` 的请求分组官方价格；
3. 请求分组明确为零价时保持零，不臆造价格；
4. 文本、Responses、输入估算兜底和图片路径必须使用同一规则；
5. 当前只有 `codex-auto-review`（无公开定价）和
   `sensenova-6.7-flash-lite`（免费方案）明确保留零价。

历史日志不会随价格表自动重算。价格数据刷新和历史费用回填是两个独立任务：前者只有显式
`UPDATE_PRICE_DATA=1` 才执行并必须先审查生成差异；后者需要生产快照、数据范围和审计，
不得通过直接写 SQLite 顺带完成。

## 明确禁止

- 禁止在 `main`、生产控制面、历史源码或 build-cache 中开发。
- 禁止从旧 tag、backup 分支、失败发布或隔离分支复制整套实现覆盖当前主线。
- 禁止直接修改生产 SQLite 来验证代码；候选只能挂独立一致性副本。
- 禁止 Handler 直接复制一套与 `internal/op/` 不一致的业务规则。
- 禁止协议转换器发明上游未要求的字段，或只凭单一供应商成功响应判定兼容。
- 禁止只更新一个版本字段、移动既有 tag 或用同名镜像覆盖旧构建。
- 禁止把 `latest`、上游基础镜像、Docker Hub 同名镜像或本地测试镜像当生产镜像。
- 禁止设置 `UPDATE_PRICE_DATA=1` 顺带刷新价格；价格更新必须独立审查差异。
- 禁止因宿主缺少 Go/pnpm 而跳过测试并把静态阅读写成验证通过。
- 禁止未获维护窗口授权时执行生产容器生命周期命令或生产数据写入。
- 禁止在当前代理 API 所依赖的前台 SSH 会话中 stop/restart/recreate Octopus。

## 已知缺陷与历史坑

| 现象 | 已确认原因 | 正确做法 | 禁止的错误处理 |
| --- | --- | --- | --- |
| `.10` 候选累计总表有 30,215 请求/3,990,045,329 Token，渠道/分组新表各只有 3 请求/657 Token | `stats_dimension*` 没有完整历史迁移/回填，且遗漏独立计量写入路径 | 以 `.9` 行为基线重做 `stats_leaderboard*`；覆盖状态必须 `completed`，三维成功、失败、输入/输出 Token、费用逐项一致 | 复用 `.10` tag、表、写入路径，或只检查页面能显示 |
| Responses 上游报 `unknown_parameter`，工具调用链中断 | 曾自动合成或重写 `function_call_output.item_reference` | 保留类型化 item ID 规范化，但 `item_reference` 仅按协议和真实输入透传，并用供应商兼容样例回归 | 为“补全”字段而发明引用值 |
| 前端 Docker 安装阶段找不到/拒绝构建原生依赖 | pnpm 版本或原生依赖许可文件未同步进入构建上下文 | 以 `web/package.json` 的 `packageManager` 为准；安装前复制 lockfile 和 `web/pnpm-workspace.yaml` | 在 Dockerfile 单独升级 pnpm，或删除 allowBuilds |
| 构建时价格表意外变化 | 设置了 `UPDATE_PRICE_DATA=1` 会刷新仓库价格数据 | 默认使用已提交价格；价格任务先审查和提交差异，再构建 | 发布功能时顺带刷新价格 |
| 代码、Web、Compose 或状态清单看似混合新旧值 | 应用源码、部署 staging 和 live 指纹是三个阶段 | 按发布与部署字段矩阵分阶段同步；staging 只声称“待切换”，切换后再写真实 inspect | 把 staging 状态说成已运行，或强行让 tag 指向部署提交 |
| 新改动被误判为 `go vet` 回归 | `.9` 基线在 `internal/relay/protocol_attempt.go:187` 已有 copy-lock 告警 | 单独记录基线和新增告警；当前任务仍须通过规定的 Go tests/CI | 把既有告警说成本轮修复，或用它解释所有失败 |

## 停止条件

出现任一情况立即停止写入或晋级，先恢复事实：

- 当前目录不是唯一源码，或工作树有来源不明的修改；
- 目标不是从最新 `origin/main` 建立，或需要合并未审查的隔离进度；
- 测试无法运行且没有 GitHub CI/固定环境的等价证据；
- 版本、tag、source commit、OCI revision 或 source tree 不一致；
- 数据结构变化没有迁移、备份/恢复和旧数据验证；
- 统计三维不一致、coverage 未完成或部分覆盖未明确展示；
- 候选触碰生产数据、生产端口或生产容器名；
- 任务会触发生产生命周期/SQLite 写入但没有明确维护窗口；
- 没有可验证快照、后台切换脚本或自动回滚。

停止不是改用旧目录、旧镜像或本地重建绕过门禁。

## 交付证据

每次交接至少写清：

```text
任务与范围：
源码目录：/home/yangs/API/octopus-mumu
分支 / HEAD / origin SHA：
当前 main / 运行应用源码：
运行镜像 tag / image ID / 容器 ID：
修改文件与修改理由：
新增或修改的测试：
实际执行命令与结果：
GitHub CI / Release URL：
是否部署：否 / 是（维护窗口与后台任务证据）
数据与容器操作：
快照 / 回滚：
明确未修改：
临时资源清理：
已知限制与停止项：
```

没有部署时必须明确写“未部署”，不能用“已合并”“Release 成功”代替。
