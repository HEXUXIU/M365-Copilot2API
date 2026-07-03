<div align="center">

# m365-copilot2api

**将 Microsoft 365 Copilot 的 WebSocket 接口转换为 OpenAI / Anthropic 兼容 HTTP API**

[![Python 3.10+](https://img.shields.io/badge/python-3.10%2B-blue.svg)](https://www.python.org/downloads/)
[![License](https://img.shields.io/badge/license-Research%20Only-critical.svg)](#免责声明)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-informational.svg)]()
[![Stars](https://img.shields.io/github/stars/HEXUXIU/M365-Copilot2API?style=social)](https://github.com/HEXUXIU/M365-Copilot2API/stargazers)
[![Forks](https://img.shields.io/github/forks/HEXUXIU/M365-Copilot2API?style=social)](https://github.com/HEXUXIU/M365-Copilot2API/forks)
[![CI](https://github.com/HEXUXIU/M365-Copilot2API/workflows/CI/badge.svg)](https://github.com/HEXUXIU/M365-Copilot2API/actions)

[快速开始](#快速开始) · [CLI 使用](#cli-使用) · [API 端点](#api-端点) · [已知限制](#已知限制) · [免责声明](#免责声明)

</div>

---

> ⚠️ **本项目仅用于学习和研究 Microsoft 365 Copilot 通信协议。** 使用即表示同意 [免责声明](#免责声明)。

---

## 这是什么

M365 Copilot 的浏览器界面通过一个未公开的 SignalR WebSocket 与后端通信。本项目将这个 WebSocket 接口包装成标准的 HTTP API，让你能在终端或任何 OpenAI 兼容客户端中使用它。

**实现的**: 文本对话流式/非流式输出、多轮上下文、session 续传

**未实现的**: 图像生成、文件上传、代码解释器 — WebSocket 端点存在但本项目未对接

**假实现的**: Claude 模型标签（前端壳，后端统一走 GPT-5）

---

## 快速开始

### 安装

```bash
git clone https://github.com/HEXUXIU/M365-Copilot2API.git
cd M365-Copilot2API
pip install -e .
```

### 配置

**只需一步：** 在浏览器中登录后，Console 运行一行 JS 获取所有信息。

```bash
m365-copilot-setup
```

向导会让你：
1. 在浏览器 Console 粘贴运行一行 JS（自动提取 Tenant ID / OID / Refresh Token）
2. 复制控制台输出的完整 JSON 并粘贴到终端
3. 自动加密存储 Token 并验证

> **提示**: 如果已登录 `m365.cloud.microsoft`，无需再次登录浏览器。

### 使用

```bash
m365-copilot "你好"                    # 提问
m365-copilot -i                         # 交互模式（多轮）
m365-copilot-server --port 8000         # 启动 HTTP API 服务
```

---

## CLI 使用

```bash
# 指定模型 (实际都是 GPT-5，只是传给后端的参数不同)
m365-copilot --model gpt5.2 "写一个快速排序"

# 多轮交互模式
m365-copilot -i

# 其他模型标识 (最终都走 GPT-5)
m365-copilot --model claude "你好"      # Claude 标签，实际走 GPT-5
m365-copilot --model reasoning "999*999"  # 后端可能有不同处理
```

### 参数

| 参数 | 说明 |
|------|------|
| `--model` | 传给后端的模型标识: `auto`/`quick`/`reasoning`/`gpt5.2`-`gpt5.5`/`claude` |
| `--reasoning` | 同 `--model reasoning` |
| `-i` | 交互模式，保留多轮上下文 |
| `--no-stream` | 等待完整回复，而非逐字输出 |
| `--list-models` | 列出所有可用模型标识 |
| `--setup` | 运行配置向导 |

---

## API 服务

```bash
m365-copilot-server --port 8000
curl http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"Hello"}]}'
```

### 支持的端点

| 端点 | 说明 |
|------|------|
| `POST /v1/chat/completions` | OpenAI Chat Completions 格式，支持 stream |
| `POST /v1/completions` | OpenAI 文本补全 |
| `POST /v1/messages` | Anthropic Messages 格式 |
| `POST /v1/complete` | Anthropic Complete (FIM) |
| `GET /v1/models` | 返回模型列表 |

### Python 调用

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8000/v1", api_key="any")
resp = client.chat.completions.create(
    model="auto", messages=[{"role": "user", "content": "你好"}]
)
print(resp.choices[0].message.content)
```

---

## 自定义工具

内置 3 个工具（时间、计算、骰子）。自定义工具无需改代码，放在 `~/.m365-copilot/tools/` 下即可自动加载：

```python
# ~/.m365-copilot/tools/weather.py
from m365_copilot.tools import provider

@provider.register(name="get_weather", description="获取指定城市的天气")
def get_weather(city: str) -> str:
    # 调用天气 API
    return f"{city}: 25℃, 晴朗"
```

```bash
$ m365-copilot --list-tools
  计算 (calculate)
  时间 (get_current_time)
  骰子 (roll_dice)
  天气 (get_weather)          ← 自动加载

$ m365-copilot "北京天气怎么样"   # 自动调用 get_weather
```

也可以通过源码中的 `MCPBridge` 集成标准 MCP 服务器（需自行补全集成代码）。

## 已知限制

| 功能 | 状态 | 说明 |
|------|------|------|
| 文本对话 | ✅ 可用 | 流式/非流式，多轮，session 续传 |
| 模型切换 | ⚠️ 传参标识 | 所有模型标识实际走 GPT-5 |
| 图像生成 | ❌ 未实现 | payload 存在但无响应解析 |
| Token 计数 | ⚠️ 粗估 | 空格分词，非真实 BPE 计数 |
| REST 对话管理 | ⚠️ 需 Cookie | 需浏览器 Cookie，Web UI 有区域限制 |

---

## 架构

```
Your App → m365-copilot2api → substrate.office.com (SignalR) → M365 Copilot Backend
              HTTP API               WebSocket (JSON/\x1e)
```

认证流程: PCA SPA + PKCE → Refresh Token (AES 加密存储) → Access Token (内存缓存 ~1.2h)

---

## 配置

环境变量（`.env` 或系统变量）:

```
M365_TENANT_ID=xxx-xxx-xxx-xxx
M365_USER_OID=xxx-xxx-xxx-xxx
M365_CLIENT_ID=4765445b-32c6-49b0-83e6-1d93765276ca  # 可选
```

手动获取 Tenant ID 和 User OID（如果 setup 失败）：

```javascript
// 浏览器登录 m365.cloud.microsoft 后，F12 Console 运行:
(() => { const k = Object.keys(localStorage).find(k => k.startsWith('msal.') && k.includes('|')); if (!k) return 'NOT_FOUND'; const p = k.split('|')[1].split('.'); return JSON.stringify({oid: p[0], tenant: p[1]}); })()
```

---

## 免责声明

本项目为**个人学习与研究作品**，旨在探索公开可观察的网络通信协议。

**使用本项目即表示您确认：**
- 您拥有合法的 Microsoft 365 Copilot 授权
- 仅用于个人学习研究，非商业用途
- 理解使用非官方接口可能导致的账号风险
- 自愿承担一切后果

**本项目不：**
- 破解加密或绕过认证
- 获取/泄露他人数据
- 干扰 Microsoft 服务
- 与 Microsoft Corporation 有任何关联

如不同意请立即停止使用。

---

<div align="center">

```
为好奇心而建，请谨慎使用。
```

⭐ Star if useful · ⚠️ At your own risk

</div>
