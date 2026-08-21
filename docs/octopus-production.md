# Octopus 生产部署手册

本文件是 fwq57ys 上 Octopus 的唯一现行生产手册，回答“生产对象分别负责什么、候选怎么验、
切换怎么做、什么必须禁止、失败时如何停止和回滚”。仓库最高规则见 `AGENTS.md`，开发修改
路线见 `docs/octopus-development-governance.md`，机器可读运行真值见
`deploy/fwq57ys/production-state.json`。

历史版本、旧目录、旧 Compose、旧 tag 和 Git 日志只用于追溯，不能据此选择生产。

## 职责与边界

| 对象 | 规范位置/名称 | 职责 | 使用边界 |
| --- | --- | --- | --- |
| GitHub | `mumu-140/octopus-concurrency` | 远端源码、CI、Release、GHCR | 不移动公开 tag，不重写 `main` |
| 规范源码 | `/opt/octopus-mumu/` | 唯一开发、测试、构建、提交入口 | 不放真实数据，不直接承担生产运行 |
| 受管 Compose | `deploy/fwq57ys/compose.yaml` | 生产目标声明 | 只声明精确镜像、host 网络和正式挂载 |
| 发布目标与运行状态 | `deploy/fwq57ys/production-state.json` | staging 目标 release/image + 切换后 live 指纹 | 必须标明阶段，不把 staging 声称为已运行 |
| 生产控制面 | `/opt/octopus/` | Compose 副本、真实数据、备份、部署日志 | 不是源码仓库，不构建 |
| 生产数据 | `/opt/octopus/data/` | `config.json`、SQLite 和运行数据 | 只挂生产容器；候选不得使用 |
| 生产容器 | `octopus` | 对外正式服务 | 不兼任候选或回滚容器 |
| 回滚容器 | 状态清单声明的精确名称 | 保留上一验证版本的可启动容器 | 不常态运行，不改名冒充候选 |
| 候选容器 | `octopus-candidate-<version>` 或任务声明的唯一名 | 独立端口、独立数据副本验收 | 不占 35276，不挂生产数据，不自动晋级 |
| 历史目录 | `octopus-src*`、`octopus-build-cache` | 只读现场/缓存 | 不开发、不构建、不发布、不部署 |

`octopus-mumu/` 和 `octopus/` 的职责必须保持分离。不要合并目录，也不要把大型生产数据移入
源码仓库或 Docker build context。

## 操作前真值核验

每次 Octopus 任务先执行以下只读步骤：

```bash
cd /opt/octopus-mumu
git status --short --branch
git rev-parse HEAD origin/main
jq '.repository, .production' deploy/fwq57ys/production-state.json
scripts/check-governance.sh --repo
scripts/check-governance.sh --live
```

随后分别记录：

- 当前 `main` commit；
- 运行应用源码 commit；
- 运行镜像 tag 和 image ID；
- 生产容器 ID、启动时间和 restart count；
- Compose 副本与受管 Compose 是否逐字一致；
- host 网络、`/opt/octopus/data:/app/data` 挂载和 HTTP 状态；
- 状态清单声明的回滚容器/快照是否真实存在。

目录名、最新 commit、镜像名或容器名中的任意一个都不能单独证明生产身份。只读核验失败时
先查明漂移，不执行 pull、tag、Compose 更新或容器生命周期操作。

### 三阶段状态模型

1. **源码/Release**：tag 指向通过测试的应用源码 commit；构建参数注入新版本。
2. **staging 待切换**：独立部署提交把默认版本、受管 Compose 和状态清单的目标
   release/image 更新为新版本，但 container ID、StartedAt 等 live 字段仍记录旧生产。
   此时 `--repo` 应通过，`--live` 应因目标镜像尚未运行而不通过；这不是部署完成。
3. **live 已核验**：后台切换后按 inspect 更新所有 live 字段和回滚快照，提交运行状态；
   此时 `--repo`、`--live` 均通过。

初始任务核验在 staging 前运行 `--repo` 与 `--live`。进入 staging 后，切换 preflight 使用
`--repo` 加“旧容器精确身份”断言；不能要求 staged 文件与旧容器相等，也不能把预期的
`--live` 不一致当成可忽略的最终状态。

## 当前生产真值

以下值于 2026-08-10 通过治理守卫、Docker inspect、SQLite `quick_check` 和独立公网连接核验。
它们用于识别当前基线，不替代每次操作前的实时核验。

| 项目 | 值 |
| --- | --- |
| 运行版本 | `v0.10.2-mumu.20` |
| 应用源码 | `947e5e477c8b66d9f677783d475bc3f3fb5dd642` |
| 当前运行状态记录提交 | `7effc8a0ced6ca3b35640ce896bbbfd4b71c1ee2` |
| 生产镜像 | `mumu-140/octopus-concurrency:v0.10.2-mumu.20` |
| 镜像 ID | `sha256:31787cf29681bd2e884f567f8318af7c6c26d25701e5cf18f82516b1c4141810` |
| 容器 | `octopus` / `c76ad77100fd85358eef8b539e33def1c63d4c15585589207a78ae12f8a0c0c9` |
| 启动时间 / restart count | `2026-08-13T03:16:42.754369701Z` / `0` |
| 网络与监听 | `host` / `0.0.0.0:35276` |
| 公网入口 | `https://octopus.muaiword.com`（Cloudflare Tunnel → caddy-gateway `127.0.0.1:27057` → `35276`；常态关闭，用时经 fwq57ys `~/software/cloudflared/cf-octopus on|off` 开关） |
| 数据挂载 | `/opt/octopus/data:/app/data` |
| Compose 副本 | `/opt/octopus/docker-compose.yml` |
| 回滚容器 | `octopus-mumu19-rollback-20260820T192318Z`（已创建未启动，.19 镜像待命，唯一正式回滚容器） |
| 回滚快照 | `/opt/octopus/backups/pre-v0.10.2-mumu.20-cutover-20260820T192318Z/`（唯一保留的回滚快照） |
| 切换后台任务 | `v0.10.2-mumu.20-cutover-20260820T192318Z`，状态 `COMPLETE` |

本次切换后，候选容器 `octopus-candidate-19` 及其候选数据副本（`octopus-candidate-18`、
`octopus-candidate-19`、`octopus-candidate-ci19`）已精确停止并删除。随后收整：历史回滚容器
`.12`、`.13` 以及历史版本/事故快照（`.11`、`.12`、`.13`、`.17`、`pre-relay-content-null`、
`pre-stale-container-cleanup`）已删除，仅保留 `.19` 正式回滚容器和最新的 `.20` 回滚快照。
生产容器、生产 SQLite 均未删除。

`.11` 从 `.9` 行为基线重新实现模型、最终渠道和请求分组三维小时统计；`.10` 的
`stats_dimension*` 实现和 tag 已废弃。生产三维必须逐项对账成功、失败、输入/输出 Token
和费用，coverage 必须为 `completed`。2026-07-26 对账为三维各 14,920 成功、2,497 失败、
1,746,810,391 输入 Token、9,006,975 输出 Token、费用 3481.480457，`.10` 旧表不存在。
30 天或累计查询超出可回填历史时，Web 必须明确显示部分覆盖。

## 镜像与源码选择

生产目标镜像必须同时满足：

1. 使用不可变新 tag：`v<major>.<minor>.<patch>-mumu.<revision>`；
2. tag 精确指向实际应用源码 commit，Release/CI 均成功；
3. GHCR 镜像的 OCI version、revision、source tree、created、source URL 与源码一致；
4. 本机 image ID 已记录并与候选实际运行 image ID 相同；
5. `deploy/fwq57ys/compose.yaml` 与 `production-state.json` 在各自正确阶段声明同一精确镜像；
6. 构建来自干净的规范源码，并使用 `Dockerfile.build` 和
   `scripts/build-production-image.sh <new-version>`。

`Dockerfile.build` 最后一阶段的
`hureru/octopus@sha256:35c6b368...` 只是固定运行时基础层。本仓库会覆盖
`/app/octopus`，所以该基础层不是生产镜像。

GHCR 是发布分发源。包为私有时，拉取凭据必须具备 `read:packages`，凭据不得进入仓库、日志
或聊天。遇到 `401 unauthorized` 或 `403` 时停止并修复包读取权限；不得静默改用 Docker Hub
同名镜像、旧 tag、本地重建或上游镜像。拉取 `ghcr.io/mumu-140/octopus-concurrency:<version>`
后先核对 OCI 和 image ID，再按受管 Compose 所需的精确本地引用使用。

明确禁止：

- `hureru/octopus:*`、`bestruirui/octopus:*`、`latest`；
- 临时 Dockerfile、测试镜像、未声明旧 mumu tag；
- `v0.10.2-mumu.8`：固定 Go 基础摘要失效导致发布失败，从未部署；
- 从 `octopus/`、`octopus-src*` 或 build-cache 构建；
- 仅凭 GitHub `main` 最新、Release 页面成功或镜像名相似判定可部署；
- Docker Hub 拉取失败/成功时自动替换 GHCR 身份。

生产 Compose 保持 `pull_policy: never`；镜像必须在切换前显式拉取和核验。

## 候选验证

候选验证可以在发布后、维护窗口前执行，但必须与生产完全隔离。

### 创建前

1. 记录生产容器 ID、启动时间、镜像 ID、挂载和 restart count。
2. 确认候选容器名、回环端口、数据副本路径均未占用。
3. 对生产 SQLite 创建在线一致性副本并验证 `quick_check`、大小和 SHA-256。
4. 核对候选镜像 OCI 身份等于目标 Release。
5. 候选仍使用 host 网络，但通过环境变量只监听 `127.0.0.1:<candidate-port>`，规避本机
   bridge/MPTCP 故障且不对外占用生产端口。

### 强制隔离

- 容器名不得为 `octopus`，不得复用回滚容器名；
- 不得监听 `35276`；
- 任一 Mount.Source 都不得等于 `/opt/octopus/data`；
- 数据副本、日志和状态目录使用本次任务唯一名称；
- 禁止修改生产 Compose 副本、生产状态清单或生产容器。

### 验收矩阵

| 维度 | 最低验收 |
| --- | --- |
| 身份 | 候选 Config.Image、image ID、OCI version/revision/source tree 与目标一致 |
| 运行 | HTTP 200、host 网络、restart=0、无 panic/FATAL、挂载只指向副本 |
| 数据 | 副本 `quick_check=ok`；迁移/回填可重试且不破坏旧数据 |
| 协议 | 按改动覆盖 OpenAI Chat、Responses、Anthropic；流式/非流式、工具调用、失败重试 |
| 统计 | coverage=`completed`；模型/最终渠道/请求分组三维成功、失败、Token、费用逐项一致 |
| Web | 真实数据；桌面与 320px/390px；排序、分页、tab、键盘、加载/空/错误、无横向溢出 |
| 生产隔离 | 验收前后生产容器 ID/启动时间/restart count 不变，生产数据未被候选挂载 |

涉及历史回填时不得沿用普通 HTTP 的短超时。已验证 `.11` 首次回填可能超过 120 秒；
就绪门禁最多等待 30 分钟并持续记录进度，超时才判失败。不得因页面先返回 200 就跳过数据门禁。

候选失败时保持生产不变，保存必要日志后按唯一名称精确删除候选容器、候选数据和临时隧道；
不得全局 prune。失败镜像/tag 保留为审计证据，除非另有精确清理授权。

## 数据备份

Octopus SQLite 使用 WAL。在线备份必须使用 SQLite backup API；禁止分别复制活动中的
`data.db`、`data.db-wal`、`data.db-shm` 作为一致性快照。

在获批脚本中使用以下已验证模式，路径必须先固定并检查可用空间：

```bash
sqlite3 "$SOURCE_DB" \
  ".timeout 60000" \
  "PRAGMA query_only=ON;" \
  "BEGIN;" \
  "SELECT COUNT(*) FROM sqlite_schema;" \
  ".backup '$SNAPSHOT_DB'" \
  "ROLLBACK;"
sqlite3 -readonly "$SNAPSHOT_DB" 'PRAGMA quick_check;'
sha256sum "$SNAPSHOT_DB"
```

`SELECT COUNT(*) FROM sqlite_schema` 使只读事务发生实际读取并固定 WAL 快照。只写 `BEGIN`
但没有实际读取时，外部持续写入可能让 `.backup` 从头重启。备份前至少预留“数据库大小 +
5 GiB”，备份后记录字节数、SHA-256、`quick_check`，并保存：

- 切换前/目标 Compose；
- 切换前状态清单；
- `config.json`；
- 旧/新容器和镜像 inspect；
- 状态、日志和回滚名称。

快照目录权限设为 700，凭据不写日志。自动回滚默认只恢复旧应用容器和声明；数据库文件恢复
属于独立高风险操作，不能在新旧进程仍打开数据库时自动覆盖。涉及非向后兼容迁移时，必须在
候选阶段证明旧应用可读新 schema，或另写明确的数据回退方案。

## 生产切换

Release 成功不等于部署授权。只有明确维护窗口、候选全部通过和以下输入固定后才能准备切换：

- source commit、source tree、Release/tag、CI URL；
- 新旧镜像引用和 image ID；
- 当前生产容器 ID、版本和数据指纹；
- 受管 Compose/生产副本；
- 快照目录、回滚容器名、部署 run ID；
- 切换脚本 SHA-256 和批准记录。

切换脚本必须是可独立完成的单一后台任务，至少包含：

1. `set -Eeuo pipefail`、`umask 077`、全局 `flock` 和原子状态文件；
2. 重新执行 `--repo`，并用批准记录精确核对干净 `main=origin/main`、tag、候选、目标镜像和旧容器；staging 后不以预期失败的 `--live` 代替旧容器身份断言；
3. 检查快照空间，创建并验证在线快照；
4. 停生产前创建并 inspect 精确回滚容器；
5. 停旧容器、保留回滚点、安装受管 Compose 副本、以 `--no-build --pull never` 启动新容器；
6. HTTP、身份、host 网络、挂载、restart count、日志、SQLite 和功能就绪门禁；
7. 任一步失败触发 trap：移除失败容器、恢复旧 Compose/声明、重命名并启动回滚容器；
8. 回滚后再次验证 HTTP、旧镜像身份、`quick_check` 和 `--live`；
9. 全部通过后才按 live inspect 更新 `production-state.json`，保存证据并写 `COMPLETE`；
10. 日志、PID、阶段和最终状态保存在 `/opt/octopus/deployments/<run-id>/`。

后台任务必须通过 `nohup` 或等价的脱离会话机制启动，stdin 关闭，stdout/stderr 写入部署日志；
启动后用新的只读 SSH/API 连接观察状态。禁止在承载 Codex/Claude/Hermes 当前通信的前台 SSH
命令中直接 stop/restart/recreate Octopus，也禁止把后续启动/回滚依赖当前会话继续发命令。

## 验证与回滚

切换完成后，从新连接执行：

1. 容器运行且 ID、image ID、Config.Image、StartedAt 与状态清单一致；
2. `network_mode=host`，数据挂载精确为生产目录，`restart=0`；
3. 本机 `127.0.0.1:35276` 和独立公网入口均返回 200；
4. SQLite `quick_check=ok`，受影响迁移/统计/价格对账通过；
5. 最近日志无 panic、FATAL 和本次缺陷特征；
6. 生产 Compose 副本与受管 Compose 逐字一致；
7. `scripts/check-governance.sh --repo` 和 `--live` 通过；
8. 状态清单变更已提交，主线 CI 对该运行状态提交成功；
9. 回滚容器和快照真实存在，候选与临时资源已精确清理。

回滚也属于生产生命周期操作，只能由独立后台任务执行。当前状态清单声明的唯一正式回滚点为
``.19` 容器 `octopus-mumu19-rollback-20260820T192318Z` 和 `.20` 切换快照（`/opt/octopus/
backups/pre-v0.10.2-mumu.20-cutover-20260820T192318Z/`）。历史回滚容器 `.12`、`.13`
及旧版快照已清理，不再支持回滚。不得复制旧文档中的前台 Docker 命令。vps76 的历史小型数据
副本和已停止的 `hureru/octopus:latest` 不是热备或受支持的回滚版本。

## 已知事故与处理

| 现象 | 已确认原因 | 正确处理 | 禁止的错误处理 |
| --- | --- | --- | --- |
| Claude/Codex 把旧 Octopus 当生产修改 | 目录名或旧容器被误当成唯一真值 | 只进 `octopus-mumu/`，读状态清单并运行 `--repo/--live` | 在 `octopus/`、`octopus-src*`、cache 初始化 Git 或恢复代码 |
| 镜像名正确但应用不是目标源码 | 把 `main`、tag、image ID、container ID 混为一个事实 | 同时核对 tag commit、OCI revision/tree、image ID 和容器 inspect | 只看镜像名或首页版本 |
| 拉 GHCR 返回 401/403 | 私有包凭据缺少 `read:packages` | 修复凭据范围后重拉原始镜像 | 改用 Docker Hub、旧 tag 或本地重建冒充 |
| 上游基础层被当成生产镜像 | 误读 `Dockerfile.build` 最后一阶段 | 只部署受管 Compose/状态清单共同声明的 mumu 镜像 | 直接启动 `hureru/octopus` |
| bridge 容器 accept 连接但 HTTP 永久无响应 | fwq57ys 内核 MPTCP 与 Go 1.24+ socket 在 Docker bridge/ports 组合下复现故障 | Octopus 使用 host 网络；候选用回环独立端口 | 改回 bridge、反复重启或误判应用死锁 |
| 前台 stop 后代理失联，后续启动/回滚无法发送 | Octopus 承载当前代理 API 调用链 | 所有中断操作放入带日志和回滚的脱离会话后台任务 | 在前台 SSH 分步 stop、再计划发送 start |
| WAL 在线备份长时间反复重启 | 只 `BEGIN` 未实际读取，未固定 WAL 读快照，外部写入持续推进 | query_only + BEGIN + 实际 SELECT + `.backup`，然后 SHA-256/`quick_check` | 复制 db/wal/shm，或无验证就切换 |
| `.11` 首次回填被判超时并自动回滚 | 120 秒健康窗口不足以完成大型历史回填 | 功能就绪门禁最多等待 30 分钟并记录进度 | 只看 HTTP 200，或用短超时反复切换 |
| `.10` 页面可用但三维数据严重缺失 | 无完整历史回填且遗漏写入路径 | 拒绝候选；从 `.9` 基线重做并逐项对账 | 因 UI 正常而切生产 |
| `.8` tag 存在但无法发布 | 固定 Go 基础镜像摘要失效 | 使用新不可变版本修复摘要并重新 CI/Release | 移动/复用 `.8` tag |
| 私有仓库无法开启 branch protection | 当前套餐 API 返回 403 | pre-push hook、governance CI、CODEOWNERS、普通快进共同约束 | 以“无保护”为由直接推 main 或 force |

## 停止条件

出现任一情况立即停止生产写入，并保持或恢复旧服务：

- 初始基线或切换完成后的 `--repo` / `--live` 失败，或 staging 之外出现 Compose/状态/inspect 漂移；
- 当前目录、分支、source commit、tag、OCI 或 image ID 任一不清楚；
- GHCR 原始镜像不可获取或候选实际 image ID 不一致；
- 候选使用生产容器名、生产端口或生产数据；
- 候选协议、Web、迁移、统计任一门禁失败；
- 统计 coverage 未完成或三维任一总量不一致；
- 快照空间不足，SHA-256/`quick_check` 失败，或回滚容器不可验证；
- 没有明确维护窗口、后台脚本、部署日志、锁或自动回滚；
- 后台任务仍依赖当前 API 会话继续发命令；
- 新版本包含旧应用不兼容的数据迁移但没有数据回退方案；
- 切换后 HTTP、身份、挂载、SQLite、日志或治理任一失败。

停止后不得以旧目录、旧 tag、Docker Hub 镜像、前台手工命令或降低门禁绕过问题。

## 交付证据

每次生产任务必须留下：

```text
维护窗口与授权：
规范源码 / 分支 / main / origin SHA：
运行前版本 / 应用源码 / image ID / container ID：
目标 Release / CI / source tree / OCI：
候选名称 / 端口 / 数据副本 / 验收结果：
快照路径 / 字节数 / SHA-256 / quick_check：
后台 run ID / 脚本 SHA-256 / 日志 / 最终状态：
运行后版本 / 应用源码 / image ID / container ID：
HTTP / 协议 / Web / 统计 / 日志验证：
production-state 提交 / 主线 CI：
回滚容器 / 快照 / 回滚验证：
生产与其他服务明确未修改项：
候选、临时数据、隧道和测试资源清理：
遗留风险或停止项：
```

纯源码、文档或 Release 任务必须明确写“未部署，生产容器和 SQLite 未修改”。
