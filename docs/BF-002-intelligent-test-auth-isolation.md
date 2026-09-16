# BF-002：智能测试隔离认证更新副作用

智能测试只观察当前账号认证状态。认证缺失、过期或没有可信到期时间时，测试记录为账号异常，提示管理员先在账号管理中单独刷新认证。测试不会轮换 OAuth refresh token、自动发现项目或注册 Agent Identity 任务。Vertex 服务账号仍可使用缓存中的短期 token，或用现有私钥签发短期 token。

## 问题与根因

智能测试 runner 已使用只读账号仓库，但 Gemini 等认证提供器仍持有原有真实仓库。Gemini OAuth 在没有 `project_id` 且 `auto_detect_project_id=true` 时，原路径可能请求项目发现接口，修改凭据内的 `project_id`、`tier_id` 并持久化，随后将测试由 AI Studio 路由切换至 Code Assist。Antigravity 也存在认证回填路径。令牌提供器的缓存和自动刷新流程同样绕开 runner 的只读边界。

只拒绝最终数据库写入仍不足以解决问题：OAuth 提供商可能已经轮换 refresh token；丢弃新凭据会使旧凭据失效。Agent Identity 的外部任务注册也必须在发起请求之前阻断，不能等到本地保存失败再处理。

## 最小修复

| 文件 | 变更 |
| --- | --- |
| [intelligent_test_auth.go](../backend/internal/service/intelligent_test_auth.go) | 统一读取账号快照中的非空、明确未过期 access token；不读共享 OAuth 缓存，不触发刷新 |
| [gemini_token_provider.go](../backend/internal/service/gemini_token_provider.go) | 智能上下文提前返回已有 OAuth token，阻断项目发现与持久化；保留 service account 分支 |
| [claude_token_provider.go](../backend/internal/service/claude_token_provider.go) | 智能上下文跳过 OAuth 缓存和刷新，保留 service account 分支 |
| [antigravity_token_provider.go](../backend/internal/service/antigravity_token_provider.go) | 智能上下文跳过 OAuth 缓存、刷新、回填和调度状态变化；静态上游 API key 路径保持原行为 |
| [grok_token_provider.go](../backend/internal/service/grok_token_provider.go) | 普通 token 入口与手动测试入口均受保护，仍拒绝缺失的已配置代理 |
| [oauth_refresh_api.go](../backend/internal/service/oauth_refresh_api.go) | 智能上下文在锁、仓库读取及刷新执行器之前返回错误，作为共享刷新入口的补充防护 |
| [openai_agent_identity.go](../backend/internal/service/openai_agent_identity.go) | 已有有效任务可生成临时签名；缺失任务或无效任务替换在注册之前返回错误 |
| [intelligent_test_auth_test.go](../backend/internal/service/intelligent_test_auth_test.go) | 四平台认证边界、实际本地 HTTP 路由、刷新入口、任务注册及 Vertex 兼容回归 |

以上分支仅适用于智能测试上下文。原有网关调用和账号连通性测试仍使用各自既有认证刷新策略。此次认证修复不修改数据库结构，也不需要新增 migration。

## 证据、发现与执行路径

| 证据 | 发现 | 执行路径 |
| --- | --- | --- |
| [认证隔离测试日志](../verification/features/intelligent-tests-auth-isolation.log)，`TestIntelligentGeminiRunnerDoesNotDiscoverProjectOrChangeRoute` | [V] 自动发现开启且缺少项目 ID 时，真实本地 HTTP 请求仍使用原 AI Studio 模型路径，无发现调用、无账号写入；过期 token 不发上游请求 | `RunIntelligentTest → buildGeminiOAuthRequest → GeminiTokenProvider.GetAccessToken` |
| 同日志，`TestIntelligentOAuthProvidersUseSnapshotWithoutCacheOrRotation` | [V] Gemini、Claude、Antigravity、Grok 共 16 个边界组合；有效 token 可用，过期/缺失 token/缺少有效期被拒绝，缓存读取、刷新执行器和仓库写入计数为零 | 提供器智能上下文分支 → 账号凭据快照 |
| 同日志，`TestIntelligentRefreshAPIBlocksBeforeExecutorAndRepository` | [V] 共享刷新入口在任何执行器调用及仓库读取前拒绝智能测试 | `OAuthRefreshAPI.RefreshIfNeeded` |
| 同日志，`TestIntelligentAgentIdentityNeverRegistersOrReplacesTask` | [V] 现有任务可签名；缺失或待替换任务不会访问本地注册 HTTP 服务 | `buildAgentIdentityAuthenticationHeaders → ensureAgentIdentityTaskForAccount` |
| 同日志，两个 `TestIntelligentVertexServiceAccount*` | [V] Gemini/Claude 的服务账号缓存仍可用，受控 HTTP 令牌端点接受 JWT grant 并返回短期 token，原私钥/账号凭据未变 | `getVertexServiceAccountAccessToken`、`exchangeVertexServiceAccountToken` |
| [含 unit 标签的兼容回归](../verification/features/intelligent-tests-auth-compatibility-unit.log) | [V] 114 个顶层测试、167 项含子测试通过，零失败；覆盖现有提供器、共享刷新、Agent Identity、Vertex 和智能测试 | `go test -tags unit ./internal/service -run ... -count=1 -v` |
| [静态检查日志](../verification/features/intelligent-tests-auth-vet.log) | [V] service、repository、admin handler、user handler 四包 `go vet` 退出 0，无输出 | Go 静态分析 |

认证隔离日志包含 17 个顶层测试、38 项含子测试。默认标签兼容回归另有 33 个顶层测试、56 项含子测试，见[日志](../verification/features/intelligent-tests-auth-compatibility.log)。这些是有重叠的执行集合，不将三份日志相加作为独立测试数量。

在 `backend/` 内可复跑核心验证：

```powershell
go test -tags unit ./internal/service -run 'Test(Intelligent|GeminiTokenProvider|ClaudeTokenProvider|AntigravityTokenProvider|GrokTokenProvider|RefreshIfNeeded|AccountTestService.*AgentIdentity|OpenAIAgentIdentity|EnsureAgentIdentity|BuildAgentIdentity|Vertex|ExchangeVertex)' -count=1 -v
go vet ./internal/service ./internal/repository ./internal/handler/admin ./internal/handler
```

## 验证边界与回滚

[V] 本地受控 HTTP 端点、实际提供器入口和现有兼容测试已执行。[T] 本记录不提供商业上游真实账号成功率或模型质量结论；刷新期间仍可能发生外部凭据并发更新，此时本次测试使用自己的快照并可能返回账号异常。管理员单独刷新后可以重新测试。没有明确到期时间的 OAuth 凭据采用拒绝测试的安全策略。

审查状态为代码自审与工具验证；没有虚构独立签署。未创建 Git commit、tag 或 checkpoint。应用级回滚应使用已保留的旧镜像；此次改动无需回退数据库。若仅回退此补丁，应同时回退本表的六处认证入口及 helper，且该操作会重新开放测试认证副作用，不能视为安全修复。不要回退或删除测试历史、账号数据或用户数据。
