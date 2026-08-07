# M365 Copilot2API

<p align="center">
  <img src="https://img.shields.io/github/license/HEXUXIU/M365-Copilot2API" alt="License">
  <img src="https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/API-OpenAI%20Compatible-412991?logo=openai" alt="OpenAI Compatible">
  <img src="https://img.shields.io/badge/API-Anthropic%20Compatible-FF6B6B?logo=anthropic" alt="Anthropic Compatible">
</p>

<p align="center">
  <strong>Microsoft 365 Copilot → OpenAI / Anthropic 兼容 API 网关</strong>
</p>

M365 Copilot2API 是一个用 Go 编写的自托管网关，把微软 365 Copilot 商业订阅背后的 **ChatHub 私有协议**（WebSocket）翻译成标准的 **OpenAI / Anthropic 兼容 API**。Claude Code、OpenCode、Cursor 以及任何 OpenAI 客户端都可以直接用熟悉的格式调用 M365 Copilot。

工作原理概括：**ChatHub 私有协议 ⇄ OpenAI / Anthropic 兼容 API**。连接握手、心跳保活、事件流解析、工具调用转换全部封装在 `internal/chathub` 层，对外只暴露 `/v1/chat/completions`、`/v1/messages` 等标准端点。

项目自带完整 Web 管理控制台，覆盖账号授权（OAuth/PKCE）、API Key 管理、代理池、云端对话管理、用量统计与模型测试，适合个人自部署、自托管使用。

> ⚠️ **免责声明（请务必阅读）**
>
> - 本项目**不是微软官方产品**，与 Microsoft、OpenAI、Anthropic 及其关联公司**均无任何从属或合作关系**。
> - 使用第三方账号池、代理转发等方式接入 M365 服务**可能违反服务商服务条款**，由此产生的一切后果由使用者自行承担。
> - 请遵守当地法律法规与目标平台的服务条款（ToS）。
> - 本项目**仅供个人学习与研究**，**禁止用于商业转售或规模化运营**。
> - 账号被封禁、数据丢失等任何损失，本项目维护者与贡献者**概不负责**。

## 功能特性

| 功能 | 说明 |
|------|------|
| OpenAI 兼容 `/v1/chat/completions` | 支持流式输出与 function calling |
| OpenAI Responses `/v1/responses` | 兼容 Responses 协议（Codex 等客户端） |
| Anthropic 兼容 `/v1/messages` | Claude Code / Cursor 直连 |
| SSE 流式输出 | 逐字实时返回，`stream: true` |
| 工具调用转换 | OpenAI function calling ⇄ M365 工具协议，`router` / `native` 两种规划模式 |
| 内容键会话复用 | 以对话上下文为键复用云端对话，命中时只发送增量消息（类似 DeepSeek 上下文缓存） |
| 会话显式绑定 | `X-M365-Session-Id` 请求头精确指定要继续的会话 |
| 自动清理 | 按闲置时间（默认 2h）或保留数量回收云端对话 |
| 多账号管理 | PKCE 授权 + 账号轮询 + 故障自动转移 |
| API Key 管理 | 控制台创建 / 撤销 / 回读 |
| 代理池 | HTTP / HTTPS / SOCKS5 代理轮换、健康检查、失败冷却 |
| 用量统计 | 按 key / 账号 / 模型 / 端点聚合（`usage.jsonl`） |
| 缓存命中统计 | 命中率、节省 token 仪表盘 |
| 多模态输入 | 支持图片等附件（base64 / URL） |
| 图像生成 | `/v1/images/generations` |
| Web 控制台 | 账号、密钥、代理池、模型、对话、日志一屏管理 |

## 架构

```
┌──────────────┐    OpenAI / Anthropic    ┌──────────────────┐    ChatHub    ┌──────────────┐
│ Claude Code  │ ───────────────────────► │      网关         │ ────────────► │ M365 Copilot │
│ OpenCode     │   /v1/chat/completions   │ (Go, m365-copilot2api) │  WebSocket    │  (云端对话)   │
│ 任意 OpenAI  │   /v1/messages           │  internal/web     │  internal/    │              │
│ 客户端        │   /v1/responses          │                   │  chathub      │              │
└──────────────┘                          └──────────────────┘               └──────────────┘
```

- **协议层（`internal/chathub`）**：封装 M365 Copilot ChatHub 的 WebSocket 私有协议——连接建立、心跳保活、事件流解析（流式 token、工具调用、多模态输入）。对上层只暴露统一的事件接口。
- **会话解析（`internal/web/session_resolver.go`）**：多账号场景下把每个客户端请求稳定解析到固定账号与云端对话，并实现内容键会话复用（见下文原理）。
- **账号轮询与故障转移**：多账号间轮询均衡流量；账号故障（鉴权失效、连接断开等）自动切换到下一个可用账号重试。

## 快速开始

### 环境要求

- Go 1.23+（`go.mod` 声明的最低版本）
- Windows / Linux 均可；Windows 上推荐用仓库自带的 `manage.py` 管理生命周期

### 源码编译

```powershell
git clone https://github.com/HEXUXIU/M365-Copilot2API.git
cd M365-Copilot2API

# 设置管理员密码（可选，默认 admin123），生产环境务必设置强密码
$env:M365_ADMIN_PASSWORD = "your_strong_password"

go build -o m365-copilot2api.exe ./cmd/server
```

```bash
# Linux / macOS
export M365_ADMIN_PASSWORD=your_strong_password
go build -o m365-copilot2api ./cmd/server
```

### 启动

Windows 上用 `manage.py` 启动（默认后台上运行，日志写入 `server.log` / `server-error.log`）：

```powershell
python manage.py start    # 后台运行，默认监听 0.0.0.0:4141
python manage.py status   # 查看运行状态
python manage.py logs     # 查看最近日志（可加参数 N 指定行数）
python manage.py err      # 查看错误日志
python manage.py stop     # 停止服务
```

> `manage.py` 内部硬编码了仓库绝对路径（`D:\M365-Copilot2API\m365-copilot2api.exe` 等），克隆到其他目录时请先修改脚本顶部的路径常量，并确保先完成编译。

直接运行二进制则默认只监听内网 `http://127.0.0.1:4141`，可通过环境变量 `M365_LISTEN` 覆盖。

### Docker 部署

仓库自带 `Dockerfile` 与 `docker-compose.yml`：

```bash
docker compose up -d --build
```

镜像内以非 root 用户运行，端口映射默认只暴露在 `127.0.0.1`，数据目录挂载在 `./data`，管理员密码可文件注入。

### 初始化与第一次调用

浏览器打开控制台（默认 `http://127.0.0.1:4141`）：

1. 用管理员密码登录（首次登录**强制要求修改密码**）。
2. 在「账号」页发起 **PKCE 授权**，按引导完成 M365 账号登录。
3. 授权成功后，在「API Key」页**创建第一个 API Key**。
4. 用下面的 API 示例验证调用。

> 有多个 M365 账号时可以重复授权，网关会以轮询 + 故障转移的方式自动调度全部账号。

## 配置说明

全部通过环境变量配置，也可以用 `.env.example` 作为起点。应用启动时会优先读取显式设置的环境变量。

### 核心

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_LISTEN` | `127.0.0.1:4141` | 监听地址（`manage.py` 与 Docker 内置为 `0.0.0.0:4141`） |
| `M365_ADMIN_PASSWORD` | `admin123` | 管理员密码（首次登录强制修改） |
| `M365_DATA_DIR` | `~/.config/m365-copilot2api` | 数据目录（token、密钥、用量等集中存储；`manage.py` 内置为 `data/`） |
| `M365_CONFIG` | `~/.config/m365-copilot2api/accounts.json` | 账号配置文件路径 |
| `M365_SESSION_TTL_MINUTES` | `120` | 会话绑定存活时间（分钟），过期从 `sessions.json` 清除 |
| `M365_CONTEXT_TTL_MINUTES` | `120` | 上下文指纹复用窗口（分钟） |
| `M365_CONTEXT_SIMILARITY` | `0.6` | 上下文相似度复用阈值（0~1，Jaccard 相似度） |
| `M365_LOG_LEVEL` | `info` | 日志级别 |

### 自动清理

云端对话被视为「缓存条目」：会话命中时自动刷新存活时间，长期闲置或超出数量上限的对话由后台循环回收。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_AUTO_CLEANUP` | 开启 | 云端对话自动清理开关（设为 `0` / `false` / `no` / `off` 关闭） |
| `M365_AUTO_CLEANUP_INTERVAL_MINUTES` | `30` | 扫描周期（分钟） |
| `M365_AUTO_CLEANUP_MAX_AGE_HOURS` | `2` | 闲置超过即回收（小时） |
| `M365_AUTO_CLEANUP_KEEP_N` | `100` | 最多保留的云端对话数 |

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_CLEANUP_MODE` | `after_response` | 本地对话索引清理模式（`after_response` / `keep_n` / `max_age`） |
| `M365_CLEANUP_KEEP_N` | `5` | `keep_n` 模式的保留量 |
| `M365_CLEANUP_MAX_AGE_HOURS` | `24` | `max_age` 模式的时限 |

### 工具与推理

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_TOOL_PLANNING_MODE` | `router` | 工具规划模式：`router`（网关路由规划）/ `native`（云端原生规划） |
| `M365_MAX_TOOL_CALLS_PER_TURN` | `1` | 单轮最多并行工具调用数（有副作用操作自动降为串行） |
| `M365_MAX_TOOL_ROUNDS` | `16` | 单次请求最大工具轮次 |
| `M365_CONTEXT_WINDOW` | `128000` | 上下文窗口 |
| `M365_MAX_OUTPUT_TOKENS` | `16384` | 最大输出 Token |
| `M365_CHAT_TIMEOUT_SECONDS` | `120` | 聊天超时（秒） |
| `M365_IMAGE_TIMEOUT_SECONDS` | `150` | 图片处理超时（秒） |

### 代理池与认证

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_PROXY_POOL` | 空 | 代理列表（逗号或换行分隔，支持 http / https / socks5） |
| `M365_PROXY_INSECURE_TLS` | — | 信任自签代理证书（`1` / `true`） |
| `M365_PROXY_HEALTH_URL` | 默认探测地址 | 代理健康检查目标 |
| `M365_CLIENT_ID` | 内置 | Azure 应用 Client ID |
| `M365_AUTHORITY` / `M365_REDIRECT_URI` / `M365_SCOPE` | 内置 | OAuth 端点自定义覆盖 |

### 数据文件

| 变量 | 说明 |
|------|------|
| `M365_TOKEN_CACHE` | Token 缓存文件（未设置时落到数据目录） |
| `M365_SESSION_CACHE` | 会话绑定缓存文件（默认 `sessions.json`） |
| `M365_CONVERSATION_CACHE` | 本地对话索引（默认 `conversations.json`） |
| `M365_API_KEYS` | API Key 存储文件 |
| `M365_USAGE_LOG` | 用量统计日志（默认 `{data_dir}/usage.jsonl`） |
| `M365_DEBUG_LOG` | 调试日志文件（请求 / 响应元数据） |

## 使用示例

### 基础聊天（OpenAI 格式）

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

### 流式输出

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "1+1=?"}],
    "stream": true
  }'
```

### 显式指定会话（内容键复用 + 增量发送）

携带同一 `X-M365-Session-Id` 的请求会被绑定到同一条云端对话，命中时网关只把新增历史部分发送给上游：

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -H "X-M365-Session-Id: my-project-session" \
  -d '{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"继续我们刚才的讨论"}]}'
```

### Anthropic 格式（Claude Code / Cursor）

```bash
curl http://127.0.0.1:4141/v1/messages \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6-sol","max_tokens":1024,"messages":[{"role":"user","content":"你好"}]}'
```

## 对接 Claude Code

在 `~/.claude/settings.json` 的 `env` 中指向网关：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4141",
    "ANTHROPIC_MODEL": "gpt-5.6-sol",
    "ANTHROPIC_API_KEY": "m365_你的密钥"
  }
}
```

其他任何支持 OpenAI / Anthropic `base_url` 配置的客户端（OpenCode、Cursor、Codex 等）同理，把 `BASE_URL` 指向网关即可。

> ⚠️ **认证冲突提醒**：如果系统环境变量残留了 `ANTHROPIC_API_KEY`，或同时配置了 `ANTHROPIC_AUTH_TOKEN`，Claude Code 会告警「认证可能不工作」。请二选一：让 `settings.json` 的 `env` 覆盖系统级变量，或删除系统级 `ANTHROPIC_*`。

## 可用模型

网关实际内置 13 个模型（`gatewayModels` + 默认映射，以 `/v1/models` 目录为准；可在控制台「设置」页增删映射、调整默认推理级别）：

| 模型 | 默认推理级别 | 说明 |
|------|-------------|------|
| `gpt-5.6-sol` | `low` | 默认模型（可配置映射） |
| `gpt-5.6-terra` | `medium` | 推理折中（可配置映射） |
| `gpt-5.6-luna` | `medium` | 推理折中（可配置映射） |
| `gpt-5.6-reasoning` | — | 内置模型 |
| `gpt-5.5` / `gpt-5.5-reasoning` | — | 内置模型 |
| `gpt-5.4` / `gpt-5.4-reasoning` | — | 内置模型 |
| `gpt-5.3` | — | 内置模型 |
| `gpt-5.2` / `gpt-5.2-reasoning` | — | 内置模型 |
| `claude-sonnet` / `claude-sonnet-reasoning` | — | 内置模型（Anthropic via M365） |

- 模型映射把公开模型名翻译成上游 tone；控制台可增删映射、调整默认推理级别。
- 推理强度还可通过请求内的 `reasoning_effort` 参数调整。
- 内置模型来自 `internal/web/codex_catalog.go`（`gatewayModels`），可配置映射来自 `internal/web/settings.go`（`defaultModelMappings`）；M365 订阅上线的新模型（如 `codex` 系）以实际目录为准，可在控制台配置导入。

## 内容键会话复用原理

多账号场景下，网关会用「内容键（context key）」把请求复用到已有云端对话上，机制对标 DeepSeek 式上下文缓存：**同一个对话上下文只维护一条云端会话，命中时只把增量新消息发给上游**，不仅省去重建上下文的开销，也更贴近多轮工具的体验。核心实现在 `internal/web/session_resolver.go`。

客户端请求到达后，`.Resolve()` 按以下优先级决定重用哪个会话：

1. **显式会话（`X-M365-Session-Id`）**：请求头显式指定的会话 ID 优先级最高，不参与任何身份判定，由调用方主动决定要连接到哪条云端对话。
2. **内容键前缀命中**：当请求的消息序列与某条已记录会话的历史**完全一致**（按最近 3 条消息计算内容指纹）时，直接复用该会话及其云端对话。此时返回的 `HistoryLen` 表示「云端对话已包含的消息条数」，上层据此只发送 `messages[HistoryLen:]` 增量。
3. **相似度兜底**：若消息不是严格前缀，但与某条最近活跃（`M365_CONTEXT_TTL_MINUTES` 窗口内）会话的最后消息相似度超过阈值（`M365_CONTEXT_SIMILARITY`，默认 0.6），仍复用该会话（此时增量边界未知，发送全量）。
4. **兜底新建**：都未命中时，按 `user` 字段 / IP+UA 指纹或轮询绑到合适的账号与轮询逻辑新建会话。

几个特性由此而来：

- **跨 IP / 跨账号复用**：内容指纹作为键全局唯一主键，不关心发起方是谁——换一台机器、换一个 M365 账号，只要对话上下文一致就能接上同一条云端会话。
- **只发增量**：严格前缀命中时上层只补发新消息，等价于把云端对话当作上下文缓存用。
- **线程与清理联动**：会话绑定持久化在 `sessions.json`（0600），过期时间由 `M365_SESSION_TTL_MINUTES` 控制；长期无命中的会话会随自动清理按同一窗口（默认 2 小时）被回收。

## 内容自动清理

云端对话被视作「缓存条目」：**会话命中 = 刷新存活时间；空闲 = 过期**。后台循环默认每 30 分钟回收：

- 空闲超过 `M365_AUTO_CLEANUP_MAX_AGE_HOURS`（默认 2 小时）的云端对话；
- 或超出数量上限 `M365_AUTO_CLEANUP_KEEP_N`（默认 100）的最老对话。

**以下对话永不回收**：白名单对话、有活跃会话绑定正在引用的对话、最近使用过的用户会话。删除云端对话时会联动清理本地索引与会话绑定，杜绝幽灵会话，防止后续请求复用已删除的对话导致串号或报错。详见 `internal/web/auto_cleanup.go`。

## API 端点参考

### 对外兼容端点（`/v1/*`）

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/models` | GET | 模型目录 |
| `/v1/chat/completions` | POST | 聊天补全（流式 / 工具调用） |
| `/v1/responses` | POST | OpenAI Responses 协议 |
| `/v1/messages` | POST | Anthropic Messages（需 `x-api-key` + `anthropic-version`） |
| `/v1/images/generations` | POST | 图像生成 |
| `/v1/sessions` | GET / POST | 查询会话绑定 / 按 `session_id` 查询或创建 |
| `/v1/sessions/{id}` | DELETE | 解除会话绑定 |

### 管理 API（`/api/*`，需管理员登录态）

| 端点 | 说明 |
|------|------|
| `/api/admin/login` · `/logout` · `/session` | 管理端登录态 |
| `/api/admin/change-password` | 修改管理员密码（首次登录强制） |
| `/api/admin/keys` | API Key 管理（创建 / 撤销 / 回读） |
| `/api/admin/settings` | 运行时设置查看与修改 |
| `/api/admin/proxy-pool` | 代理池管理 |
| `/api/accounts` · `/refresh` · `/delete` | 账号管理 |
| `/api/auth/start` · `status` · `callback` | PKCE 授权流程 |
| `/api/conversations` · `/api/m365/conversations` | 本地 / 云端对话列表、删除、清理、白名单 |
| `/api/stats` · `/stats/reset` | 缓存命中统计 |
| `/api/usage` · `/usage/logs` | 用量统计仪表盘与明细 |
| `/api/chat` · `/chat/stream` | 控制台内即时对话 |
| `/api/health` · `/api/version` | 健康检查 / 版本 |

## 测试

仓库自带完整单元测试（会话解析、自动清理、工具路由、协议兼容、用量统计等），运行：

```bash
go test ./...
```

例如会验证：默认自动清理闲置窗口为 2 小时（`internal/web/auto_cleanup_test.go`）、内容键前缀命中只发送增量（`session_resolver_test.go`）、Responses / Anthropic 协议事件序列等。

## 目录结构

```
M365-Copilot2API/
├── cmd/server/            # 入口，HTTP 服务启动
├── internal/
│   ├── web/               # HTTP 路由、会话解析器、自动清理、管理 API、用量统计
│   │   ├── session_resolver.go   # 内容键会话复用（四重指纹）
│   │   ├── auto_cleanup.go        # 云端对话自动清理
│   │   ├── usage.go               # usage.jsonl 用量统计
│   │   └── ...                    # 工具调用、协议转换、代理池、密钥管理等
│   ├── chathub/           # M365 Copilot ChatHub WebSocket 客户端
│   ├── auth/              # OAuth / PKCE
│   ├── mcp/               # MCP 工具网关（SSE / JSON-RPC）
│   └── outbound/          # HTTP 代理池
├── web/                   # 管理控制台（纯 HTML / JS 单页）
├── scripts/               # 运维脚本
│   ├── e2e_test.py        # 端到端测试
│   ├── chathub_probe.py   # ChatHub 协议探针
│   ├── test-recorder.ps1  # Windows 测试录制
│   └── m365-upload-forensic-trace.user.js  # 上传取证脚本
├── docs/screenshots/      # 界面截图
├── manage.py              # start / stop / status / logs / err 进程管理
├── docker-compose.yml · Dockerfile
└── data/                  # 运行数据（由 M365_DATA_DIR 指定）
```

## 安全说明

- **默认仅监听内网**：直接运行二进制默认 `M365_LISTEN=127.0.0.1:4141`；对外提供服务务必通过 TLS 终泄反向代理（Nginx / Caddy），并为 SSE 与 WebSocket 开启长连接与 `proxy_buffering off`。
- **首次登录强制改密**：使用默认密码或引导密码完成首次登录后必须修改管理员密码。
- **密钥最小暴露**：API Key 控制台创建后即可回读，请妥善保护控制台访问权限。
- **数据落盘权限**：账号凭据、Token 缓存、会话绑定、API Key 等数据文件以 `0600` 权限写入，数据目录建议 `0700`。请定期备份数据目录。

## 常见问题

**Q1：为什么云端对话越来越多？**

后台每 30 分钟自动清理一次：回收闲置超过 2 小时（`M365_AUTO_CLEANUP_MAX_AGE_HOURS`，默认 2）或超出数量上限（`M365_AUTO_CLEANUP_KEEP_N`，默认 100）的云端对话；被活跃会话引用、白名单中的对话永不回收。调低这两个值可以清理得更激进；彻底关闭用 `M365_AUTO_CLEANUP=0`（不推荐，云端对话会无限膨胀，可能触发风控）。

**Q2：如何切换 M365 账号？**

不需要切换。多账号场景下网关自动轮询所有可用账号，单账号故障自动转移到下一个。要增加账号，直接在控制台发起新的 PKCE 授权即可。

**Q3：Claude Code 提示「认证可能不工作」怎么办？**

通常是系统环境变量残留了 `ANTHROPIC_API_KEY`，或同时配置了 `ANTHROPIC_AUTH_TOKEN` 导致两种认证方式冲突。只保留 `~/.claude/settings.json` 中的 `ANTHROPIC_API_KEY`（settings 会覆盖系统级变量），并删除系统级残留或 `AUTH_TOKEN`。

**Q4：X-M365-Session-Id 是什么？**

网关默认按内容（上下文前缀 / 相似度）自动复用会话；当你希望在客户端侧显式控制会话与云端对话的对应关系时，携带 `X-M365-Session-Id` 请求头，网关直接绑定到该 ID（本地内容指纹不再参与优先级判定）。

**Q5：对话出现串号 / 上下文错乱？**

会话绑定在到期后会自动清除。若本地缓存与云端不同步，可在控制台「对话」页手动删除该云端对话，网关会连同本地绑定一起清理重建。

## 贡献指南

PRs Welcome！提交前请留意：

1. Fork 仓库并创建独立分支，一个 PR 聚焦一个问题。
2. 切勿提交任何凭据、cookie、账号缓存、日志或构建产物。
3. 改动 Go 文件前先 `gofmt -w`，提交前跑完 `go test ./...`、`go vet ./...` 与 `go build ./...`。
4. 描述行为变化，涉及新逻辑时附上对应测试。

详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

[MIT License](LICENSE)。
