# M365 Copilot2API

> 本 README 为项目主文档，使用中文维护。Web 控制台的中英文 i18n 仅用于控制台界面语言切换。

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

## 界面预览

<p align="center"><img src="docs/screenshots/02-dashboard.png" alt="仪表盘" style="max-width:860px;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.18)"></p>

<table>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/01-login.png" alt="登录页" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>登录</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/03-usage.png" alt="用量统计" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>用量统计</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/04-accounts.png" alt="账号管理" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>账号管理</b></sub></td>
  </tr>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/05-apikeys.png" alt="API Keys" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>API Keys</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/06-conversations.png" alt="对话管理" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>对话管理</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/07-proxies.png" alt="代理池" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>代理池</b></sub></td>
  </tr>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/08-modeltest.png" alt="模型测试" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>模型测试</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/09-settings.png" alt="设置" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>设置</b></sub></td>
    <td align="center" width="33%"><sub><i>更多功能，等你发现</i></sub></td>
  </tr>
</table>

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
| 多模态输入 | 支持图片等附件（base64 data URL / https URL），自动完成 M365 上传与消息注解注入 |
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
| `M365_PUBLIC_IDENTITY_POLICY` | `true` | 公开身份策略总开关；设为 `false` / `0` / `off` 时关闭身份预设及所有正文、推理、引用和流式清洗 |

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
| `M365_BROWSER_CLIENT_ID` / `M365_BROWSER_AUTHORITY` / `M365_BROWSER_REDIRECT_URI` / `M365_BROWSER_SCOPE` | 内置 | 仅用于浏览器 PKCE 的 OAuth 配置；authority 可使用 `common`、`organizations` 或 `consumers` |
| `M365_DEVICE_CLIENT_ID` / `M365_DEVICE_AUTHORITY` / `M365_DEVICE_SCOPE` | 内置 | 仅用于 Device Code 的 OAuth 配置 |

Loopback 回调需显式启用。请先在 Azure 应用注册中登记完全一致的 loopback URI，例如 `http://127.0.0.1:4141/api/auth/callback`，再同时设置 `M365_BROWSER_CLIENT_ID` 和 `M365_BROWSER_REDIRECT_URI`。内置第一方客户端可能会因 loopback URI 未注册而返回 `AADSTS50011`，因此默认仍使用兼容性更好的 `nativeclient` 重定向，并由用户手动粘贴回调 URL。

选择 `common`、`organizations` 或 `consumers` 只会控制 Microsoft 可提供的账号类别，不会绕过 Microsoft 身份验证、多重身份验证（MFA）、租户策略、账号类型限制、授权同意、许可证或服务资格检查。自定义 Client ID 不保证能够访问私有 Substrate scope；个人 Microsoft 账号成功登录也不代表该账号具备 M365 Copilot 服务资格。

### 工具与网络搜索

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_ENABLE_WEB_SEARCH` | 开 | 自动注入 `web_search` 声明（`0` / `false` / `off` 关闭）。开启后每次对话都会像 M365 网页版那样注册 BingWebSearch 内建插件，模型可基于实时搜索结果作答 |

> 说明：`web_search` 是服务端内建工具（`BingWebSearch`），不会出现在下发给客户端的 `tool_calls` 里；搜索结果以 `SearchResults` 引用形式出现在回答流中。客户端若要自行声明 `web_search`（type 或 function name），网关不会重复注入。

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

### 多模态图片输入（OpenAI 格式）

客户端用标准的 OpenAI `image_url` 格式传图即可，网关会自动把图片上传到 M365 的 `UploadFile` 端点，并在 ChatHub 消息里注入文件注解（无需客户端感知上游细节）：

```bash
# base64 data URL 方式
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "这张图里是什么颜色？"},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB..."}}
      ]
    }]
  }'
```

也可以直接传 https 图片 URL（仅公网地址，带 SSRF 防护；本地图请用 data URL）。Responses 协议的 `input_image` / `input_file` 同样支持。

### Anthropic 格式（Claude Code / Cursor）

```bash
curl http://127.0.0.1:4141/v1/messages \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6-sol","max_tokens":1024,"messages":[{"role":"user","content":"你好"}]}'
```

上游返回的推理内容（ChainOfThought）会映射为 Anthropic `thinking` block，Claude Code 中可正常显示与使用。

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

控制台「API Keys」页的「使用 API 密钥」弹窗可直接生成 Claude Code 的 `settings.json` 配置与终端环境变量，复制即可。

> ⚠️ **认证冲突提醒**：如果系统环境变量残留了 `ANTHROPIC_API_KEY`，或同时配置了 `ANTHROPIC_AUTH_TOKEN`，Claude Code 会告警「认证可能不工作」。请二选一：让 `settings.json` 的 `env` 覆盖系统级变量，或删除系统级 `ANTHROPIC_*`。

## 可用模型

网关默认内置模型映射（可在控制台「设置」页增删与调整默认推理级别）：

| 模型 | 默认推理级别 | 说明 |
|------|-------------|------|
| `gpt-5.6-sol` | `low` | 默认模型 |
| `gpt-5.6-terra` | `medium` | 推理折中 |
| `gpt-5.6-luna` | `medium` | 推理折中 |

- 模型映射把公开模型名翻译成上游 tone；控制台可增删映射、调整默认推理级别。
- 推理强度还可通过请求内的 `reasoning_effort` 参数调整。
- M365 订阅会上线的新模型名（如 `gpt-5.2`、`gpt-5.4`、`codex` 系）以实际目录为准，可在控制台配置导入。

## 账号亲和与会话复用

多账号模式使用 Redis 保存租户隔离的账号亲和与微软云端会话绑定。两者是独立状态：相同首轮请求可以稳定落到同一账号，但仍会创建独立云端对话；只有显式会话 ID、Responses 会话链，或包含 assistant 消息的严格历史前缀才能续接已有云端对话。

新会话使用带并发上限的 Rendezvous Hash 分配健康账号。热会话优先保留原账号；短暂的 429/503（默认 `Retry-After <= 5s`）会先在原账号重试一次，持续限流或认证失败才迁移。迁移采用成功后 CAS 切换，失败不会覆盖旧绑定。

缓存统计只在微软云端对话成功续接后产生，迁移、冷请求和失败请求均为 0。标准字段如下：

- Chat Completions：`usage.prompt_tokens_details.cached_tokens`
- Responses：`usage.input_tokens_details.cached_tokens`
- Anthropic Messages：`usage.cache_read_input_tokens`
- 流式响应：只在成功终止 usage 事件中输出

`M365_AFFINITY_MODE=observe` 只采集和预热绑定，不改变现有路由；确认 `/api/health` 中 Redis 状态正常后切换为 `enforce`。Redis 异常时请求会降级到进程内存储，缓存统计保持保守值，不会把普通历史消息误报成命中。

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
| `/api/admin/models` · `/models/test` | 模型目录 / 单模型连通测试（不依赖明文 Key） |
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
│   ├── genprobe.py        # 图像生成协议探针（原始帧 dump）
│   ├── multimodal_probe.py # 多模态图片输入探针（上传 + 注解流程）
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

不需要手动切换。多账号场景下，新会话按稳定亲和键分配到健康账号，热会话固定原账号；只有持续限流、认证失败或并发容量已满时才转移。要增加账号，直接在控制台发起新的 PKCE 授权即可。

**Q3：Claude Code 提示「认证可能不工作」怎么办？**

通常是系统环境变量残留了 `ANTHROPIC_API_KEY`，或同时配置了 `ANTHROPIC_AUTH_TOKEN` 导致两种认证方式冲突。只保留 `~/.claude/settings.json` 中的 `ANTHROPIC_API_KEY`（settings 会覆盖系统级变量），并删除系统级残留或 `AUTH_TOKEN`。

**Q4：X-M365-Session-Id 是什么？**

网关只通过显式 ID 或包含 assistant 消息的严格历史前缀复用云端会话；携带 `X-M365-Session-Id` 可让客户端稳定控制会话与账号归属，并避免依赖无状态历史查找。

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
