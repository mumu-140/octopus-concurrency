# 生产运维规范

## 治理入口

仓库级强制规则见 `AGENTS.md`，完整开发、分支、发布和交接流程见
`docs/development-governance.md`。机器可读运行基线位于
`deploy/fwq57ys/production-state.json`。

开始开发或审查时运行：

    scripts/check-governance.sh --repo

在 fwq57ys 执行任何获批生产操作前后运行：

    scripts/check-governance.sh --live

两种检查均为只读；`--live` 不会操作容器生命周期或写 SQLite。

## 真值来源

fwq57ys 上唯一可编辑的二开源码仓库是
/home/yangs/API/octopus-mumu/。生产数据和 compose 位于
/home/yangs/API/octopus/，该目录不是源码仓库。

2026-07-16 核验的运行版本如下：

| 项目 | 值 |
| --- | --- |
| 版本 | v0.10.1-mumu.1 |
| 源码提交 | ac5679612d498dd2e31511bcbada33287719414e |
| 镜像 | mumu-140/octopus-concurrency:v0.10.1-mumu.1 |
| 镜像 ID | sha256:178121589e353954c217b31817120e721828590df0f7bc7bb1f0efe27c18d8f3 |
| 数据挂载 | /home/yangs/API/octopus/data:/app/data |
| 网络 | host，0.0.0.0:35276 |

发布 tag 精确指向运行镜像对应的应用源码提交。构建工具在该 tag 之后
规范化，因此该历史镜像应从已验证的镜像归档恢复，不得用同名 tag
重新构建并覆盖。

## 恢复快照

规范化前的控制面快照位于：

    /home/yangs/API/octopus/backups/20260716-0736-production-normalization-prechange/

其中包含完整 Git 历史、运行镜像归档、运行二进制、原 compose/config
以及 SHA-256 证据。该快照没有复制或修改生产数据库。

## 生产构建

生产构建统一使用 Dockerfile.build，并通过以下脚本启动：

    scripts/build-production-image.sh v0.10.2-mumu.0

脚本要求受跟踪工作树干净，并显式传入版本、提交、源码 tree 和构建时间。
Docker 基础镜像、Dockerfile frontend 与 pnpm 均已固定。镜像构建默认使用
仓库已提交的 internal/price/presets.go。

刷新价格预设必须作为独立、可审查的源码变更执行：

    UPDATE_PRICE_DATA=1 scripts/build.sh release
    git diff -- internal/price/presets.go

审查并提交生成的价格差异后，才能构建生产镜像。禁止在生产镜像构建期间
临时从 models.dev 获取数据。

## 部署

受 Git 管理的生产 compose 是 deploy/fwq57ys/compose.yaml，生产副本是
/home/yangs/API/octopus/docker-compose.yml。

当前容器在本次规范化前以手工方式创建。更新 compose 文本不会接管或
重建该容器。转换为 compose 管理必然需要 Docker 重建容器，只能在明确
批准的维护窗口内执行。

文档或纯源码变更不得运行 docker compose 生命周期命令。获批发布前必须：

1. 创建并验证控制面快照；
2. 预加载并检查目标镜像；
3. 比较生产 compose 与受 Git 管理的版本；
4. 记录当前容器 ID、启动时间、镜像和挂载；
5. 在维护窗口执行明确的切换；
6. 验证 HTTP、挂载、数据库完整性和回滚路径。

## 分支

main 是唯一集成主线，但不得直接在 main 开发。每个任务从最新 origin/main 创建
短生命周期主题分支，经治理检查、测试、审查和 GitHub CI 后才可普通快进 main。
fix/group-update-conflict 保留 v0.10.1-mumu.1 发布分支。
codex/v0.10.1-image-api 包含尚未发布的协议路由工作；未完成独立审查和
数据库迁移验证前，不得合并或部署。

GitHub 私有仓库当前套餐不支持服务端 branch protection。服务器规范源码仓库必须运行
`scripts/install-git-hooks.sh` 安装版本化 pre-push guard；任何新克隆也必须先安装。


## 2026-07-19 v0.10.2-mumu.7 item_reference passthrough

- Issue: outbound Responses path synthesized `function_call_output.item_reference`, rejected by gateways such as Anyrouter (`unknown_parameter`).
- Fix commit: `2125315` — leave `item_reference` exactly as client-sent; keep typed item ID normalization.
- Image/tag: `mumu-140/octopus-concurrency:v0.10.2-mumu.7` / annotated tag `v0.10.2-mumu.7`.
- Candidate: `:35287` on DB copy; chat + responses smoke OK.
- Production cutover snapshot: `/home/yangs/API/octopus/backups/pre-v0.10.2-mumu.7-cutover-20260719-081850/`.
- Rollback container: `octopus-mumu6-rollback-20260719-081850` (`v0.10.2-mumu.6`).
