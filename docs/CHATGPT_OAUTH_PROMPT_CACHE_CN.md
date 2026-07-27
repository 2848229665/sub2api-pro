# ChatGPT OAuth Prompt Cache 分析与优化

> 本文记录 sub2api-pro 对 ChatGPT OAuth（OpenAI OAuth / ChatGPT Codex 上游）
> Prompt Cache 的官方依据、生产基线、根因判断、实现边界和验收方法。分析时间为
> 2026-07-27；官方 Codex 源码基线为
> [`61a44880a85d2fd0d8770908dea5733495e571c8`](https://github.com/openai/codex/tree/61a44880a85d2fd0d8770908dea5733495e571c8)。

## 1. 结论

生产数据不支持“ChatGPT OAuth 原生 Responses 缓存整体失效”这一判断。

- `taihezhisuan.cc` 最近 48 小时 `/v1/responses` 的可缓存请求 token 加权命中率为
  `96.36%`；
- `sub2api.xiaofengai.cc` 同口径为 `94.13%`，样本量较小；
- 大量表面上的“未命中”来自不足 1024 input tokens、会话首轮冷启动，以及把请求数
  或全部请求直接当作缓存命中率分母；
- Priority Saturation 调度器快照中 `account_switch_total=0`，账号切换不是当前生产
  缓存损失的根因。

已经确认并修复的实际协议偏差有五类：

1. `/v1/messages` 兼容桥在 ChatGPT OAuth 转换后删除 body
   `prompt_cache_key`，只留下 session header；
2. `/v1/responses/compact` 规范化白名单删除 `prompt_cache_key`，与官方 Codex
   “普通 Responses 和 compact 复用同一个 key”的行为不一致；
3. 官方 Codex 发送 `session-id`，网关原先只识别旧别名 `session_id`，导致粘性调度、
   上游转发和 `usage_logs.session_id` 关联丢失该信号。
4. 官方 Codex 原生 Responses 已经具有正确的 `instructions/tools/input/text` 结构，
   旧翻译器仍将整包 JSON 解码为 map、再次规范化并重新序列化，没有保留客户端已经
   稳定的原生前缀。
5. body `prompt_cache_key`、session/thread headers 和 `client_metadata` 使用了不同的
   上游身份值，偏离官方 Codex “session key 一致、thread identity 独立”的请求形态。

其中第 1-3、5 项属于默认生效的协议字段和身份修复；第 4 项的“保留稳定 raw JSON
前缀”由默认开启、可在管理端关闭的原生 Codex Prompt Cache 优化器控制。无论是否开启优化器，网关都会
沿用客户端的会话级 key，对同一下游 API Key 做确定性隔离，并把隔离结果一致写入
body/header/已有 metadata；不会把多个无关会话合并到一个共享 key。

## 2. OpenAI 官方规则

权威来源：
[Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)。

当前官方规则与本项目直接相关的部分如下：

1. 只有 prompt 的**精确前缀匹配**才可能命中缓存。instructions、examples、images 和
   tools 等静态内容应放在前面，用户或每轮动态内容放在后面；tools 也必须保持一致。
2. 自动缓存的最低输入长度是 **1024 tokens**。低于 1024 tokens 的响应仍会返回
   `cached_tokens`，但值必然为 0，不能计为异常 miss。
3. 共享长前缀的请求应稳定复用 `prompt_cache_key`，帮助请求路由到同一缓存。
4. GPT-5.6 及后续模型必须提供 `prompt_cache_key`，才能使用更可靠的 implicit /
   explicit cache matching；只有 header 不能替代这个 body 参数。
5. 每个 key 的所有 prefix 合计流量建议保持在约 **15 RPM**。高流量必须使用稳定映射
   分片，不能用一个全局 key 汇聚所有用户。
6. 读命中使用 `cached_tokens` 衡量；GPT-5.6 及后续模型还应记录
   `cache_write_tokens`，比较 cache write 成本与后续 cache read 收益。

因此，优化目标不是“让所有请求都出现 cached tokens”，而是：

- 对达到缓存门槛且确实共享前缀的同一会话，保持 key 和前缀稳定；
- 不把冷启动、短 prompt 或动态 tools 造成的合理 miss 当成网关故障；
- 不以提高表面命中率为由制造跨用户会话碰撞或单 key 流量热点。

## 3. 官方 Codex 请求行为

官方仓库：
[`openai/codex`](https://github.com/openai/codex)，本地分析提交为
`61a44880a85d2fd0d8770908dea5733495e571c8`。

### 3.1 稳定的 body `prompt_cache_key`

[`codex-rs/core/src/client.rs#L483-L486`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/src/client.rs#L483-L486)
显示：

- 有显式 override 时使用 override；
- 否则默认使用稳定的 `responses_metadata.session_id`。

[`client.rs#L905-L920`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/src/client.rs#L905-L920)
把该值写入每次 `ResponsesApiRequest.prompt_cache_key`。它是请求 body 的正式字段，
不是仅供网关内部使用的 header 别名。

### 3.2 官方 session headers

[`codex-rs/codex-api/src/requests/headers.rs#L5-L12`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/codex-api/src/requests/headers.rs#L5-L12)
发送：

```text
session-id
thread-id
```

本次把与默认 `prompt_cache_key` 同源的 `session-id` 纳入缓存、粘性调度和用量关联，
并保留独立的 `thread-id` / `x-client-request-id`。
公开 Prompt Caching 指南把 body `prompt_cache_key` 定义为缓存路由参数；不能把
`session-id` header 单独视为它的替代品。

### 3.3 compact 复用普通请求的 key

[`codex-rs/core/tests/suite/compact_remote.rs#L851-L866`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/tests/suite/compact_remote.rs#L851-L866)
明确断言：

- compact body 中存在 `prompt_cache_key`；
- compact key 与最后一次普通 `/responses` 请求相同。

同文件
[`#L901-L911`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/tests/suite/compact_remote.rs#L901-L911)
覆盖 ChatGPT auth 场景。

### 3.4 前缀随会话只追加、不重排

[`codex-rs/core/tests/suite/prompt_caching.rs#L496-L507`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/tests/suite/prompt_caching.rs#L496-L507)
和
[`#L778-L790`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/core/tests/suite/prompt_caching.rs#L778-L790)
验证：

- 每轮 override 不改变 `prompt_cache_key`；
- instructions、tools 和已有历史保持相同前缀；
- 新一轮 user message 追加到已有历史末尾。

## 4. 生产基线与口径

### 4.1 统计口径

sub2api 的 `usage_logs.input_tokens` 已扣除缓存读写 tokens，因此单次请求的完整输入为：

```text
prompt_tokens =
    input_tokens
  + cache_creation_tokens
  + cache_read_tokens
```

只对 `prompt_tokens >= 1024` 的请求统计缓存命中：

```text
eligible request hit rate =
  eligible requests with cache_read_tokens > 0 / eligible requests

weighted token hit rate =
  SUM(cache_read_tokens) / SUM(prompt_tokens)
```

token 加权值比“有无命中”的请求数比例更有意义：一次请求可能命中大段固定前缀，同时
仍有本轮新增 tokens 未命中。

### 4.2 最近 48 小时结果

查询时间为 2026-07-27（Asia/Taipei），范围为 OpenAI OAuth 账号：

| 实例 / 入站端点 | 请求数 | 可缓存请求 | 可缓存请求中有命中 | token 加权命中率 |
|---|---:|---:|---:|---:|
| `taihezhisuan.cc` `/v1/responses` | 8,870 | 8,718 | 8,680 | 96.36% |
| `taihezhisuan.cc` `/v1/chat/completions` | 820 | 5 | 3 | 73.15% |
| `taihezhisuan.cc` `/v1/responses/compact` | 1 | 0 | 0 | 不适用 |
| `sub2api.xiaofengai.cc` `/v1/responses` | 46 | 46 | 44 | 94.13% |

解释：

- `taihezhisuan.cc` 原生 Responses 的 eligible request hit rate 为
  `8,680 / 8,718 = 99.56%`，已经很健康；
- Chat Completions 虽有 820 次调用，但只有 5 次达到 1024 tokens，不能用 820
  作为缓存命中率分母；
- `sub2api.xiaofengai.cc` 只有 46 个 eligible 样本，94.13% 不代表已经达到稳定的
  长期均值；
- 冷会话的首个可缓存请求、改变 tools/instructions 的请求，仍可能合理 miss。

### 4.3 账号切换不是根因

`taihe-sub2api` 在 2026-07-27 01:07:48 的 Priority Saturation 指标：

```text
select_total=2048
sticky_session_hit_total=1872
load_balance_select_total=176
account_switch_total=0
```

没有发生永久账号切换。会话亲和仍有助于上游状态复用，但当前不能把缓存 miss 归因于
调度器跨账号漂移。

### 4.4 `usage_logs.session_id` 的诊断价值

修复部署前，上述 OpenAI OAuth 行的 `usage_logs.session_id` 全部为 `NULL`。原因不是
客户端没有会话，而是官方 Codex 使用 `session-id`，旧代码只持久化 `session_id`。

该字段不直接决定 OpenAI 的缓存命中，但修复后可用于：

- 按真实客户端会话关联用量；
- 区分冷会话与同会话 miss；
- 对照 scheduler sticky hit、账号和 `cached_tokens` 排查异常。

历史行不会回填；只能从修复部署后的新请求开始观察。

## 5. 已实现的修复

### 5.1 Messages 兼容桥保留 body key

涉及：

- `backend/internal/service/openai_gateway_messages.go`
- `backend/internal/service/openai_codex_transform.go`
- `backend/internal/service/openai_messages_bridge.go`

行为：

- ChatGPT OAuth 不再删除转换后 body 中的 `prompt_cache_key`；
- 若兼容转换生成了稳定 key，则同时放入 Responses body；
- 上游 session header 继续使用 API Key 隔离后的确定性值；
- Grok 独立缓存协议不受此修改影响。

这使 `/v1/messages -> ChatGPT Codex Responses` 同时具备官方 Codex 的 body key 和
session header，而不是 header-only。

### 5.2 compact 保留相同 key

涉及：

- `backend/internal/service/openai_gateway_request_body.go`

`normalizeOpenAICompactRequestBody` 的允许字段新增 `prompt_cache_key`，继续删除
`store`、`stream` 等不属于 compact schema 的字段。

### 5.3 识别官方 `session-id`

涉及：

- `backend/internal/service/openai_gateway_scheduling.go`
- `backend/internal/service/session_id.go`
- `backend/internal/service/openai_gateway_forward.go`
- `backend/internal/service/openai_gateway_passthrough.go`
- `backend/internal/service/openai_ws_forwarder_logutil.go`
- `backend/internal/service/openai_ws_forwarder_payload.go`

解析优先级为：

```text
session-id
session_id
conversation_id
其他已支持的兼容客户端会话头
body prompt_cache_key
内容 fallback（只用于已有兼容路径）
```

它统一用于：

- HTTP 与 WebSocket sticky scheduling；
- 普通转发和 passthrough；
- compact session 解析；
- usage log 会话关联。

### 5.4 原生 Codex 翻译器使用最小 JSON patch

涉及：

- `backend/internal/service/openai_codex_native_transform.go`
- `backend/internal/service/openai_gateway_forward.go`
- `backend/internal/config/config.go`

优化器的实现和适用条件集中在 `openai_codex_native_transform.go`；主转发链路只保留
一个可选钩子，便于后续同步上游。身份协议的实现也在该文件中，但入口独立于优化器，
不受此开关控制。raw JSON patch 功能默认开启：

```yaml
gateway:
  openai_codex_prompt_cache_optimization_enabled: true
```

也可通过管理端“网关设置 → Fork 专属功能”即时开关。管理端保存的数据库值优先于
YAML/环境变量；尚未保存数据库值时，可通过
`GATEWAY_OPENAI_CODEX_PROMPT_CACHE_OPTIMIZATION_ENABLED=false` 显式关闭。关闭时继续
使用上游默认的 `applyCodexOAuthTransform` 完整翻译路径，不启用局部 JSON patch；
session/thread/parent/window identity 仍按默认协议路径解析、隔离和转发。

只有同时满足以下条件的请求进入该路径：

- `openai_codex_prompt_cache_optimization_enabled=true`；
- ChatGPT OAuth 账号；
- 入站 header 能识别为官方 Codex 客户端；
- 原生 `/v1/responses`，不是 Messages bridge 或 compact；
- body 已经是合法的 Codex Responses schema。

该路径使用 `gjson/sjson` 局部修改 JSON，只处理 ChatGPT internal 必需的字段：

- `store=false`、`stream=true`；
- 删除 internal endpoint 明确不支持的顶层参数；
- 补齐 `reasoning.encrypted_content` include；
- 删除 `store=false` 下会触发 404 的 reasoning `rs_*` replay ID，并保留
  `encrypted_content`；
- 对少量必需的 call ID 兼容规则做确定性修正；
- 同步缓存身份字段。

已经合规的 `instructions`、`tools`、`input` item、`text` 及其数组顺序保持原始
raw JSON；需要 system/tool role、Chat Completions tool schema 等兼容转换的请求自动
回退到原有完整翻译器。

### 5.5 默认生效的 body、header 与 metadata 身份一致化

所有 ChatGPT OAuth Codex 请求都会在可选翻译器之前建立身份。session 值先混入下游
API Key ID 做确定性隔离，再按协议形态写入：

```text
body prompt_cache_key
session-id
session_id
已有 client_metadata.session_id
```

其中 `session-id` 对齐当前官方 Codex，`session_id` 保留已有 ChatGPT 兼容性。原生
优化器命中时可以创建缺失的 `client_metadata`；默认完整翻译路径只改写请求中已有的
metadata，不为不相关客户端强行添加整块 metadata。

thread identity 使用独立的确定性映射，并同步写入：

```text
thread-id
x-client-request-id
client_metadata.thread_id
x-codex-parent-thread-id
client_metadata.x-codex-parent-thread-id
x-codex-window-id / client_metadata.x-codex-window-id
x-codex-turn-metadata 中的对应字段
```

因此 root 与 child thread 共享 session `prompt_cache_key`，但各自保留不同 thread
identity，并保留 child 指向 root 的 parent 关系，和官方 Codex 的多代理语义一致。
映射按下游 API Key + 原始 ID 稳定生成，不会随请求或上游账号切换。HTTP、compact、
OAuth passthrough、普通 Responses WebSocket 和 WebSocket passthrough 使用相同协议；
同一 WebSocket 的后续 `response.create` 帧即使省略身份字段，也会继承首帧的
session/thread/parent/window identity。

首次部署该身份修复时，旧 raw body key 与新隔离 key 可能不同，会产生一次预期的冷
缓存；同一会话后续请求会稳定复用新 key。之后单独开关 raw JSON patch 优化器不会再
改变已建立的隔离身份。

### 5.6 Codex 内部接口兼容

除 Responses 与 compact 外，当前 Codex 会使用以下接口：

- `POST /v1/realtime/calls` 与 `GET /v1/realtime?call_id=...`；
- `POST /v1/live` 与 `GET /v1/live/:call_id`；
- `POST /backend-api/codex/images/generations`、`images/edits`；
- `POST /backend-api/codex/memories/trace_summarize`。

sub2api-pro 已补齐这些入口。ChatGPT internal JSON 接口只调度 OpenAI OAuth 账号，
沿用用户/账号并发、billing、审计、换号和用量记录。

这些没有顶层 model 的固定协议端点不能依赖模型推断平台。OpenAI 分组直接允许；
Composite 分组显式写入 OpenAI target platform；纯非 OpenAI 分组拒绝；若 Composite
中间件已经根据请求模型解析成其他平台，也拒绝继续进入 Codex internal handler。
因此账号选择、`QuotaPlatform()`、billing 与 usage log 使用同一个 OpenAI 平台结论。

Realtime 同时支持新版 V1 query `call_id` 和 Frameless path `call_id`。创建响应的
`Location` 保留 call ID 路径段，供 Codex 客户端解析；真实 V1 Sideband 仍连接
`/v1/realtime?call_id=...`。

当前官方 Codex 的 `prepare_realtime_start` / `realtime_request_headers` 为创建请求
和 Sideband 兼容链路设置 `OpenAI-Alpha`、`x-session-id`、`session-id`、
`thread-id` 与 `originator`，没有发送 `x-codex-installation-id`。本实现按该实际
调用链对齐，不额外伪造 installation ID；后续若官方协议变化，应以对应版本源码和抓包
重新验证，而不是从其他 Responses 请求头推断。

### 5.7 Codex 响应头与限额协议

请求白名单包含当前 Codex 使用的 parent thread、attestation 和 timing metrics headers。
响应默认透传动态 `x-codex-*`，并保留模型、reasoning、safety buffering 和授权错误等
协议头，包括：

```text
x-models-etag
openai-model / x-openai-model
x-reasoning-included
x-openai-authorization-error
```

限额快照优先解析新版绝对 Unix 时间
`x-codex-primary-reset-at` / `x-codex-secondary-reset-at`，转换成内部调度所需的剩余
秒数；旧版 `*-reset-after-seconds` 继续作为 fallback。

Codex models manifest 额外携带实际上游 URL path 与完整响应头到 handler。本地 ETag
命中生成的 304 也保留这两项元数据，因此端点记录不会退回客户端别名，限额快照也不会
因短缓存命中而漏更新。自定义 API Key 上游的 header override 在 Codex 默认
`Originator`、`Version`、`User-Agent` 写入后最后应用。

动态 `x-codex-*` 前缀默认透传，但配置中的 `response_headers.force_remove` 先于前缀
放行判断，仍具有最终否决权。这样既能兼容上游新增限额头，也不会绕过管理员明确的安全
删除规则。

## 6. 未采用的高风险“优化”

### 6.1 不生成全局共享 key

把同模型、同 system prompt 的所有用户映射到一个 key，可能快速超过官方建议的约
15 RPM，并把多个不同 prefix 挤到同一路由桶。即使命中率短期看似提高，也会制造
不稳定热点。

### 6.2 不用每轮 request ID 作为 key

request/message ID 每轮变化，会让相同会话无法复用缓存。只有会话级稳定标识适合作为
key。

### 6.3 不为了命中率改变动态请求语义

不能删除用户 tools、重排 instructions、复用错误的 `previous_response_id`，或把两个
逻辑会话强行合并。缓存优化必须保持请求语义和用户隔离。

### 6.4 不把内容 fallback 宣称为可靠会话 ID

对没有任何稳定会话标识的兼容客户端，现有内容摘要只能尽力复用相同前缀：

- 相同首轮内容可能来自不同用户；
- 会话中途改变 system/tools 会产生新前缀；
- 多实例进程内 digest binding 不能替代客户端稳定 session ID。

这类客户端要获得可靠命中，应显式发送稳定 `prompt_cache_key` 或 `session-id`。网关
不能在不知道真实会话边界时安全地猜出唯一会话。

## 7. 验证

本次代码验证：

```bash
cd backend
go test -p 2 -parallel 2 ./internal/service -run '<cache/session focused tests>' -count=1
go test -p 2 -parallel 2 -tags=unit ./internal/service -run '<session persistence tests>' -count=1
go test -p 2 -parallel 2 ./internal/service -count=1
go test -p 2 -parallel 2 ./... -count=1
```

覆盖点包括：

- Messages bridge OAuth body key 保留；
- 自动生成的 metadata/digest key 跨轮稳定；
- compact key 保留；
- 官方 Codex 原生请求的 `instructions/tools/input/text` raw JSON 不被翻译器重写；
- 默认配置继续走原有完整翻译器，同时仍转发默认身份协议；
- 显式开启配置后才进入原生局部 patch 路径；
- 下一轮只追加 input 时，已有 input item 前缀保持不变；
- reasoning replay 的必要兼容清理仍生效；
- body/session/thread/parent/window/turn metadata 使用一致的隔离映射；
- `session-id` 优先于旧别名；
- HTTP、passthrough、WebSocket 上游同时得到两个隔离后的 session header；
- WebSocket 后续帧继承首帧身份，root/child thread 保留 parent 关系；
- V1 Realtime、Frameless Live、images 和 memories 接口路由；
- Composite 固定端点只解析到 OpenAI，且 quota/billing 平台一致；
- models 实际端点、响应头、缓存 304 元数据和最终 header override；
- Realtime 官方兼容头不包含伪造的 installation ID；
- 新旧 Codex 限额头及动态响应头透传；
- `force_remove` 可否决动态 `x-codex-*`；
- usage log 提取官方 header；
- header override 拒绝 `session-id`；
- Grok 和 WebSocket retry 的既有缓存键行为不回归。

## 8. 部署后观察

至少按端点和模型观察一个完整业务周期，并把统计限制在
`prompt_tokens >= 1024`：

1. eligible request hit rate；
2. weighted token hit rate；
3. `cached_tokens` 与 GPT-5.6+ `cache_write_tokens`；
4. 新增 `usage_logs.session_id IS NOT NULL` 的比例；
5. scheduler `sticky_session_hit_total`、`account_switch_total`；
6. `/v1/messages` 和 `/v1/responses/compact` 修复前后的分段变化。

原生 `/v1/responses` 已有 94%–96% 的 token 加权命中率，部署验收应先排除 key
切换造成的一次性冷启动，再比较同一 session 的后续轮次。预期增益来自稳定前缀不再
经过完整翻译器重建，以及 body/header/metadata 被路由到同一缓存身份。
