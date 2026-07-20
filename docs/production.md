# Octopus 生产部署手册

本文件是 fwq57ys 上 Octopus 的唯一现行部署手册。仓库规则见 `AGENTS.md`，开发流程见
`docs/development-governance.md`，机器可读运行真值见
`deploy/fwq57ys/production-state.json`。历史版本、旧方案和 Git 记录只能用于追溯，不能据此
选择源码、镜像或执行部署。

## 职责边界

| 对象 | 规范位置 | 职责 | 禁止事项 |
| --- | --- | --- | --- |
| GitHub | `mumu-140/octopus-concurrency` | 远端源码、CI、Release、GHCR | 不移动公开 tag，不重写 `main` |
| 规范源码 | `/home/yangs/API/octopus-mumu/` | 唯一开发、测试、构建和提交目录 | 不在其他目录改代码或构建 |
| 受管 Compose | `deploy/fwq57ys/compose.yaml` | 生产部署声明真值 | 不直接挂测试数据或改用 bridge |
| 生产控制面 | `/home/yangs/API/octopus/` | Compose 生产副本、真实数据、备份 | 不是 Git 仓库，不存源码、不构建 |
| 生产数据 | `/home/yangs/API/octopus/data/` | `config.json`、SQLite 和运行数据 | 不复制进源码，不用于候选测试 |
| 历史目录 | `octopus-src*`、`octopus-build-cache` | 只读现场或缓存 | 不作为开发、构建、发布或部署来源 |

生产控制面继续保留在 `octopus/`，规范源码继续保留在 `octopus-mumu/`。不要合并目录，也不要
把 17 GB 生产数据移动到源码仓库。

## 当前生产真值

以下值于 2026-07-20 通过 `scripts/check-governance.sh --live` 和 Docker inspect 核验：

| 项目 | 值 |
| --- | --- |
| 运行版本 | `v0.10.2-mumu.9` |
| 应用源码 | `ee01f2af8c6856d275421bd9bd9aca2180f57d93` |
| 当前运行状态记录提交 | `a63b32335384672bbac3e7dfd1678cf6127989ec` |
| 生产镜像 | `mumu-140/octopus-concurrency:v0.10.2-mumu.9` |
| 镜像 ID | `sha256:b21cb2b9806307d34997d3756c8ba896758eab512e7e3403f5ec8fff0de8836f` |
| 容器 | `octopus` / `7acd6c3da5522b896860b75047c7b1dc80057d006d5f6c9f0002d28b8ee9001a` |
| 网络与监听 | `host` / `0.0.0.0:35276` |
| 数据挂载 | `/home/yangs/API/octopus/data:/app/data` |
| Compose 副本 | `/home/yangs/API/octopus/docker-compose.yml` |
| 回滚容器 | `octopus-mumu7-rollback-20260719-221914` |
| 回滚快照 | `/home/yangs/API/octopus/backups/pre-v0.10.2-mumu.9-cutover-20260719-221914/` |

不得仅凭本文中的版本号判断当前生产。每次操作前都重新读取
`deploy/fwq57ys/production-state.json` 并运行只读守卫。

## 镜像与源码选择

生产只接受同时满足以下条件的镜像：

1. 精确 tag 同时出现在 `deploy/fwq57ys/compose.yaml` 和 `production-state.json`；
2. 镜像 ID、OCI revision、source tree 与目标 Release 和源码提交一致；
3. 构建来自干净的规范源码仓库，并通过 `scripts/build-production-image.sh <version>`；
4. 发布 tag 是不可变的 `v<major>.<minor>.<patch>-mumu.<revision>`。

明确禁止：

- 直接部署 `hureru/octopus:*` 或 `bestruirui/octopus:*`；
- 使用浮动 `latest`、临时 Dockerfile、测试镜像或未被状态清单声明的旧 mumu tag；
- 使用 `v0.10.2-mumu.8`。该 tag 因构建基础摘要失效导致发布失败，从未部署；
- 从 `/home/yangs/API/octopus/`、`octopus-src*` 或 `octopus-build-cache` 构建；
- 将 GitHub `main` 的最新提交自动解释为当前运行应用源码。

`Dockerfile.build` 中固定摘要的 `hureru/octopus@sha256:...` 仅是运行时基础层，本仓库构建会
覆盖 `/app/octopus`。它不是生产部署镜像。生产 Compose 使用 `pull_policy: never`，防止未限定
镜像名意外从 Docker Hub 拉取；需要从 GHCR 分发时，必须显式拉取
`ghcr.io/mumu-140/octopus-concurrency:<version>`，核验后再按受管 tag 使用。

## 开发、发布与部署

开发开始：

```bash
cd /home/yangs/API/octopus-mumu
scripts/check-governance.sh --repo
git fetch origin
git switch -c codex/<topic> origin/main
```

代码通过测试、审查和 GitHub CI 后才能普通快进 `main`。发布成功不等于允许部署；创建新
Release、更新受管 Compose、更新生产副本、切换容器和更新状态清单是不同步骤。

生产构建只使用：

```bash
scripts/build-production-image.sh v<major>.<minor>.<patch>-mumu.<revision>
```

价格预设默认使用仓库已提交内容。只有独立价格更新任务才可设置 `UPDATE_PRICE_DATA=1`，并且
必须先审查、提交生成差异，再构建正式镜像。

## 强制后台切换

任何会中断 API 的 stop、restart、recreate、remove 或版本切换，都不得在当前代理会话的前台
SSH 命令中直接执行。Octopus 承载 Codex/Claude/Hermes 的调用链，前台停机可能让后续启动和
回滚命令无法发送。

获批维护窗口必须按以下顺序执行：

1. 运行 `scripts/check-governance.sh --repo` 和 `--live`；
2. 创建 SQLite 在线一致性备份、Compose/inspect/镜像清单并验证 SHA-256 与 `quick_check`；
3. 在独立数据副本、独立容器名和独立端口完成候选验证；
4. 等待目标提交 CI、GitHub Release 和镜像发布全部成功；
5. 把停止旧容器、保留回滚容器、启动新容器、健康等待、失败自动回滚和完整日志写入一个
   可独立完成的后台任务；
6. 脱离当前 API 会话启动后台任务，不再依赖当前会话继续发命令；
7. 用新的只读连接核验容器、HTTP、host 网络、数据挂载、SQLite 和回滚点；
8. 更新 `production-state.json`，提交运行状态，再确认该 SHA 的主线 CI。

纯文档、纯源码或 Compose 文本变更不允许触发容器生命周期。禁止全局 Docker 清理；候选资源
必须按名称精确删除。

## 价格规则

价格单位为美元/百万 token，计费顺序为：

1. `actual_model_name` 有非零精确价格时使用实际模型价格；
2. 实际模型缺价或只有全零占位时，回退到 `request_model_name` 的分组官方价格；
3. 请求分组明确为零价时保持零，不臆造价格。

文本、Responses、输入估算兜底和图片路径使用同一规则。当前仅
`codex-auto-review`（无公开定价）与 `sensenova-6.7-flash-lite`（免费方案）明确保留零价。
历史日志不会随价格表自动重算，回填必须作为独立数据任务执行。

## 验证与回滚

日常只读验证：

```bash
cd /home/yangs/API/octopus-mumu
scripts/check-governance.sh --repo
scripts/check-governance.sh --live
```

`--live` 只读取容器、Compose、挂载和 HTTP 基线，不操作生命周期或写 SQLite。

当前回滚只能使用状态清单声明的 `.7` 回滚容器和 `.9` 切换快照。回滚也属于生产生命周期
操作，必须由带日志和失败处理的独立后台任务执行；不得复制旧文档中的前台 Docker 命令。

vps76 的 `/opt/docker/octopus/data/` 只是 2026-07-10 迁移时的历史小型数据副本，已停止的
`hureru/octopus:latest` 不是受支持回滚版本。vps76 不是热备或可直接启动的灾备节点。
