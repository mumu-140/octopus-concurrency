# Octopus 开发治理

本文件解释根 `AGENTS.md` 的执行细节。规则冲突时以 `AGENTS.md` 为准，运行事实冲突时
以当前代码、Git 对象、`deploy/fwq57ys/production-state.json` 和 Docker inspect 证据为准。

## 目录治理

### 唯一可编辑源码

`/home/yangs/API/octopus-mumu/` 是唯一开发仓库。所有代码、测试、构建文件、CI、
release workflow 和受版本控制的部署声明都在这里修改。

### 生产目录

`/home/yangs/API/octopus/` 只保存：

- `docker-compose.yml`：受 Git 管理 compose 的生产副本；
- `data/`：生产配置和 SQLite；
- `backups/`：控制面和数据快照；
- 已停用的历史切换脚本。

该目录不是源码仓库。不得在其中复制源码、执行 Git 初始化或临时构建。

仓库根目录不再提供通用 `docker-compose.yml`。唯一受版本控制的生产 Compose 是
`deploy/fwq57ys/compose.yaml`；本地开发按 README 从源码启动并使用独立数据。

### 历史与缓存目录

以下目录只保留现场，不得作为开发起点：

- `/home/yangs/API/octopus-src/`
- `/home/yangs/API/octopus-src.failed-clone-20260713/`
- `/home/yangs/API/octopus-build-cache/`

不因“看起来旧”而删除、移动或覆盖。清理必须作为独立任务，有清单、快照和逐项授权。

## 状态模型

每次汇报必须区分四个值：

1. 运行镜像 tag；
2. 运行应用源码 commit；
3. 当前 `main` commit；
4. 运行镜像 ID / 容器 ID。

运行版本、应用源码提交、当前 `main`、镜像和容器是相互独立的事实。具体值不得
从本说明中的历史示例推断；生产状态以 `deploy/fwq57ys/production-state.json` 为机器真值，
并由只读守卫与 Docker inspect 共同核验。`main` 更新不代表生产应用已更新。

## 分支生命周期

### 开始

```bash
scripts/check-governance.sh --repo
git status --short --branch
git fetch origin
git switch -c codex/<topic> origin/main
```

若工作树不干净或当前目录不明确，停止，不创建新目录规避问题。

### 开发与验证

- 每个分支只有一个目标；不顺带合并别的功能。
- 每次提交保持可审查，构建产物、缓存、数据库和凭据不得入 Git。
- 配置/文档改动至少运行语法和静态检查；代码改动运行对应测试。
- 构建/发布改动必须完成一次无生产数据挂载的完整旁路镜像构建。

### 推送与晋级

```bash
git push -u origin codex/<topic>
# 等待该 SHA 的 GitHub CI 全部成功并完成审查
git switch main
git merge --ff-only codex/<topic>
OCTOPUS_MAIN_PROMOTION=1 git push origin main
```

`OCTOPUS_MAIN_PROMOTION=1` 只允许 hook 放行显式晋级；使用者仍必须提供 CI URL 和审查证据。
禁止 `--no-verify`、force、rebase 已推送主线或重置公开历史。

### 收尾

- 核对 `origin/main` 等于本地 `main`。
- 主题分支不再承载新工作；需要新目标时从最新 `origin/main` 重新建分支。
- 回到干净 `main`。
- 更新交接记录，注明“未部署”或具体部署证据。

## 分支与历史版本

`main` 是唯一集成主线，但不是运行版本的替代描述。历史分支、backup 分支和旧 release tag
只用于追溯，不能作为新任务起点或部署来源。需要确认隔离分支时读取
`production-state.json` 的 `quarantinedBranches`，不要依赖文档中复制的旧列表。

## 发布与部署分离

### 发布

1. 从最新 `main` 完成版本字段更新和测试。
2. 创建新的 annotated tag；不得移动旧 tag。
3. 使用 `scripts/build-production-image.sh <new-version>` 或受控 release workflow 构建。
4. 核对二进制版本、Go 版本、commit、build time、OCI revision/source tree。
5. 推送新镜像，不覆盖旧版本。

生产服务只允许使用 `deploy/fwq57ys/compose.yaml` 和 `production-state.json` 同时声明的精确
镜像。上游镜像、`latest`、旧 mumu tag、失败发布 tag 和本地测试镜像均不得替代生产镜像。

### 部署

发布成功不自动授权部署。部署必须另有维护窗口，且执行：

1. `scripts/check-governance.sh --live`；
2. 创建并验证控制面/必要数据快照；
3. 预加载目标镜像；
4. 记录旧容器和数据指纹；
5. 把获批切换及失败回滚写成带日志、可独立完成的后台任务，脱离当前 API 会话执行；
6. 验证 HTTP、模型接口、挂载、数据库和回滚；
7. 更新生产状态清单并提交。

## 平台限制

该 GitHub 仓库是私有仓库，当前套餐的 branch protection API 返回 403。替代措施为：

- `main`/tag 治理写入根 `AGENTS.md`；
- `.githooks/pre-push` 阻止非快进、tag 改写和未显式批准的 main 推送；
- CI governance job 校验规则、状态清单、tag、版本和敏感文件边界；
- CODEOWNERS 与 PR 模板要求审查证据；
- 服务器源码仓库安装版本化 hook。

这些措施不能阻止从未安装 hook 的其他克隆直接推送，所以任何新克隆必须先运行
`scripts/install-git-hooks.sh`。套餐支持后，应把相同规则迁移为 GitHub ruleset，不能删除仓库内守卫。

## 交接模板

```text
任务：
源码目录：/home/yangs/API/octopus-mumu
分支 / HEAD / origin SHA：
运行版本 / 应用源码 / 镜像 ID / 容器 ID：
修改文件：
验证命令与 CI URL：
是否部署：否 / 是（维护窗口证据）
生产变更：
明确未修改：容器生命周期 / SQLite / 其他分支
快照与回退：
遗留风险：
临时资源清理：
```
