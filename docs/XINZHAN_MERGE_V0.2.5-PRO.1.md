# v0.2.5-pro.1 合并分析

本文记录 `sub2api-pro` 如何并入官方 `sub2api v0.2.5` 与澄川「新站」定制，作为本版本的功能核对清单。不记录服务器地址、密码或部署密钥。

## 1. 输入

| 来源 | 状态 |
|---|---|
| 官方 `Wei-Shaw/sub2api` | `origin/main` = `881f32026`（仅把 `VERSION` 从 0.2.4 改为 0.2.5）。功能代码与 tag `v0.2.5` / `86f93c28e` 相同。 |
| Pro 旧基线 | `0.2.0-pro.1` / `8400508a8` |
| 新站 | `sub2新站/sub2新站`，`VERSION=0.2.4`，无独立 Git 历史；快照导入为 `xinzhan/xinzhan-v0.2.4` / `99f927194` |
| 本版本 | `backend/cmd/server/VERSION` = `0.2.5-pro.1`，分支 `sync/v0.2.5` |

官方比 tag 多出的唯一提交是 VERSION 文件，已被本 Fork 的 `0.2.5-pro.1` 覆盖，因此官方最新功能代码已在本树中。

## 2. 新站功能核对

对照新站 `docs/FEATURE_CHANGE_MANIFEST.md` 列出的 139 个路径：产品代码 130 个均已进入本树。未纳入的 9 个全部是文档或本地 `verification/` 日志，不是运行时代码。

已合入并接线的能力：

- 账号保护 / 反降智（legacy 默认、mode1、TLS 档案、请求完整性）
- 严格 RPM、自适应并发、流量策略
- 智能测试中心（含认证隔离，不在测试路径刷新 OAuth）
- 用户清理（预览、保护、软删除）
- `super_admin` / `admin` 分级
- 分组安全策略、全局定价、账号健康、毛利、分层路由、花费守卫、工单
- Relay 工作台（澄川品牌、账单、工单入口）
- 官方 0.2.5 分组模型 allowlist、MiniMax、OpenCode Go

调度合并原则：

- 保留 Pro 的 Priority Saturation、亲和预留、三返回值槽位获取
- 并发上限先走 `Mode1EffectiveConcurrency()`，再套亲和分区
- 安全审计：Pro 关键词会话拦截 + 新站分组安全策略
- Cyber 完整请求体与 `overturned` 标记并存
- 自更新仓库保持 `killaragorn/sub2api-pro`，不使用新站/官方 `Wei-Shaw/sub2api` 更新源

## 3. 迁移编号

官方 0.2.5 占用 `235_group_model_allowlist` 到 `238_opencode_go_platform`。新站同号文件已改名，避免覆盖：

| 原新站文件 | 本树编号 |
|---|---|
| `235_account_proxy_mode_check.sql` | `239` |
| `237_group_security_policy.sql` | `240` |
| `238_content_moderation_overturned.sql` | `249` |
| `238_purge_unlimited_user_platform_quotas.sql` | `250` |

`241`–`248` 新站迁移编号未与官方冲突，保持不变。

## 4. 明确不合并

- 新站根目录本地评审备忘与 `verification/` 运行日志
- `docs/MANAGEMENT_FEATURES.md` 中的具体服务器/备份路径
- 新站 `update_service` 若指向官方仓库的部分（Pro 已改 Fork）
- 未打 tag 前的临时 `docs/plan/` 辅助脚本

## 5. 验证

- `go build ./cmd/server ./internal/... ./ent/`
- `go test -count=0`：`internal/service`、`internal/handler`、`internal/handler/admin`、`internal/repository`、`migrations`
- 抽样：亲和容量、安全策略、allowlist 模型列表
