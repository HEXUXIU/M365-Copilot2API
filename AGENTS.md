# m365-copilot2api

## 架构
- **单 WebSocket** (无需 negotiate): `wss://substrate.office.com/m365Copilot/Chathub/{oid}@{tenant}?access_token=...`
- negotiate 端点返回 403 (即使 token 有效，需浏览器 CORS 上下文)
- TRouter (`trouter.teams.microsoft.com`) 是 Teams IC3 传输层，可选

## 协议 (SignalR)
- **Chat 请求**: `type:4` (StreamInvocation), `target:"chat"` (小写)
- **关键字段**: `streamingMode:"ConciseWithPadding"` (字符串), `spokenTextMode:"None"`
- `author` 是字符串 `"user"`, 不是 `{"role": "user"}`
- `gptIdOverride`: `{"id": "Gpt_5_2_Chat", "source": "MOS3"}`
- **流式响应**: `type:1 target:"update"` → `writeAtCursor` (增量) + `messages` (快照)
- **结束**: `type:2` StreamItem + `type:3 Completion`
- 所有消息用 `\x1e` (Record Separator) 分隔

## 认证
- **PCA 客户端**: `4765445b-32c6-49b0-83e6-1d93765276ca` (SPA type)
- **Token 刷新**: Origin spoofing (`Origin: https://m365.cloud.microsoft`) 绕过 SPA 限制
- **Scope**: `https://substrate.office.com/sydney/.default`
- 首次设置: 运行 `m365-copilot-setup`

## 项目结构
```
m365_copilot/
├── src/m365_copilot/
│   ├── auth.py       TokenManager (刷新/缓存/首次设置)
│   ├── client.py     M365Client (SignalR WS, chat, stream)
│   ├── models.py     模型定义 + 常量
│   ├── payload.py    URL/消息构建 (3种 payload 变体)
│   ├── scripts/      配置向导
│   ├── tools/        工具系统 (provider/detector/builtin)
│   └── servers/      CLI + OpenAI API 入口
├── data/tokens/      RT + token 缓存 (gitignore)
├── .env              环境变量配置 (gitignore)
└── .env.example      环境变量模板
```

## 已确认模型
| 内部 ID | tone / gptIdOverride | 备注 |
|---------|---------------------|------|
| Auto | `tone: "Magic"` | 自动选择 (实际 GPT-5) |
| Quick | `tone: "Chat"` | 快速答复 |
| Reasoning | `tone: "Reasoning"` | 深度思考 |
| Gpt_5_2_Chat | `gptIdOverride: {id:"Gpt_5_2_Chat", source:"MOS3"}` | GPT 5.2 快速 |
| Gpt_5_3_Chat | 同上 | GPT 5.3 快速 |
| Gpt_5_4_Chat | 同上 | GPT 5.4 快速 |
| Gpt_5_5_Chat | 同上 | GPT 5.5 快速 |
| Claude_Sonnet | `gptIdOverride: {id:"Claude_Sonnet", source:"MOS3"}` | ⚠️ **非真 Claude** |

**关键发现**: Claude_Sonnet override 被服务器接受, 但实际路由到 GPT-5。
穿透测试 ("Are you Claude?") 返回 "No. I am M365 Copilot, not Claude."
所有模型均报告 "based on GPT-5 chat model"。
**结论**: M365 Copilot 后端统一使用 GPT-5, Claude 标签仅为前端展示用途。

## 快速开始

### 1. 安装
```bash
pip install -e .
```

### 2. 配置
```bash
m365-copilot-setup
```
或通过 Microsoft Graph API 获取 Tenant ID 和 User OID:
```javascript
// 在浏览器 DevTools Console (F12) 中运行:
fetch('https://graph.microsoft.com/v1.0/me').then(r=>r.json()).then(d=>console.log('OID:', d.id))
fetch('https://graph.microsoft.com/v1.0/organization').then(r=>r.json()).then(d=>console.log('TENANT:', d.value[0].id))
```
然后写入 `.env` 文件:
```
M365_TENANT_ID=<your-tenant-id>
M365_USER_OID=<your-user-oid>
M365_CLIENT_ID=4765445b-32c6-49b0-83e6-1d93765276ca
```

### 3. 使用
```bash
m365-copilot "你好"              # CLI 提问
m365-copilot -i                  # 交互模式
m365-copilot --list-models       # 列出模型
m365-copilot-server --port 8000  # OpenAI API 服务器
```

## API 端点 (m365-copilot-server)
- `POST /v1/chat/completions` — OpenAI Chat Completions
- `POST /v1/completions` — OpenAI Completions (FIM)
- `POST /v1/messages` — Anthropic Messages
- `POST /v1/complete` — Anthropic Complete (FIM)
- `GET /v1/models` — 模型列表

## 环境变量
| 变量 | 必须 | 说明 |
|------|------|------|
| M365_TENANT_ID | ✅ | Azure AD 租户 ID |
| M365_USER_OID | ✅ | 用户 Object ID |
| M365_CLIENT_ID | 可选 | 默认 PCA 客户端 ID |

## 关键发现
- Substrate WSS 不检查 appid, PCA token 直接可用
- 区域封锁仅在 Web UI 层, API 层无限制
- BCBv2Windows 身份被服务器接受
- MCP Native Bridge 有完整工具发现协议
- SignalR WS 在 type:3 Completion 后需要重连 (已修复)

## Bug 修复 (2026-07-03)
1. ToolCallDetector: 改用括号匹配提取嵌套 JSON (detector.py)
2. WS 连接复用: type:3 后标记 dirty 强制重连 (client.py)

## REST API 认证 (Cookie-based)

### 架构
- `m365.cloud.microsoft` 是 ASP.NET Core + React SPA
- 使用 **cookie 认证** (非 Bearer token) 保护 REST API

### PCA Token 限制
- **PCA client (4765445b) 是 SPA type**: token 只能通过浏览器 CORS 使用
- 服务端 REST API 需要 cookies

### Cookie 提取流程
1. 关闭 Edge（Profile 被锁）
2. `m365-copilot --setup-cookies` → Playwright 启动浏览器

### 文件位置
- `src/m365_copilot/cookie_store.py` — Cookie 管理器
- `src/m365_copilot/servers/api.py` — Cookie-based ConversationAPI
