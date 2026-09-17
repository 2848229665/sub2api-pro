# v0.2.5-pro.2 魔改核对

本文记录对 `sub2魔改/sub2新站` 的源码核对，以及它与当前 `sub2api-pro` 的关系。不记录服务器地址、密码或部署密钥。

## 1. 输入

| 来源 | 状态 |
|---|---|
| 魔改目录 | `sub2魔改/sub2新站`，`VERSION=0.2.4`，无 Git 提交历史 |
| 上次新站快照 | `sub2新站/sub2新站` / `temp-xinzhan` `xinzhan-v0.2.4` |
| 官方 | tag `v0.2.5` |
| 上一发布 | `v0.2.5-pro.1` |
| 本版本 | `backend/cmd/server/VERSION` = `0.2.5-pro.2` |

对 `backend/cmd`、`backend/internal`、`backend/ent/schema`、`backend/migrations`、`frontend/src`、`deploy`、`docs` 做 SHA-256 比较后：魔改与上次新站快照 **0 个文件新增、0 个删除、0 个内容变化**。魔改是同一份澄川定制树，外加根目录本地审查备忘，不是另一套产品代码。

## 2. 魔改能力（已在本树）

对照魔改 `docs/FEATURE_CHANGE_MANIFEST.md` 与 Sep 14/15 本地交付记录，下列能力均已在 `v0.2.5-pro.1` 接线，本版本继续保留：

- 账号保护 / 反降智：legacy 默认、mode1、TLS 档案、请求完整性独立开关
- 账号编辑分区：基本信息 / 连接与模型 / 保护与流量 / 计费与高级；流量策略随账号统一提交
- 严格 RPM、突发预算、自适应并发、建议参数只填入
- 智能测试中心（认证隔离，测试路径不刷新 OAuth）
- 用户清理（预览、保护、软删除、审计）
- `super_admin` / `admin` 分级
- 分组安全策略、完整分组统计、全局定价、账号健康、毛利、分层路由、花费守卫、工单
- Relay 工作台
- Grok 实时语音在开启硬限制时明确拒绝逐轮 RPM，避免假限制
- 官方 0.2.5 分组模型 allowlist、MiniMax、OpenCode Go

调度仍按 Pro 原则：

- Priority Saturation、亲和预留、三返回值槽位
- 并发上限先走 `Mode1EffectiveConcurrency()`，再经 `ConcurrencyLimitForAffinity()`
- 安全审计：Pro 关键词拦截 + 魔改分组安全策略
- 自更新仓库保持 `killaragorn/sub2api-pro`

## 3. 文件核对

魔改相对官方 `v0.2.5` 多出的产品源码文件 209 个：

| 结果 | 数量 | 说明 |
|---|---:|---|
| 与本树字节一致 | 201 | 直接保留魔改实现 |
| 本树有意改写 | 5 | 见下表 |
| 仅文件名不同 | 3 | 迁移编号冲突，上一版已改名 |

本树改写而不是原样复制的 5 个文件：

| 文件 | 原因 |
|---|---|
| `openai_ws_mode1_tls_test.go` | 官方 0.2.5 WebSocket acquire 增加参数 |
| `security_policy_test.go` | 补齐官方/Pro 审核仓库接口 |
| `account_proxy_mode_check_migration_test.go` | 读取 `239_` 而不是魔改的 `235_` |
| `frontend/src/styles/relay.css` | 仅换行差异 |
| `IntelligentTestsView.vue` | 仅换行差异 |

与官方 `v0.2.5` 重叠、且魔改相对 0.2.4 有改动的 203 个文件：

- 111 个本树与魔改字节一致；其中 **0 个** 同时被官方 0.2.5 改过，因此没有丢掉官方补丁。
- 92 个做了三方合并。抽查调度、安全审计、`overturned`、分组 platform allowlist、账号编辑分区、用户仪表盘后，魔改语义都在，官方新增平台/`opencode_go` 也保留。

上一版已改名的迁移继续有效：

| 原魔改文件 | 本树编号 |
|---|---|
| `235_account_proxy_mode_check.sql` | `239` |
| `237_group_security_policy.sql` | `240` |
| `238_content_moderation_overturned.sql` | `249` |
| `238_purge_unlimited_user_platform_quotas.sql` | `250` |

## 4. 明确不合并

- 魔改根目录本地审查备忘与 `verification/` 运行日志
- `docs/MANAGEMENT_FEATURES.md`、`FEATURE_ACCEPTANCE.md`、`SERVER_DEPLOYMENT_*.md` 中的具体站点/备份路径
- `__pycache__` 与临时构建产物
- 魔改 `update_service` 若指向官方仓库的部分（Pro 已改 Fork）

本版本补入产品文档 `docs/STRATEGY_TEST_MATRIX.md`（去掉本地审查文件引用）。

## 5. 结论

`sub2魔改` 的运行时代码已经全部在 `v0.2.5-pro.1` 中。`v0.2.5-pro.2` 是对这份魔改目录重新核对后的发布戳，产品行为与上一版一致，版本号改为 `0.2.5-pro.2`。
