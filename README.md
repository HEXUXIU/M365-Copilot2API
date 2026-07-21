# M365 Copilot2API

<p align="center">
  <img src="https://img.shields.io/github/license/HEXUXIU/M365-Copilot2API" alt="License">
  <img src="https://img.shields.io/github/last-commit/HEXUXIU/M365-Copilot2API" alt="Last Commit">
  <img src="https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/API-OpenAI%20Compatible-412991?logo=openai" alt="OpenAI Compatible">
  <img src="https://img.shields.io/badge/API-Anthropic%20Compatible-FF6B6B?logo=anthropic" alt="Anthropic Compatible">
  <img src="https://img.shields.io/badge/MCP-Protocol-FF6B35?logo=internetcomputer" alt="MCP Protocol">
  <br>
  <img src="https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fapi.github.com%2Frepos%2FHEXUXIU%2FM365-Copilot2API&query=%24.stargazers_count&label=Stars&style=flat&color=yellow" alt="Stars">
  <img src="https://img.shields.io/badge/PRs-Welcome-brightgreen" alt="PRs Welcome">
</p>

<p align="center">
  <strong>M365 Copilot ChatHub 网关</strong><br>
  将 Microsoft 365 Copilot 转换为 OpenAI / Anthropic 兼容 API
</p>

---

## 📋 功能特性

| 功能 | 状态 |
|------|------|
| ✅ OpenAI 兼容 `/v1/chat/completions` | 支持流式、工具调用 |
| ✅ OpenAI Responses `/v1/responses` | 兼容 |
| ✅ Anthropic 兼容 `/v1/messages` | 兼容 |
| ✅ 流式输出 (SSE) | 实时逐字输出 |
| ✅ 推理模型 | gpt-5.5-reasoning、gpt-5.6-sol 等 |
| ✅ 联网搜索 | claude-sonnet 内置 web_fetch |
| ✅ MCP 协议 | `/v1/mcp/sse` + `/v1/mcp/message` + `/v1/mcp/tools` |
| ✅ 视觉识别 | 支持 base64 图片输入 |
| ✅ 多账号管理 | PKCE OAuth 授权 |
| ✅ API Key 管理 | 控制台创建/撤销 |
| ✅ 代理池 | 轮换、失败冷却 |
| ✅ Web 管理控制台 | 账号、设置、日志 |

## 🚀 快速开始

### 源码编译

```bash
git clone https://github.com/HEXUXIU/M365-Copilot2API.git
cd M365-Copilot2API
# 配置管理员密码（可选，默认 admin123）
export M365_ADMIN_PASSWORD=your_password
go run ./cmd/server
```

默认地址 `http://127.0.0.1:4141`，打开浏览器完成管理员设置和登录。

### Docker 部署

```bash
docker compose build
docker compose up -d
```

## 🔑 使用示例

```bash
# 基础聊天
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5",
    "messages": [{"role": "user", "content": "你好"}]
  }'

# 流式输出
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5-reasoning",
    "messages": [{"role": "user", "content": "1+1=?"}],
    "stream": true
  }'

# 联网搜索（claude-sonnet 内置）
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet",
    "messages": [{"role": "user", "content": "北京今天天气？"}]
  }'
```

## 🤖 可用模型

| 模型 | 推荐用途 | 速度 |
|------|---------|------|
| `gpt-5.5` | 日常对话 | ⚡ ~5s |
| `gpt-5.2` | 最轻量快速 | ⚡ ~5s |
| `claude-sonnet` | 联网搜索、实时信息 | ⚡ ~5-6s |
| `gpt-5.6-sol` | 复杂推理（默认高推理） | ⏳ ~7s |
| `gpt-5.5-reasoning` | 数学/逻辑推理 | ⏳ ~9s |

## 🧩 MCP 协议

网关内置 MCP (Model Context Protocol) 服务器：

| 端点 | 说明 |
|------|------|
| `GET /v1/mcp/sse` | SSE 连接 |
| `POST /v1/mcp/message` | JSON-RPC 消息 |
| `GET /v1/mcp/tools` | 工具列表 |
| `tools/list` | 发现工具 |
| `tools/call` | 调用工具 |

## 🛠 技术栈

- **语言**: Go 1.22+
- **协议**: SignalR / WebSocket / SSE / MCP
- **前端**: 单页 HTML + Inter 字体 + Lucide 图标
- **部署**: Docker / 裸机

## 📄 配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_LISTEN` | `127.0.0.1:4141` | 监听地址 |
| `M365_ADMIN_PASSWORD` | `admin123` | 管理员密码 |
| `M365_CHAT_TIMEOUT_SECONDS` | `120` | 聊天超时 |
| `M365_CONTEXT_WINDOW` | `128000` | 上下文窗口 |
| `M365_MAX_OUTPUT_TOKENS` | `16384` | 最大输出 Token |

## 🔒 安全

- 默认绑定 localhost
- 首次登录后立即修改管理员密码
- API Key 仅在创建时显示完整密钥
- 建议使用 TLS 和反向代理对外暴露

## 📝 许可证

MIT License. 详见 [LICENSE](LICENSE)。