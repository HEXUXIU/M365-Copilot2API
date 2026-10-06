# HAR 逆向挖掘报告（脱敏版）

本目录收录 9 份逆向报告。报告 01–08 基于生产流量抓包（HAR）的 M365 Copilot Web 逆向分析，
数据源为 2026-08-05 ~ 2026-08-23 录制的 6 个 HAR 文件（约 2225 个 entry、903 帧 Chathub
WebSocket 消息），覆盖正常聊天、临时会话、个性化设置、图片生成与限额触发等场景；报告 09 不基于
HAR，而是在生产网关上用三个真实 agent（opencode / codex / claude-code）实测得出，记录工具调用
两种模式的可用性差异。

所有报告已完成脱敏：账号邮箱、OID/TID、会话与对话 ID、access_token/JWT、fileToken、设备指纹等
一律替换为占位符（对照表见文末）。端点 URL、参数名、JSON 结构、时序数据、帧类型与技术结论均保留原貌。

## 报告索引

| # | 文件 | 一句话摘要 | 主要关联代码 |
|---|---|---|---|
| 01 | [01-ws-protocol-payload.md](01-ws-protocol-payload.md) | Chathub WS 升级参数全表 + BuildWSURL JS 还原 + chatPayload 字段级 diff，13 条 P0-P2 改动建议 | internal/chathub/client.go |
| 02 | [02-hidden-endpoints.md](02-hidden-endpoints.md) | substrate/graph/designer 三域全业务端点枚举（P0-P2 共 30 项价值分级），含图片生成完整调用链 | client.go、internal/web/images.go |
| 03 | [03-auth-trouter.md](03-auth-trouter.md) | OAuth broker/FOCI/token 刷新实测规律 + Trouter v4 协议，auth 模块十项改进清单 | internal/auth/*、pkce_auth_gateway.py |
| 04 | [04-memory-personalization.md](04-memory-personalization.md) | 临时会话唯一开关 disableMemory 的实证 + 个性化三层开关模型 + 记忆管理 API 方案 | client.go（disableMemory）、internal/web/server.go |
| 05 | [05-streaming-frames.md](05-streaming-frames.md) | 903 帧流式响应解剖：snapshot/delta 冗余双通道语义 + 引用挂载位置 + G1-G9 遗漏帧清单 | client.go（emitSnapshot/finalizeText）、events.go |
| 06 | [06-timing-performance.md](06-timing-performance.md) | 13 次聊天分阶段延迟基线 + connpool 死代码 bug 发现 + 六项性能优化 | internal/chathub/connpool.go |
| 07 | [07-errors-risk.md](07-errors-risk.md) | 风控三级分层结论（metering 软拒 → 能力终态码 → 硬封）+ 错误码全景 + 账号健康预警指标 | internal/web/errors.go、agent_ledger.go |
| 08 | [08-telemetry-fingerprint.md](08-telemetry-fingerprint.md) | 遥测三路流/指纹链/variants 动态派生 + Go 客户端拟真度评分卡 + 最小拟真改动清单 | client.go（variants）、plugins.go |
| 09 | [09-tool-planning-modes.md](09-tool-planning-modes.md) | 工具调用 router/native 两模式实测对比：router 可用、native 因上游不接受客户端插件而不可用 | tool_planning.go、tools.go |

## 建议阅读顺序

按依赖关系推荐线性顺序 **01 → 05 → 04 → 03 → 02 → 06 → 07 → 08**：
先懂"怎么连、发什么"（01），再懂"怎么读响应"（05），然后是会话模型（04）、登录态维持（03）、
周边端点全景（02）、性能（06）、风控（07）、最后是拟真对抗（08，依赖前述全部结论）。

按角色的捷径：

- 只关心聊天主链路跑通：01 → 05
- 认证 / 账号池运维：03 → 07
- 拟真与反检测：08 → 07 → 01 §2
- 功能扩展点（生图 / 记忆 / 上传）：02 → 04

## 与代码库的关联

- `internal/chathub/client.go` — WS 主链路核心：BuildWSURLWithOptions / chatPayload / Metrics /
  emitSnapshot / finalizeText，是 01/04/05/07/08 五份报告的直接对照物
- `internal/chathub/connpool.go` — 连接池；06 号报告发现其 returnConn 死代码 bug 并给出修复方案
- `internal/chathub/events.go`、`images.go` — 流内事件提取与生图 URL 抓取（05）
- `internal/auth/config.go`、`cache.go`、`token.go` — OAuth scope 选择、token 缓存与刷新（03）
- `pkce_auth_gateway.py`（仓库根目录）— PKCE 登录网关（03 §5、07 §5.2）
- `internal/web/errors.go`、`agent_ledger.go` — failover 与账号账本，07 号报告的 ParseMetering 落点
- `internal/web/plugins.go`、`m365cloud.go`、`server.go` — UA 与客户端指纹硬编码位置（08）

## 脱敏占位符对照

| 占位符 | 含义 |
|---|---|
| `<USER_A_OID>` / `<USER_B_OID>` | 两个抓包账号的用户 OID |
| `<TENANT_ID>` | 两账号共享的租户 ID |
| `<SESSION_ID_A>` / `<SESSION_ID_B>` | 页面级 X-SessionId（A/B 账号各一）；`_HEX` 后缀为其去连字符形态 |
| `<CONVERSATION_ID_A1>` / `<CONVERSATION_ID_A2>` / `<CONVERSATION_ID_B>` | 客户端自造并复用的 ConversationId |
| `<REQ_ID_1>` ~ `<REQ_ID_5>`、`<REQ_ID_RECONNECT>` | 请求级 chatsessionid（四值一体）；RECONNECT 为断线重连时复用的那个 |
| `<REDACTED_TOKEN>` | access_token/JWT/fileToken 等凭据长串（长度等元信息随文保留） |
| `<MAILBOX_ITEM_ID>` | CustomInstructions 的 Exchange 邮箱 ItemId 主键 |
| `<TOKEN_PREFIX>` | Designer fileToken 解码结构中的 TokenPrefix GUID |
| 其余 `<...>` | 各报告中一次性出现的消息 ID、轮询 ID、设备指纹、哈希等 |

说明：文中保留未脱敏的 GUID 仅为微软公开的第一方应用 ID（如 M365 Copilot Web client id
`c0ab8ce9-…`、M365 门户 SPA `4765445b-…`、Graph 资源 `00000003-0000-c000-…`），属公开常量，
不构成任何账号或租户的身份信息。
