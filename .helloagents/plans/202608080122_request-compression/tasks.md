# 任务分解：request-compression

方案包：`.helloagents/plans/202608080122_request-compression/`

- [x] T1 脚手架：`internal/relay/compress/` 包、GroupCompressConfig、Engine 接口、fail-open 包装、master SettingKey
- [x] T2 Lite 保尾版 + fixture 测试（npm 输出保尾、空白折叠、代码围栏不动、幂等）
- [x] T3 Headroom + round-trip 测试（≥8 行同构 JSON 数组 → 列式表 → 解析回原行）
- [x] T4 Output styles（terse-prose / terse-cjk）+ 幂等注入测试
- [x] T5 Group 模型加 `compress_config` JSON 列 + 分组更新 API 透传 + master 开关读取
- [x] T6 relay_handler.go 挂点（group 解析后、replay 前）+ debug 统计日志
- [x] T7 benchmark（关闭路径 <1μs）+ `go build ./... && go test ./...` + `scripts/check-governance.sh --repo`
- [ ] T8 部署：后台构建镜像 → 单分组灰度开启 → 观察 → 回滚容器待命（按 docs/octopus-production.md）
- [ ] T9 (P2) 管理 UI：分组编辑对话框加压缩配置区（web/）

## Codex /goal 执行入口

目标引用本方案包：`.helloagents/plans/202608080122_request-compression/`（requirements.md / plan.md / 本文件 / contract.json）。
链路：`/goal -> ~auto -> ~qa`。AFK 边界：T1–T7 可自动连续执行；T8 部署（生产容器切换）为 HITL，必须用户显式确认后按 docs/octopus-production.md 后台任务执行，回滚容器先就绪。禁止前台停止/重启 Octopus 生产容器。
