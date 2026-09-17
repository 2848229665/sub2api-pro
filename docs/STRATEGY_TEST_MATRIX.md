# 防降智策略测试矩阵

本文件记录策略注册表的目的和测试顺序。策略用于受控对比，不能证明上游模型质量，也不会改变账号凭据或上游权限。

2026-09-14 本地修改：新建及重新开启保护默认选择 `legacy`（初代兼容），适用于独立 OpenAI/Anthropic OAuth 或 Setup Token 且非随机代理的账号。其他类型保持通用并发保护；已有开启策略保留。前端默认入口与 API 未指定模式时也选择 legacy。该默认已进入本 Fork 的账号保护实现。

## 策略

| ID | 身份模式 | TLS | 并发 | 用途 |
| --- | --- | --- | ---: | --- |
| `native_baseline` | off | account | 当前值 | 原生基线；通过明确还原操作启用 |
| `minimal_compat` | device | standard | 8 | 仅固定设备身份 |
| `session_standard` | session | standard | 8 | 对照会话收敛 |
| `legacy` | OpenAI 为 session；Anthropic 保留原身份 | nodejs24 | 16 | 默认，sub2 初代策略 |
| `mode1` | device | standard | 16 | 兼容架构 v3 |
| `mode2` | full | nodejs22 | 8 | 强收敛诊断 |
| `tls_node24` | device | nodejs24 | 8 | 单独对照 TLS |
| `low_concurrency` | session | standard | 4 | 排查限流和连接复用 |

所有身份策略都必须同步改写请求体 `client_metadata`，禁止只改 Header。`Authorization`、`chatgpt-account-id` 和账号凭据不属于可调实验变量。

## 推荐顺序

`native_baseline → minimal_compat → session_standard → legacy → mode1 → tls_node24 → low_concurrency → mode2`

测试阶段应固定账号、模型、输入、代理和测试并发，关闭自动换号和自动重试，并保存原始响应、错误分类、耗时和策略快照。

## 结果解释

- 基线即失败：优先检查账号、模型、额度、OAuth 和代理。
- 初代成功而 mode1 失败：升级版策略存在兼容差异。
- TLS 对照改善：优先检查传输层，而不是继续收敛身份。
- 低并发改善：优先检查限流、连接复用和账号调度。
- 所有策略均成功但输出质量异常：不能直接归因于防降智，需检查模型和上游能力。
