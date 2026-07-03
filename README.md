<div align="center">

# m365-copilot2api

**将 Microsoft 365 Copilot 转换为 OpenAI / Anthropic 兼容 API**

[![Python 3.10+](https://img.shields.io/badge/python-3.10%2B-blue.svg)](https://www.python.org/downloads/)
[![License](https://img.shields.io/badge/license-Research%20Only-critical.svg)](#免责声明)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey.svg)]()

*脱离浏览器，直通 Substrate WebSocket。*

[快速开始](#快速开始) · [使用指南](#使用指南) · [API 端点](#api-端点) · [配置说明](#配置说明)

</div>

---

> ⚠️ **本项目仅用于学习和研究目的。** 使用本项目即表示您已阅读并同意 [免责声明](#免责声明) 中的全部条款。

---

## 特性

- **完整的 Copilot 对话能力** — 流式/非流式、多轮对话、深度思考（Reasoning）
- **OpenAI 兼容 API** — 直接替换 `base_url` 即可接入现有 OpenAI 生态工具
- **Anthropic Messages 兼容** — 同时支持 `/v1/messages` 端点
- **本地工具集成** — 意图检测 + 本地执行（时间计算、数学运算、掷骰子等）
- **模型切换** — Auto / Quick / Reasoning / GPT-5.2-5.5 / Claude_Sonnet<sup>†</sup>
- **凭证加密存储** — Refresh token 通过 AES-128-CBC 加密后落盘
- **零浏览器依赖** — Token 刷新通过 Origin Spoofing 绕过 SPA 限制

> <sup>†</sup> `Claude_Sonnet` 标签被服务器接受，但后端实际路由至 GPT-5。详见 [模型说明](#模型说明)。

---

## 快速开始

### 环境要求

- Python 3.10 或更高版本
- 已登录 [M365 Copilot](https://m365.cloud.microsoft) 的 Microsoft 账号

### 三步部署

**Step 1 — 克隆并安装**

```bash
git clone <仓库地址>
cd m365-copilot
pip install -e .
```

**Step 2 — 配置凭证**

```bash
# 运行配置向导（交互式引导）
m365-copilot-setup
```

向导将引导您完成：
1. 从 Microsoft Graph API 获取 Tenant ID 和 User OID
2. 通过浏览器登录获取 Refresh Token（自动加密存储）
3. 验证 Token 有效性

**Step 3 — 开始使用**

```bash
# 命令行提问
m365-copilot "你好，今天天气怎么样？"

# 交互模式
m365-copilot -i

# 查看所有可用模型
m365-copilot --list-models
```

---

## 使用指南

### CLI 命令

```bash
# 基础提问
m365-copilot "用一句话解释量子纠缠"

# 选择模型
m365-copilot --model gpt5.2 "写一个快速排序"

# 深度思考模式
m365-copilot --reasoning "证明费马大定理" --no-stream

# 交互模式（保留上下文）
m365-copilot -i --model reasoning

# 图像生成
m365-copilot --image "画一只在月球上的猫"

# 禁用工具检测
m365-copilot --no-tools "你好"
```

### 参数一览

| 参数 | 说明 |
|------|------|
| `prompt` | 提问内容（位置参数） |
| `--model` | 模型选择: `auto`, `quick`, `reasoning`, `gpt5.2`-`gpt5.5`, `claude` |
| `--reasoning` | 启用深度思考模式 |
| `-i, --interactive` | 交互模式（多轮对话） |
| `--no-stream` | 禁用流式输出 |
| `--no-tools` | 禁用本地工具检测 |
| `--image` | 启用图像生成 |
| `--list-models` | 列出所有可用模型 |
| `--list-tools` | 列出已注册的本地工具 |
| `--setup` | 运行配置向导 |
| `--setup-cookies` | 从浏览器提取 M365 Cookies |

---

## API 服务器

启动 OpenAI 兼容的 HTTP 服务，无缝接入任何支持自定义 `base_url` 的 AI 客户端。

```bash
m365-copilot-server --port 8000
```

### 端点

| 端点 | 兼容 | 说明 |
|------|------|------|
| `POST /v1/chat/completions` | OpenAI | 对话补全（流式/非流式） |
| `POST /v1/completions` | OpenAI | 文本补全（FIM） |
| `POST /v1/messages` | Anthropic | Messages API |
| `POST /v1/complete` | Anthropic | Complete API (FIM) |
| `GET /v1/models` | OpenAI | 模型列表 |
| `GET /health` | — | 健康检查 |

### 调用示例

```bash
curl http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt5.2",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8000/v1",
    api_key="any-value"
)

response = client.chat.completions.create(
    model="auto",
    messages=[{"role": "user", "content": "你好"}]
)
print(response.choices[0].message.content)
```

---

## 配置说明

### 环境变量

| 变量 | 必须 | 说明 |
|------|:----:|------|
| `M365_TENANT_ID` | ✅ | Azure AD 租户 ID |
| `M365_USER_OID` | ✅ | 用户 Object ID |
| `M365_CLIENT_ID` | 可选 | 默认使用公共 PCA 客户端 |

配置方式（任选其一）：

- 项目根目录创建 `.env` 文件（推荐，已 gitignore）
- 系统环境变量

### 手动获取凭证

如果配置向导无法使用，可手动获取：

1. 浏览器登录 `https://m365.cloud.microsoft`
2. 打开 DevTools (F12) → Console，依次运行：

```javascript
// 获取 User OID
fetch('https://graph.microsoft.com/v1.0/me')
  .then(r => r.json())
  .then(d => console.log('OID:', d.id))

// 获取 Tenant ID
fetch('https://graph.microsoft.com/v1.0/organization')
  .then(r => r.json())
  .then(d => console.log('TENANT:', d.value[0].id))
```

3. 将输出值写入 `.env` 文件

### 文件结构

```
m365-copilot/
├── src/m365_copilot/      # 核心源码
│   ├── auth.py            # Token 管理与刷新
│   ├── client.py          # SignalR WebSocket 客户端
│   ├── payload.py         # 消息构建
│   ├── models.py          # 模型定义与配置
│   ├── scripts/           # 配置向导
│   ├── tools/             # 本地工具系统
│   └── servers/           # CLI + HTTP 服务
├── data/tokens/           # 凭证存储 (加密, gitignore)
├── .env.example           # 环境变量模板
└── pyproject.toml
```

---

## 模型说明

| 模型 | 内部标识 | 实测后端 | 耗时 |
|------|---------|---------|------|
| Auto | `Magic` tone | GPT-5 | ~5s |
| Quick | `Chat` tone | GPT-5 | ~4s |
| Reasoning | `Reasoning` tone | GPT-5 | ~8s |
| GPT 5.2–5.5 | `Gpt_5_X_Chat` override | GPT-5 | ~4s |
| Claude Sonnet | `Claude_Sonnet` override | **GPT-5** ⚠️ | ~5s |

> **注意**: `Claude_Sonnet` 的 `gptIdOverride` 字段被服务器接受，但穿透测试表明后端统一使用 GPT-5。Claude 标签仅为前端 UI 展示。

### 安全说明

- **Refresh Token 加密存储** — 使用 Fernet (AES-128-CBC) 加密，密钥独立存放于 `~/.m365-copilot/encryption.key`
- **密钥与数据分离** — 加密密钥不在项目目录内，即使项目目录泄露，凭证也无法解密
- **自动迁移** — 首次运行时自动将已有明文 Token 升级为加密存储
- **Token 刷新** — Access token 缓存约 1.2 小时，Refresh token 有效期约 23 小时，自动续期

---

## 架构

```
┌──────────────┐     ┌───────────────────────────────┐
│  Your App    │────▶│  M365 Copilot CLI / API Server │
│  (任何客户端)  │     │  m365-copilot / openai compat  │
└──────────────┘     └──────────────┬────────────────┘
                                   │
                    ┌──────────────▼──────────────┐
                    │  Substrate WebSocket          │
                    │  substrate.office.com         │
                    │  SignalR / ChatHub            │
                    └──────────────┬──────────────┘
                                   │
                    ┌──────────────▼──────────────┐
                    │  Microsoft 365 Copilot       │
                    │  Backend (GPT-5)              │
                    └─────────────────────────────┘
```

---

## 免责声明

<div align="center">

### ⚠️ 使用本项目前，请仔细阅读以下条款 ⚠️

</div>

**1. 项目性质**

本项目为**个人学习与研究作品**，旨在探索 Microsoft 365 Copilot 的通信协议和接口设计。项目中不包含、不获取、不存储任何 Microsoft 专有代码、逆向工程产物或商业机密。

**2. 使用范围**

- ✅ **允许**: 个人学习、技术研究、接口兼容性开发
- ❌ **禁止**: 商业用途、批量爬取、绕过付费墙、服务转售、任何违反 Microsoft 服务条款的行为

**3. 用户责任**

使用本项目即表示您确认：
- 您拥有合法的 Microsoft 365 Copilot 使用授权
- 您仅将本项目用于个人学习和研究目的
- 您不会将本项目用于任何违法或侵权用途
- 您理解使用非官方 API 可能导致账号受限或终止的风险
- 您自愿承担使用本项目的一切后果

**4. 免责条款**

- 作者不对因使用本项目导致的**任何直接或间接损失**负责，包括但不限于账号封禁、数据丢失、服务中断
- 本项目按"现状"提供，不保证功能完整性或可用性
- 作者保留随时更新或终止项目的权利
- 项目的存在不构成对任何第三方服务的使用建议或鼓励

**5. 合规声明**

本项目仅实现了公开可观察的 WebSocket 通信协议，未涉及：
- 破解加密算法或数字版权管理
- 绕过身份认证机制
- 获取或泄露其他用户数据
- 修改或干扰 Microsoft 服务正常运行

**6. 知识产权**

本项目的知识产权归作者所有。本项目与 Microsoft Corporation 或其关联公司无任何关联、赞助或认可关系。Microsoft、M365、Copilot 是 Microsoft Corporation 的商标。

---

> 如不同意以上任一条款，请立即停止使用并删除本项目。继续使用即视为接受全部条款。

---

<div align="center">

**[⬆ 回到顶部](#m365-copilot-cli)**

*Built for curiosity. Use with responsibility.*

</div>
