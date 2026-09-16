# 本轮功能变更清单

本清单以原交付 ZIP 与当前 `sub2新站` 目录逐文件比较，记录用户清理、智能测试、账号保护、分级权限及配套接入。**旧镜像只识别 `admin/user`；回滚前必须处理角色兼容，不能只切换镜像。** 危险操作的新版确认参数及权限收紧见后文。

## 比较基线与范围

- 采样时间：2026-09-13 00:01:33 +08:00。
- 基线：`交付包/chengchuan-relay-20260912.zip`，ZIP 内根目录 `chengchuan-relay/`，共 4,010 个文件条目。
- 基线 ZIP SHA-256：`43953fb27ec9e9a258db3c361c596edb60081f4eed103f70306c0c4cbf41ed84`。
- 比较方法：去除 ZIP 根目录后，以相对路径匹配当前目录，使用原始字节 SHA-256 判断修改；不依赖没有基线的 Git 状态，也不把换行变化自动视作相同。
- 排除依赖、构建/缓存、运行验证产物目录：`.cache, .git, .pnpm-store, .tools, .turbo, .vite, __pycache__, build, coverage, dist, node_modules, target, verification`；排除 `.pyc/.exe/.test/.tsbuildinfo/.log` 以及 `frontend/vite.config.js`、`frontend/vite.config.d.ts`。测试源代码、迁移、Wire 接入源码仍列入。
- 状态：`A` 新增，`M` 修改，`D` 基线文件缺失。当前计数 **新增 62、修改 71、缺失 0，合计 133 个文件**；含本清单本身。功能列表不包含 `verification/` 产物，证据章节仅引用已有日志。
- [V] 基线中的旧 migration 没有修改或删除。新增数据库变化由 `245`、`246`、`247` 三个迁移提供。
- [H] 文件差异证明当前目录相对交付物的变化范围，不证明每个文件都由本轮某位代理创建；`openspec/config.yaml` 来源未归因，单列记录。

## 按功能核对文件

同一共享文件仅列一次；`UserEditModal` 同时接入清理保护与角色选项，`AccountsView` 同时接入保护开关和测试入口。父线程后续的两组账号横排布局、详情复制工具以及公开能力入口锚点归在对应智能测试/共享页面文件中。

### 用户清理

复用软删除与后台时间，增加预览、候选复验、幂等执行、资产/活动保护和审计；没有执行真实用户清理。 共 14 个文件（新增 12、修改 2、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| A | [backend/internal/handler/admin/user_cleanup_handler.go](../backend/internal/handler/admin/user_cleanup_handler.go) |
| A | [backend/internal/handler/admin/user_cleanup_handler_test.go](../backend/internal/handler/admin/user_cleanup_handler_test.go) |
| A | [backend/internal/service/user_cleanup.go](../backend/internal/service/user_cleanup.go) |
| A | [backend/internal/service/user_cleanup_execute.go](../backend/internal/service/user_cleanup_execute.go) |
| A | [backend/internal/service/user_cleanup_identity.go](../backend/internal/service/user_cleanup_identity.go) |
| A | [backend/internal/service/user_cleanup_postgres_test.go](../backend/internal/service/user_cleanup_postgres_test.go) |
| A | [backend/migrations/245_user_cleanup.sql](../backend/migrations/245_user_cleanup.sql) |
| A | [frontend/src/api/admin/userCleanup.ts](../frontend/src/api/admin/userCleanup.ts) |
| A | [frontend/src/components/admin/user/UserCleanupControl.vue](../frontend/src/components/admin/user/UserCleanupControl.vue) |
| A | [frontend/src/components/admin/user/UserCleanupGuard.vue](../frontend/src/components/admin/user/UserCleanupGuard.vue) |
| A | [frontend/src/components/admin/user/__tests__/UserCleanupControl.spec.ts](../frontend/src/components/admin/user/__tests__/UserCleanupControl.spec.ts) |
| A | [frontend/src/components/admin/user/__tests__/UserCleanupGuard.spec.ts](../frontend/src/components/admin/user/__tests__/UserCleanupGuard.spec.ts) |
| M | [frontend/src/views/admin/UsersView.vue](../frontend/src/views/admin/UsersView.vue) |
| M | [frontend/src/views/admin/__tests__/UsersView.spec.ts](../frontend/src/views/admin/__tests__/UsersView.spec.ts) |

### 账号保护与测试身份

所有创建路径强制开启保护；普通写入保留已提交保护状态；独立确认关闭；新测试复用稳定设备身份与独立会话，保持原连接测试行为。 共 18 个文件（新增 9、修改 9、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| M | [backend/internal/handler/admin/anti_degrade_handler.go](../backend/internal/handler/admin/anti_degrade_handler.go) |
| A | [backend/internal/repository/account_protection.go](../backend/internal/repository/account_protection.go) |
| A | [backend/internal/repository/account_protection_database_test.go](../backend/internal/repository/account_protection_database_test.go) |
| A | [backend/internal/repository/account_protection_integration_test.go](../backend/internal/repository/account_protection_integration_test.go) |
| M | [backend/internal/repository/account_repo.go](../backend/internal/repository/account_repo.go) |
| M | [backend/internal/repository/scheduler_cache.go](../backend/internal/repository/scheduler_cache.go) |
| M | [backend/internal/service/account_anti_degrade.go](../backend/internal/service/account_anti_degrade.go) |
| M | [backend/internal/service/account_mode1_protection.go](../backend/internal/service/account_mode1_protection.go) |
| A | [backend/internal/service/account_protection.go](../backend/internal/service/account_protection.go) |
| A | [backend/internal/service/account_protection_test.go](../backend/internal/service/account_protection_test.go) |
| M | [backend/internal/service/admin_account.go](../backend/internal/service/admin_account.go) |
| A | [backend/internal/service/intelligent_test_protection.go](../backend/internal/service/intelligent_test_protection.go) |
| A | [backend/internal/service/intelligent_test_protection_test.go](../backend/internal/service/intelligent_test_protection_test.go) |
| M | [frontend/src/api/admin/accounts.ts](../frontend/src/api/admin/accounts.ts) |
| M | [frontend/src/components/account/EditAccountModal.vue](../frontend/src/components/account/EditAccountModal.vue) |
| A | [frontend/src/components/account/ProtectionToggle.vue](../frontend/src/components/account/ProtectionToggle.vue) |
| M | [frontend/src/components/account/__tests__/EditAccountModal.antiDegrade.spec.ts](../frontend/src/components/account/__tests__/EditAccountModal.antiDegrade.spec.ts) |
| A | [frontend/src/components/account/__tests__/ProtectionToggle.spec.ts](../frontend/src/components/account/__tests__/ProtectionToggle.spec.ts) |

### 智能测试与公开能力检测

独立持久任务表、队列租约、配置快照、可扩展评分器、卡片/详情/历史；用户结果按当前分组和可见性授权。认证读取与策略写入隔离。 共 40 个文件（新增 29、修改 11、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| A | [backend/internal/handler/account_capability_handler.go](../backend/internal/handler/account_capability_handler.go) |
| A | [backend/internal/handler/account_capability_handler_test.go](../backend/internal/handler/account_capability_handler_test.go) |
| A | [backend/internal/handler/admin/intelligent_test_handler.go](../backend/internal/handler/admin/intelligent_test_handler.go) |
| A | [backend/internal/handler/admin/intelligent_test_handler_test.go](../backend/internal/handler/admin/intelligent_test_handler_test.go) |
| A | [backend/internal/repository/intelligent_test_integration_test.go](../backend/internal/repository/intelligent_test_integration_test.go) |
| A | [backend/internal/repository/intelligent_test_public.go](../backend/internal/repository/intelligent_test_public.go) |
| A | [backend/internal/repository/intelligent_test_queue.go](../backend/internal/repository/intelligent_test_queue.go) |
| A | [backend/internal/repository/intelligent_test_repo.go](../backend/internal/repository/intelligent_test_repo.go) |
| M | [backend/internal/service/account_test_service.go](../backend/internal/service/account_test_service.go) |
| M | [backend/internal/service/account_test_service_cn_adaptive.go](../backend/internal/service/account_test_service_cn_adaptive.go) |
| M | [backend/internal/service/antigravity_gateway_service.go](../backend/internal/service/antigravity_gateway_service.go) |
| M | [backend/internal/service/antigravity_token_provider.go](../backend/internal/service/antigravity_token_provider.go) |
| M | [backend/internal/service/claude_token_provider.go](../backend/internal/service/claude_token_provider.go) |
| M | [backend/internal/service/gemini_token_provider.go](../backend/internal/service/gemini_token_provider.go) |
| M | [backend/internal/service/grok_token_provider.go](../backend/internal/service/grok_token_provider.go) |
| A | [backend/internal/service/intelligent_test_auth.go](../backend/internal/service/intelligent_test_auth.go) |
| A | [backend/internal/service/intelligent_test_auth_test.go](../backend/internal/service/intelligent_test_auth_test.go) |
| A | [backend/internal/service/intelligent_test_evaluator.go](../backend/internal/service/intelligent_test_evaluator.go) |
| A | [backend/internal/service/intelligent_test_runner.go](../backend/internal/service/intelligent_test_runner.go) |
| A | [backend/internal/service/intelligent_test_runner_test.go](../backend/internal/service/intelligent_test_runner_test.go) |
| A | [backend/internal/service/intelligent_test_service.go](../backend/internal/service/intelligent_test_service.go) |
| A | [backend/internal/service/intelligent_test_types.go](../backend/internal/service/intelligent_test_types.go) |
| M | [backend/internal/service/oauth_refresh_api.go](../backend/internal/service/oauth_refresh_api.go) |
| M | [backend/internal/service/openai_agent_identity.go](../backend/internal/service/openai_agent_identity.go) |
| M | [backend/internal/service/openai_plugin_transport.go](../backend/internal/service/openai_plugin_transport.go) |
| A | [backend/migrations/247_intelligent_tests.sql](../backend/migrations/247_intelligent_tests.sql) |
| A | [frontend/src/api/__tests__/intelligentTests.spec.ts](../frontend/src/api/__tests__/intelligentTests.spec.ts) |
| A | [frontend/src/api/intelligentTests.ts](../frontend/src/api/intelligentTests.ts) |
| A | [frontend/src/components/account/UserAccountCapabilities.vue](../frontend/src/components/account/UserAccountCapabilities.vue) |
| A | [frontend/src/components/admin/intelligent-tests/AccountManagementTabs.vue](../frontend/src/components/admin/intelligent-tests/AccountManagementTabs.vue) |
| A | [frontend/src/components/admin/intelligent-tests/QuickTestDialog.vue](../frontend/src/components/admin/intelligent-tests/QuickTestDialog.vue) |
| A | [frontend/src/components/admin/intelligent-tests/TestDetailDialog.vue](../frontend/src/components/admin/intelligent-tests/TestDetailDialog.vue) |
| A | [frontend/src/components/admin/intelligent-tests/TestHistoryTable.vue](../frontend/src/components/admin/intelligent-tests/TestHistoryTable.vue) |
| A | [frontend/src/components/admin/intelligent-tests/TestResultCard.vue](../frontend/src/components/admin/intelligent-tests/TestResultCard.vue) |
| A | [frontend/src/components/admin/intelligent-tests/TestSettingsPanel.vue](../frontend/src/components/admin/intelligent-tests/TestSettingsPanel.vue) |
| A | [frontend/src/components/admin/intelligent-tests/TestStatusBadge.vue](../frontend/src/components/admin/intelligent-tests/TestStatusBadge.vue) |
| A | [frontend/src/components/admin/intelligent-tests/__tests__/intelligentTests.spec.ts](../frontend/src/components/admin/intelligent-tests/__tests__/intelligentTests.spec.ts) |
| A | [frontend/src/components/admin/intelligent-tests/display.ts](../frontend/src/components/admin/intelligent-tests/display.ts) |
| A | [frontend/src/views/admin/IntelligentTestsView.vue](../frontend/src/views/admin/IntelligentTestsView.vue) |
| M | [frontend/src/views/user/AvailableChannelsView.vue](../frontend/src/views/user/AvailableChannelsView.vue) |

### 角色与权限

增加 super_admin，兼容既有 admin 判断；普通管理员保留账号及测试管理，管理员账户与全局安全能力由超级管理员管理。共 35 个文件（新增 7、修改 28、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| M | [backend/internal/domain/constants.go](../backend/internal/domain/constants.go) |
| M | [backend/internal/handler/admin/user_handler.go](../backend/internal/handler/admin/user_handler.go) |
| M | [backend/internal/handler/auth_handler.go](../backend/internal/handler/auth_handler.go) |
| M | [backend/internal/handler/channel_monitor_v2_handler.go](../backend/internal/handler/channel_monitor_v2_handler.go) |
| M | [backend/internal/handler/page_handler.go](../backend/internal/handler/page_handler.go) |
| M | [backend/internal/repository/simple_mode_admin_concurrency.go](../backend/internal/repository/simple_mode_admin_concurrency.go) |
| M | [backend/internal/repository/user_repo.go](../backend/internal/repository/user_repo.go) |
| A | [backend/internal/repository/user_hierarchy.go](../backend/internal/repository/user_hierarchy.go) |
| A | [backend/internal/repository/user_hierarchy_database_test.go](../backend/internal/repository/user_hierarchy_database_test.go) |
| M | [backend/internal/server/middleware/admin_only.go](../backend/internal/server/middleware/admin_only.go) |
| M | [backend/internal/server/middleware/backend_mode_guard.go](../backend/internal/server/middleware/backend_mode_guard.go) |
| M | [backend/internal/server/middleware/panel_rate_limit.go](../backend/internal/server/middleware/panel_rate_limit.go) |
| M | [backend/internal/server/middleware/server_timing.go](../backend/internal/server/middleware/server_timing.go) |
| A | [backend/internal/server/middleware/user_hierarchy.go](../backend/internal/server/middleware/user_hierarchy.go) |
| A | [backend/internal/server/middleware/user_hierarchy_test.go](../backend/internal/server/middleware/user_hierarchy_test.go) |
| M | [backend/internal/service/admin_service_role_test.go](../backend/internal/service/admin_service_role_test.go) |
| M | [backend/internal/service/admin_user.go](../backend/internal/service/admin_user.go) |
| M | [backend/internal/service/domain_constants.go](../backend/internal/service/domain_constants.go) |
| M | [backend/internal/service/totp_service.go](../backend/internal/service/totp_service.go) |
| M | [backend/internal/service/user.go](../backend/internal/service/user.go) |
| A | [backend/internal/service/user_hierarchy.go](../backend/internal/service/user_hierarchy.go) |
| A | [backend/internal/service/user_hierarchy_test.go](../backend/internal/service/user_hierarchy_test.go) |
| M | [backend/internal/setup/setup.go](../backend/internal/setup/setup.go) |
| A | [backend/migrations/246_account_protection_roles.sql](../backend/migrations/246_account_protection_roles.sql) |
| M | [frontend/src/api/admin/users.ts](../frontend/src/api/admin/users.ts) |
| M | [frontend/src/components/admin/user/UserCreateModal.vue](../frontend/src/components/admin/user/UserCreateModal.vue) |
| M | [frontend/src/components/admin/user/UserEditModal.vue](../frontend/src/components/admin/user/UserEditModal.vue) |
| M | [frontend/src/components/admin/user/__tests__/UserEditModal.spec.ts](../frontend/src/components/admin/user/__tests__/UserEditModal.spec.ts) |
| M | [frontend/src/components/user/profile/ProfileInfoCard.vue](../frontend/src/components/user/profile/ProfileInfoCard.vue) |
| M | [frontend/src/composables/useOnboardingTour.ts](../frontend/src/composables/useOnboardingTour.ts) |
| M | [frontend/src/i18n/locales/en/admin/overview.ts](../frontend/src/i18n/locales/en/admin/overview.ts) |
| M | [frontend/src/i18n/locales/zh/admin/overview.ts](../frontend/src/i18n/locales/zh/admin/overview.ts) |
| M | [frontend/src/router/meta.d.ts](../frontend/src/router/meta.d.ts) |
| M | [frontend/src/stores/auth.ts](../frontend/src/stores/auth.ts) |
| M | [frontend/src/views/admin/SettingsView.vue](../frontend/src/views/admin/SettingsView.vue) |

### 共享接入与页面集成

路由、依赖注入、DTO、类型和页面布局接入；保留旧账号列表、编辑/导入、统计及连接测试。 共 22 个文件（新增 1、修改 21、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| M | [backend/cmd/server/wire.go](../backend/cmd/server/wire.go) |
| M | [backend/cmd/server/wire_gen.go](../backend/cmd/server/wire_gen.go) |
| M | [backend/cmd/server/wire_gen_test.go](../backend/cmd/server/wire_gen_test.go) |
| M | [backend/internal/handler/dto/mappers.go](../backend/internal/handler/dto/mappers.go) |
| M | [backend/internal/handler/dto/types.go](../backend/internal/handler/dto/types.go) |
| M | [backend/internal/handler/handler.go](../backend/internal/handler/handler.go) |
| M | [backend/internal/handler/wire.go](../backend/internal/handler/wire.go) |
| M | [backend/internal/repository/wire.go](../backend/internal/repository/wire.go) |
| M | [backend/internal/server/http.go](../backend/internal/server/http.go) |
| M | [backend/internal/server/router.go](../backend/internal/server/router.go) |
| M | [backend/internal/server/routes/admin.go](../backend/internal/server/routes/admin.go) |
| M | [backend/internal/server/routes/ops_ingress_reject_routes_test.go](../backend/internal/server/routes/ops_ingress_reject_routes_test.go) |
| M | [backend/internal/server/routes/prompt_audit_route_coverage_test.go](../backend/internal/server/routes/prompt_audit_route_coverage_test.go) |
| A | [backend/internal/server/routes/relay_management.go](../backend/internal/server/routes/relay_management.go) |
| M | [backend/internal/server/routes/user.go](../backend/internal/server/routes/user.go) |
| M | [backend/internal/service/wire.go](../backend/internal/service/wire.go) |
| M | [frontend/src/components/layout/AppHeader.vue](../frontend/src/components/layout/AppHeader.vue) |
| M | [frontend/src/components/layout/AppLayout.vue](../frontend/src/components/layout/AppLayout.vue) |
| M | [frontend/src/components/layout/AppSidebar.vue](../frontend/src/components/layout/AppSidebar.vue) |
| M | [frontend/src/router/index.ts](../frontend/src/router/index.ts) |
| M | [frontend/src/types/index.ts](../frontend/src/types/index.ts) |
| M | [frontend/src/views/admin/AccountsView.vue](../frontend/src/views/admin/AccountsView.vue) |

### 文档

功能说明、审查修复记录和可复现变更清单。 共 3 个文件（新增 3、修改 0、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| A | [docs/BF-002-intelligent-test-auth-isolation.md](../docs/BF-002-intelligent-test-auth-isolation.md) |
| A | [docs/FEATURE_CHANGE_MANIFEST.md](../docs/FEATURE_CHANGE_MANIFEST.md) |
| A | [docs/MANAGEMENT_FEATURES.md](../docs/MANAGEMENT_FEATURES.md) |

### 其他新增配置

仅记录检测到的额外配置差异，不将模板文件归为核心业务实现。 共 1 个文件（新增 1、修改 0、缺失 0）。

| 状态 | 相对项目根目录的完整路径 |
| --- | --- |
| A | [openspec/config.yaml](../openspec/config.yaml) |

## 权限与旧 API 兼容性复核

本节是对当前源码及 ZIP 基线的只读复核；本次整理文档没有重跑已通过测试。

| 范围 | 兼容情况与限制 | 实现依据 |
| --- | --- | --- |
| 旧路由 | [V] `routes/admin.go` 与 `routes/user.go` 原有静态注册的 HTTP 方法/局部路径没有删除。新功能通过独立注册函数增加；不代表所有旧请求的权限行为完全不变。 | `server/routes/relay_management.go`、两个原路由文件 |
| 认证 | [V] JWT、Admin API Key 入口及凭据格式保留。JWT 仍重读当前用户状态/角色与 token version。`GetFirstAdmin` 优先选择有效 `super_admin`，再选择有效 `admin`，保留全局管理密钥的管理用途。 | `server/middleware/admin_auth.go`（未改）、`repository/user_repo.go` |
| 角色字段 | [V] 返回值及类型增加 `super_admin`。原来只枚举 `admin/user` 的外部客户端必须更新枚举和后台判断。新建部署默认超级管理员；迁移在不存在未删除超级管理员时仅提升最早的有效管理员，不改密码/余额。 | `migrations/246_account_protection_roles.sql`、`setup/setup.go`、`service/user.go` |
| 管理员基础权限 | [V] 后台模式、管理员中间件、管理页面、限流豁免、计时信息、渠道监控及 TOTP 的管理员判断同时识别两种管理角色。普通用户不能访问后台测试或保护修改接口。 | `server/middleware/`、`handler/` 与角色文件列表 |
| 普通管理员管理用户 | [V] 可创建/修改普通用户。不能通过角色变更、自提权、修改管理账号密码/余额/TOTP/身份或全量用户批处理扩大权限；需要批处理时选择普通用户 ID。自身密码等个人操作继续使用原用户端接口。 | `server/middleware/user_hierarchy.go`、`service/user_hierarchy.go`、`handler/admin/user_handler.go` |
| 全局管理能力 | [V] 全局管理密钥的读取/生成/删除、备份/恢复、数据管理，以及系统设置/插件写入改为超级管理员权限。普通管理员的必要只读设置接口保留；前端系统设置/插件入口隐藏。 | `user_hierarchy.go`、`router/index.ts`、`AppSidebar.vue` |
| 原防降智预览/应用 | [V] 原 `GET .../anti-degrade`、`POST .../anti-degrade/apply` 保留。新增保护接口使用已有 Account DTO，并附加保护状态/范围字段。 | `handler/admin/anti_degrade_handler.go`、`api/admin/accounts.ts` |
| 原防降智还原 | [V] `POST .../anti-degrade/revert` 路径保留，但现在必须提交 `{"confirm_disable":true}`；旧空请求返回 400。这是满足管理员明确关闭要求的有意变化，旧脚本需调整。 | `AntiDegradeHandler.Revert` |
| 新保护开关 | [V] `POST .../accounts/:id/protection` 关闭必须 `enabled:false` 且 `confirm_disable:true`；只提供批量开启接口。普通更新/批量导入不能通过 extra 绕过。新账号默认开启，旧账号状态不被默认值批量覆盖。 | `account_protection.go`、`account_repo.go`、`SetProtection` |
| 公开测试 | [V] 普通用户仅能通过 `/account-capabilities` 专门只读接口查看当前有权访问账号、当前已公开测试结果；列表、历史、详情、图片同一授权谓词。原始响应、输入、备注及内部错误不在公开 DTO 中。 | `repository/intelligent_test_public.go`、`handler/account_capability_handler.go` |
| 上游认证 | [V] 智能测试新增上下文才采用认证只读策略；原连接测试/日常调用仍保留原刷新路径。缺失或过期 OAuth 凭据报告管理员先刷新；禁止测试自动注册 Agent Identity 任务。Vertex service account 正常短期令牌签发保留。 | `intelligent_test_auth.go`、provider 与 refresh/identity guard |

[V] 角色写入边界已由 repository 事务级 advisory lock 与行锁串行化；服务层前置检查仍用于尽早反馈，但最终事务会重新读取操作者、目标及管理员数量。普通自降级及最后超级管理员降级均在最终写入前拦截。隔离数据库并发演练因测试 DSN 返回 `EOF` 尚未完成，见 E6。

## 旧镜像回滚的最小兼容步骤

以下是维护方案，**未执行，也没有为写文档修改任何数据库数据**。

1. 停止新应用及测试 worker，保留 PostgreSQL、Redis、数据库备份和当前镜像标识。检查没有仍在写入角色或执行任务的实例。
2. 在维护事务内先保存当前全部 `super_admin` 的用户 ID/原角色映射到独立安全文件；确认映射文件保存成功，再仅将这些行临时改成 `admin`。不调整密码、余额、API Key、用户业务记录或测试历史。原角色映射必须随回滚记录保留，不能只依赖旧的部署前备份。
3. 启动已备份的旧镜像，重新登录，确认后台可用。旧版的所有管理员重新拥有其原来同级管理权限，不能继续声称新版的管理员分级仍然生效。
4. 保留新增表和 `schema_migrations` 条目；不要删除迁移记录来强迫重跑，也不要为了应用回滚覆盖整库。迁移执行器逐个检查镜像内已知 migration，没有依据未来条目拒绝启动的分支，但完整旧镜像回滚演练未在本次文档任务中执行。
5. 重新升级时先停旧应用，根据回滚时保存的 ID 映射核对账号身份、删除/状态及回滚期间角色变动，再恢复应恢复的 `super_admin`，随后启动新镜像。`246` 已记录执行，不会仅因换回新镜像而自动再提升角色；不能假设自动恢复。

依据：ZIP 原 `service/user.go` 的 `IsAdmin()` 只接受 `admin`，原后台模式登录及前端也只认 `admin`。因此保留 `super_admin` 直接换旧镜像会失去后台权限，后台模式下还可能被拒绝登录。

## 审查发现、修复与证据

`[V]` 表示有源码/工具证据支持的事实；`[H]` 表示静态推断；`[T]` 表示尚待执行。已保留失败尝试，不把失败日志改名后宣称全部通过。

| Evidence → Finding → Path | 修复结果 | 现有验证证据 |
| --- | --- | --- |
| E1：旧整对象/局部/批量写路径 → F1：导入或刷新可能覆盖保护开关 → P1：repository 创建边界默认 + 行锁下保留当前值 + JSONB 原子保护字段 | [V] 新建忽略传入 false；管理员确认关闭后，迟到的旧 ON 快照不能重开；管理员开启后旧 OFF 快照不能关闭。种子与并发边界保留。 | [postgres-final.jsonl](../verification/protection/postgres-final.jsonl)：真实隔离 PostgreSQL，4 个顶层测试通过；对应 `account_protection_*test.go` |
| E2：原 admin 可获取全局管理密钥及恢复整库 → F2：仅挡用户角色接口不足以建立分级 → P2：后端全局能力 guard + 前端入口与 fetch 限制 | [V] 非超级管理员不能利用全局管理密钥、备份恢复或插件写入取得同等高权限；常规普通用户管理保留。 | `user_hierarchy_test.go`、`user_hierarchy.go`；[backend-final.jsonl](../verification/protection/backend-final.jsonl) 中 middleware/service/handler/DTO 包通过 |
| E3：智能 runner 复用共享 Gemini/provider 服务 → F3：自动发现 project_id/tier_id 或 OAuth 旋转绕过只读 repository，并可能改变路由 → P3：智能上下文只用已持久化未过期 OAuth 凭据，刷新/注册前拒绝 | [V] 阻断自动发现/写入、共享缓存带来的状态替换及刷新；在上游旋转发生前阻止，不丢弃已旋转凭据。Vertex 无账号持久化的短期认证路径保留。 | [intelligent-tests-auth-isolation.log](../verification/features/intelligent-tests-auth-isolation.log)、[intelligent-tests-auth-compatibility-unit.log](../verification/features/intelligent-tests-auth-compatibility-unit.log)；`intelligent_test_auth_test.go` |
| E4：常规 Responses 测试只复用传输/并发 → F4：显示保护开启却遗漏设备投影与独立会话 → P4：智能测试专属保护 helper + 请求语义校验 | [V] 两次真实本地 HTTP 捕获证明相同设备、不同 session/thread、body/header 身份一致、无账号写入；原连接测试路径不受影响；提示词变更被拒绝。 | [intelligent-protection.jsonl](../verification/protection/intelligent-protection.jsonl)：2/2；`intelligent_test_protection_test.go` |
| E5：设置保存返回可变表单对象 → F5：等待响应期间编辑可能将未提交值显示为已保存 → P5：API/组件提交快照、保存锁定、再编辑清除保存提示 | [V] 网络返回只确认已提交的题目/答案/可见性，重复提交被阻止。 | [intelligent-settings.json](../verification/protection/intelligent-settings.json)：10/10；API 与组件行为测试 |
| E6：服务层最后超级管理员检查与写入分离 → F6：两个副本可并发互降并同时通过前置检查 → P6：repository 事务级 PostgreSQL advisory lock、目标/操作者 `FOR UPDATE`，最终重读角色和管理员数量 | [V] 最终写入边界重新校验操作者状态、超级管理员权限、自我降级、管理员保护及最后超级管理员；外部 Ent 事务复用同一锁并由调用方提交/回滚。 | [T] `user_hierarchy_database_test.go` 已加入并发、外部事务及迟到授权场景；隔离 DSN 当前连接返回 `EOF`，恢复后须运行并记录 JSONL |

验证说明：

- [V] `verification/protection/frontend-tests.json` 是前期保护开关、账号编辑、用户编辑 16/16 通过记录；`postgres-final.jsonl` 是后续真实数据库复验的通过记录。
- [V] `backend-tests.jsonl`、`backend-final.jsonl` 中 repository 包存在早期夹具问题（缺 scheduler_outbox、局部变量遮蔽 schema 导入）；这些文件不是全绿报告。夹具已修复，repository 后续通过以 `postgres-final.jsonl` 为准。
- [V] 智能认证隔离日志为 17 个顶层/38 个含子测试通过；默认构建兼容日志为 33/56；带 `unit` 标签的认证兼容回归为 114/167，均零失败。计数按测试日志的 `--- PASS` 层级区分，不能累加成独立业务场景数。
- [V] 本代理此前执行的定向 ESLint、`vue-tsc --noEmit`、`git diff --check` 均返回 0；本次清单整理没有重新运行。最终整站构建、隔离站 API 与浏览器结果由父线程的交付说明汇总。
- [T] 真实外网模型质量依赖实际可用上游账号；本地 HTTP 夹具验证协议、授权和持久化行为，不能当成真实模型质量结论。鹈鹕规则只校验静态 SVG 结构/安全性，糖果规则只核对配置答案。角色并发集成测试还需隔离 PostgreSQL 恢复后执行。

## 复现清单比较

在 `sub2新站` 根目录使用 Python 3 标准库执行以下脚本，仅打印差异，不写文件、不解压或修改基线。输出按路径排序；后续继续开发产生的新差异应重新采样，不能沿用本文计数。

```python
from pathlib import Path
from zipfile import ZipFile
from hashlib import sha256
import os

root = Path.cwd()
archive = root.parent / "交付包" / "chengchuan-relay-20260912.zip"
skip = {".git", "node_modules", "dist", "build", "target", "coverage",
        "verification", ".cache", ".vite", ".turbo", ".pnpm-store",
        "__pycache__", ".tools"}
generated = {"frontend/vite.config.js", "frontend/vite.config.d.ts"}

def included(name):
    p = Path(name)
    return (not any(part in skip for part in p.parts)
            and name not in generated
            and p.suffix not in {".pyc", ".exe", ".test", ".tsbuildinfo", ".log"})

current = {}
for directory, dirs, files in os.walk(root):
    dirs[:] = [name for name in dirs if name not in skip]
    for name in files:
        path = Path(directory) / name
        relative = path.relative_to(root).as_posix()
        if included(relative):
            current[relative] = path

print("Baseline SHA-256:", sha256(archive.read_bytes()).hexdigest())
with ZipFile(archive) as z:
    baseline = {entry.filename.split("/", 1)[1]: entry
                for entry in z.infolist()
                if not entry.is_dir() and "/" in entry.filename
                and included(entry.filename.split("/", 1)[1])}
    for name in sorted(set(baseline) | set(current)):
        if name not in baseline:
            print("A", name)
        elif name not in current:
            print("D", name)
        elif sha256(z.read(baseline[name])).digest() != sha256(current[name].read_bytes()).digest():
            print("M", name)
```
