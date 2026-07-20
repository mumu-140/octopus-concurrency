# Octopus Repository Rules

本文件是该仓库对 Codex、Claude、Hermes 和其他自动化代理的最高优先级项目规则。
开始任何工作前必须先读本文件，再按需读取 `docs/development-governance.md` 和
`docs/production.md`。

## 1. 唯一真值

| 对象 | 唯一位置 | 允许操作 |
| --- | --- | --- |
| GitHub 仓库 | `mumu-140/octopus-concurrency` | 代码、文档、CI、Release 和 GHCR 的远端真值 |
| 可编辑源码 | `/home/yangs/API/octopus-mumu/` | 开发、测试、提交、构建 |
| 生产控制面 | `/home/yangs/API/octopus/` | 仅保存生产 compose 副本、运行数据和备份；不是源码 |
| 生产数据 | `/home/yangs/API/octopus/data/` | 仅获批的数据操作；不得用于开发或测试 |
| 构建缓存 | `/home/yangs/API/octopus-build-cache/` | 仅作缓存；不是源码，不得提交或部署 |
| 历史源码 | `/home/yangs/API/octopus-src/` | 只读保留，不得继续开发、构建或部署 |
| 失败克隆 | `/home/yangs/API/octopus-src.failed-clone-20260713/` | 只读证据，不得恢复为工作目录 |

禁止新建第二个 Octopus 源码目录。禁止在生产目录、历史目录或缓存目录修改代码。
目录存在不代表它是当前进度；Git 提交、生产状态清单和运行容器才是证据。

生产身份必须同时满足以下条件，缺一项都不得启动或重建：

1. 镜像 tag、镜像 ID、应用源码提交与 `deploy/fwq57ys/production-state.json` 一致；
2. Compose 来自 `deploy/fwq57ys/compose.yaml`，生产副本与其逐字一致；
3. 数据只挂载 `/home/yangs/API/octopus/data:/app/data`，网络模式为 `host`；
4. `scripts/check-governance.sh --repo` 和 `--live` 均通过。

## 2. 任务与会话边界

- 一个任务只使用一个主题分支和一个方案/状态记录。
- 不继承其他项目、其他会话或已归档任务的 Delivery Gate、计划状态和待办。
- 恢复工作时按以下顺序判断真值：当前用户请求、当前仓库代码与 Git、
  `deploy/fwq57ys/production-state.json`、当前任务状态、历史文档。
- 交接前必须写清当前分支、HEAD、远端 SHA、未提交变更、验证结果、是否部署和回退点。
- “代码完成”“镜像构建完成”“已部署”是三个不同状态，不得混用。

## 3. Git 规则

- `main` 是唯一集成主线；空闲和任务结束时工作树必须回到干净的 `main`。
- 禁止直接在 `main` 开发。每个任务从最新 `origin/main` 创建短生命周期分支：
  `codex/<topic>`、`feat/<topic>`、`fix/<topic>` 或 `chore/<topic>`。
- 一个分支只承担一个主题。禁止把未审查的其他分支进度顺手合并。
- `codex/v0.10.1-image-api` 是未发布隔离进度；没有独立方案、数据库迁移验证和审查时
  不得合并或部署。
- `fix/group-update-conflict`、`codex/production-normalization` 和 `backup/*` 是历史证据，
  不再作为新开发起点。
- 只允许普通快进。禁止 force push、重写公开历史、移动已发布 tag 或复用同名 tag。
- 发布 tag 使用 `v<major>.<minor>.<patch>-mumu.<revision>`，必须是 annotated tag，
  且精确指向实际发布的应用源码提交。
- 本仓库套餐不支持 GitHub 服务端 branch protection；必须安装 `.githooks/pre-push`。
  hook 只是补充控制，`--no-verify` 不得用于绕过规则。

## 4. 标准开发流程

1. 运行 `scripts/check-governance.sh --repo`。
2. 确认工作树干净，执行 `git fetch origin`，从 `origin/main` 创建主题分支。
3. 只实现当前任务；代码变更先测试，再实现，再回归。
4. 后端至少运行 `go test -buildvcs=false ./...`；前端至少运行
   `pnpm install --frozen-lockfile` 和 `pnpm lint`。
5. 构建或部署相关变更必须额外运行 Dockerfile、compose 和完整镜像旁路构建检查。
6. 提交并推送主题分支，等待 GitHub CI 全部通过。
7. 审查通过后才允许普通快进 `main`。本机 hook 要求显式设置
   `OCTOPUS_MAIN_PROMOTION=1`，该变量表示“已核对 CI 与审查”，不是免检开关。
8. 推送后再次核对远端 SHA，删除或停止继续使用已完成主题分支。
9. 没有获批维护窗口时，到此结束；不得把合并代码自动解释为部署授权。

## 5. 构建与发布

- 生产镜像只使用 `Dockerfile.build` 和 `scripts/build-production-image.sh`。
- 禁止使用已删除的 `Dockerfile.concurrency`、浮动 `latest` 基础镜像或临时 Dockerfile。
- 生产服务不得直接运行 `hureru/octopus`、`bestruirui/octopus`、已删除的历史根
  Compose、任意旧 mumu tag 或仅凭名称相似的本地镜像。
- `Dockerfile.build` 最后一阶段的固定摘要 `hureru/octopus@sha256:...` 只是运行时
  基础层，应用二进制会被本仓库构建产物覆盖；该基础层本身不是可部署的生产镜像。
- 唯一允许的生产镜像是受管 Compose 和 `production-state.json` 同时声明、且 OCI
  revision/source tree 已核验的精确版本。`v0.10.2-mumu.8` 发布失败且从未部署，禁止使用。
- 构建必须来自干净、已提交的工作树，并写入 version、revision、source tree、
  RFC 3339 UTC build time 和 source URL。
- pnpm 版本由 `web/package.json` 固定；原生依赖许可只维护在
  `web/pnpm-workspace.yaml`，Dockerfile 必须在安装前复制该文件。
- 默认使用已提交的价格数据。只有显式设置 `UPDATE_PRICE_DATA=1` 才能刷新，
  且刷新结果必须独立审查、提交后再构建。
- 当前运行版本、tag、源码提交和容器指纹始终以 `deploy/fwq57ys/production-state.json` 与 `scripts/check-governance.sh --live` 为准；不得依赖规则文件中的历史版本描述。
- 新发布必须使用新版本和新 tag；GitHub release workflow 只响应 `v*-mumu.*`。

## 6. 生产与数据边界

- 未获得明确维护窗口授权时，禁止执行任何 compose/container 生命周期命令，
  包括 up、down、restart、stop、rm 和 recreate。
- 即使已获维护窗口授权，stop/recreate/start、健康等待和失败回滚也必须写入带日志的
  独立后台任务，脱离当前 Codex/Claude/Hermes/API 会话执行。禁止在承载当前代理通信的
  前台 SSH 命令中直接中断 Octopus。
- 更新 `/home/yangs/API/octopus/docker-compose.yml` 文本不等于部署；是否已生效必须以容器
  Compose 标签、运行指纹和 `production-state.json` 共同核验。
- 生产 SQLite 不得用于开发测试。禁止直接改库、复制到源码目录、加入构建上下文或提交。
- 数据变更必须先创建并验证最小快照，再通过受审查的迁移或应用 API 执行，并记录范围。
- 禁止全局 Docker 清理。测试镜像和容器必须使用唯一名称并在验证后精确删除。
- 发布前后必须运行 `scripts/check-governance.sh --live`，比较容器、镜像、启动时间、
  host 网络、数据挂载、compose 和 HTTP 状态。
- 任何获批部署完成后，必须更新 `deploy/fwq57ys/production-state.json`，再提交运行证据。

## 7. 停止条件

出现以下任一情况立即停止写入并先查明原因：

- 当前目录不是唯一源码仓库；
- 工作树存在来源不明的改动；
- release tag、镜像标签和应用源码提交不一致；
- 生产容器指纹与状态清单不一致；
- 没有可验证的回退快照；
- 操作会触发生产容器生命周期或 SQLite 写入但没有明确授权；
- 需要合并隔离分支，却没有独立方案、迁移验证和 CI 证据。

## 8. 完成交付

完成报告必须同时包含：

- 源码目录、分支、HEAD 和远端 SHA；
- 运行版本、应用源码 commit、镜像 ID 和容器 ID；
- 实际执行的测试与 CI URL；
- 修改了什么、明确未修改什么；
- 快照与回退路径；
- 测试容器、镜像和临时文件的清理结果；
- 未部署进度和已知风险。
