# 09 工具调用两种模式：router 可用、native 不成立

本报告记录 `toolPlanningMode` 两个取值的实测差异。结论基于 2026-10-03 ~ 10-06 用三个真实
agent（opencode / codex / claude-code）在生产网关上的端到端压测，不是代码推断。

## 结论

| 模式 | 工具调用 | 工具轮次旁白 | 结论 |
|---|---|---|---|
| `router`（默认） | 可用 | 无 | **保持默认** |
| `native` | 不生效 | 有 | 本部署下不可用 |

## 实测数据

`native` 模式下，三个 agent 的工具调用**全部为空**，模型明确回复工具不可用：

```json
{"type":"event","t":11914,"event":{"type":"message.part.updated",
  "properties":{"part":{"type":"text",
    "text":"I can\u2019t call `get_weather` because that tool isn\u2019t available in this chat"}}}}
```

三个 agent 的会话目录里都没有产生目标文件，`trace.jsonl` 里 `tool_use` 事件数为 0。

## 关键证据

**一、M365 不接受客户端内联声明的 API 插件。**

网关把客户端工具作为 plugins 下发（`internal/chathub/tools.go:clientPlugins`）。做了三组实验，全部返回同一个"工具不可用"：

| 实验 | 变量 | 结果 |
|---|---|---|
| 基线 | `{"Id":name,"Source":"API",...}` | 工具不可用 |
| 关 MCP 插件 | 只保留内联 API 插件，去掉 `mcp-gateway` | 工具不可用 |
| 补 HAR 观察字段 | 加 `IsThirdPartyPluginSource` / `IsGraphConnectorPluginType` | 工具不可用 |

排除的是"格式不对"和"MCP 插件挡住了内联插件"，指向的是上游根本不接受客户端自定义插件。

**二、HAR 里没有任何客户端自定义工具被调用的样例。**

`02-hidden-endpoints.md` 里的 `TriggerPlugin` / `HintInvocation` / `ResumePluginAuth`
等帧类型，来自 `allowedMessageTypes` 枚举——那是"允许出现"的清单，不是实际抓到的帧。
对 6 个 HAR 全文搜索 `TriggerPlugin`，**0 命中**。

唯一被实证的插件是内置的：

```json
"plugins":[{"id":"BingWebSearch","source":"BuiltIn",
            "isThirdPartyPluginSource":false,"isGraphConnectorPluginType":false}]
```

注意这是 **BuiltIn**。`Source:"API"` 的客户端插件在 HAR 中从未出现。

**三、`clientPlugins` 的 `Source:"API"` 分支缺少协议依据。**

`docs/har-mining/01-ws-protocol-payload.md` §5.6 (F13) 记录的插件默认值只有
`BingWebSearch`。API 源插件的字段形态、回调方式、结果回填格式，在现有 HAR 证据里
都是空白。

## 为什么 `router` 能工作

`router` 不依赖上游发出工具调用。网关自己跑一次路由模型调用，从文本里解析出工具决策，
再把 `tool_calls` 直接返回给客户端，由客户端执行。闭环完全在本地，所以稳定可用。

代价是那次路由调用只输出工具决策、不含文本——这就是 issue #102 反馈的"工具轮次
不说话"的来源。

### 提示词能改善吗

部分能，但不能根治。在 router 模式下，工具协议提示词里加"每次工具调用前先说一句"
后，模型确实开始输出意图：

```
codex:    "I'll create `note.txt` in the current workspace, then list the directory to verify it."
claude-code: "我会在当前工作目录创建 `note.txt`，随后运行 `dir` 检查文件是否存在。"
```

但这段旁白此前会被围栏缓冲逻辑吞掉或错序——那是 issue #102 的真凶，已由
`a661371` / `6215351` 修复。所以"看起来不说话"是提示词 + 围栏缓冲两个问题叠加，
修掉后者之后前者的效果才显现。

## 给社区的提示

如果你的部署里 `native` 模式不生效（工具始终不被调用、模型回复"工具不可用"），
请切回 `router`：

```json
{ "toolPlanningMode": "router" }
```

或 `POST /api/admin/settings` 修改。该字段可热更新，无需重启。

要让 native 真正可用，需要先补齐上游协议证据：

1. 一个能成功调用自定义工具的真实客户端 HAR（能拿到 `Source:"API"` 插件的完整生命周期：
   声明 → 调用 → 参数增量 → 结果回填）；
2. 或把网关的 MCP 端点暴露到 M365 云端可达的地址，让上游能真正回调
   （当前网关下发的是本机 `http://<host>/v1/mcp/sse`，M365 服务器访问不到）。

在这两点之一成立之前，`native` 只是一个未经验证的假设路径。

## 关联

- `internal/web/tool_planning.go` — 模式选择
- `internal/web/server.go` — router 分支（`planningMode == "router" && body.Stream`）、
  native 分支（`buildAnswerRequest` 中透传 `req.Tools`）
- `internal/chathub/tools.go` — `clientPlugins`，plugins 下发
- `internal/chathub/tool_protocol.go` — 围栏协议提示词
- `internal/web/fence_stream.go` — 围栏缓冲与顺序（issue #102 修复）

## 复现方式

`tests/agents/harness/` 下是真 agent 驱动：

```powershell
$env:M365_API_KEY = '<key>'

# 三 agent 并发跑同一提示
.\invoke.ps1 -Agent opencode,codex,claude-code -Prompt 'Create note.txt, run dir, then tell me what you did.'

# 切到 native 再跑一次，对比 tool_use 事件数
.\invoke.ps1 -Agent claude-code -Prompt 'Create note.txt and run dir'
```

每个会话目录下的 `trace.jsonl` 记录全部事件与毫秒时间戳，`api-key.txt` 不入库
（见 `.gitignore`）。