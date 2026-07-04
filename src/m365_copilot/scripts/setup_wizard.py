"""
M365 Copilot 一键配置向导
运行: python -m m365_copilot.scripts.setup
"""
import os, sys, json, re, ssl, urllib.request

if sys.stdout.encoding and sys.stdout.encoding.lower() not in ('utf-8', 'utf8'):
    try:
        sys.stdout.reconfigure(encoding='utf-8')
    except Exception:
        pass

CLIENT_ID = "4765445b-32c6-49b0-83e6-1d93765276ca"
SCOPE = "https://substrate.office.com/sydney/.default openid profile offline_access"

BASE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
DATA_DIR = os.path.join(BASE_DIR, "data", "tokens")
ENV_FILE = os.path.join(BASE_DIR, ".env")


def step(title):
    print(f"\n{'='*60}")
    print(f"  {title}")
    print(f"{'='*60}")


def extract_from_console_output(raw: str):
    """Parse tenant, oid, refresh_token from browser console output."""
    raw = re.sub(r'^粘贴\s*=>\s*', '', raw)
    raw = re.sub(r'^PS\s+[^>]+>\s*', '', raw)
    raw = re.sub(r'^>\s*', '', raw)

    tenant = oid = refresh_token = None

    m_oid = re.search(r"OID:\s*([a-f0-9-]+)", raw)
    m_tenant = re.search(r"TENANT:\s*([a-f0-9-]+)", raw)
    if m_oid and m_tenant:
        oid = m_oid.group(1)
        tenant = m_tenant.group(1)

    m_rt = re.search(r"REFRESH[_\s]?TOKEN[:\s]+([A-Za-z0-9\-_.]+)", raw)
    if m_rt:
        refresh_token = m_rt.group(1)

    if not refresh_token:
        m_rt2 = re.search(r"refresh[_\s]?token[:\s]+([A-Za-z0-9\-_.]{20,})", raw, re.I)
        if m_rt2:
            refresh_token = m_rt2.group(1)

    if not refresh_token or refresh_token == "NOT_FOUND":
        m = re.search(r"\{", raw)
        if m:
            start = m.start()
            depth = 0
            in_string = False
            escape = False
            for i in range(start, len(raw)):
                c = raw[i]
                if escape:
                    escape = False
                    continue
                if c == '\\' and in_string:
                    escape = True
                    continue
                if c == '"' and not escape:
                    in_string = not in_string
                    continue
                if not in_string:
                    if c == '{':
                        depth += 1
                    elif c == '}':
                        depth -= 1
                        if depth == 0:
                            try:
                                data = json.loads(raw[start:i+1])
                                tenant = tenant or data.get("tenant")
                                oid = oid or data.get("oid")
                                refresh_token = data.get("refresh_token")
                            except json.JSONDecodeError:
                                pass
                            break

    return tenant, oid, refresh_token


def get_config_from_browser():
    step("步骤 1: 从浏览器获取配置")
    print()
    print("请在浏览器中完成以下操作:")
    print("  1. 打开 https://m365.cloud.microsoft 并登录")
    print("  2. 按 F12 打开 DevTools → Console")
    print("  3. 粘贴运行下面这行代码:")
    print()
    print("  (复制下面完整的一行)")
    print()
    print("-" * 60)
    js_snippet = (
        "(() => {"
        "const k = Object.keys(localStorage).find(k => k.startsWith('msal.') && k.includes('|'));"
        "if (!k) return 'NOT_FOUND';"
        "const p = k.split('|')[1].split('.');"
        "const oid = p[0], tenant = p[1];"
        "const entries = Object.entries(localStorage);"
        "const rtEntry = entries.find(([k]) => k.toLowerCase().includes('refreshtoken') && k.startsWith('msal.3'));"
        "let rt = 'NOT_FOUND';"
        "if (rtEntry) {"
        "  try { const d = JSON.parse(rtEntry[1]); rt = d.secret || d.value || JSON.stringify(d); }"
        "  catch(e) { rt = rtEntry[1]; }"
        "}"
        "return '===== 请复制下面完整的一行 JSON =====\\n' + JSON.stringify({oid, tenant, refresh_token: rt});"
        "})()"
    )
    print(js_snippet)
    print("-" * 60)
    print()
    print("  请复制控制台输出的【完整 JSON 字符串】（前后带 === 标记）")
    print("  如果显示 NOT_FOUND，请确认已在 m365.cloud.microsoft 登录")
    print()
    print("=" * 60)
    print("【请复制从这里开始 ==================================】")
    print("=" * 60)
    print()
    print("  提示: 只粘贴 JSON 部分（从 { 开始到 } 结束），不要带 === 标记")
    print()

    raw = input("粘贴 => ").strip()
    if not raw:
        print("错误: 未输入任何内容")
        sys.exit(1)

    tenant, oid, refresh_token = extract_from_console_output(raw)
    if not tenant or not oid:
        try:
            data = json.loads(raw)
            tenant = tenant or data.get("tenant")
            oid = oid or data.get("oid")
            refresh_token = refresh_token or data.get("refresh_token")
        except json.JSONDecodeError:
            pass

    if not tenant or not oid:
        print("错误: 无法解析 Tenant ID 或 User OID")
        print("请确保 Console 输出包含 OID 和 TENANT 字段")
        sys.exit(1)

    if not refresh_token or refresh_token == "NOT_FOUND":
        print("错误: 无法从输出中提取 Refresh Token")
        print("请确认已在 Console 中成功运行 JS 代码")
        sys.exit(1)

    return tenant, oid, refresh_token


def verify_token(tenant, oid, refresh_token):
    """Verify refresh token by exchanging for access token."""
    step("步骤 2: 验证 Token")
    print()

    rt_file = os.path.join(DATA_DIR, "rt_90day.txt")
    cache_file = os.path.join(DATA_DIR, "token_cache.json")
    os.makedirs(DATA_DIR, exist_ok=True)

    sys.path.insert(0, os.path.join(BASE_DIR, "src"))
    from m365_copilot.auth import TokenManager
    from m365_copilot.scripts.crypto import encrypt

    with open(rt_file, "w") as f:
        f.write(encrypt(refresh_token))
    print(f"  Refresh token 已加密保存")

    os.environ["M365_TENANT_ID"] = tenant
    os.environ["M365_USER_OID"] = oid

    tm = TokenManager(tenant, CLIENT_ID, SCOPE, rt_file, cache_file)
    try:
        access_token = tm.get()
        print(f"  ✓ Token 验证成功 (access_token 长度: {len(access_token)})")
    except Exception as e:
        print(f"  ✗ Token 验证失败: {e}")
        print("  可能原因: Refresh Token 已过期，请重新登录浏览器获取")
        sys.exit(1)

    return rt_file, cache_file


def save_env(tenant, oid):
    env_content = f"""# M365 Copilot Configuration
M365_TENANT_ID={tenant}
M365_USER_OID={oid}
M365_CLIENT_ID={CLIENT_ID}
"""
    with open(ENV_FILE, "w") as f:
        f.write(env_content)
    print(f"  环境变量已保存 → {ENV_FILE}")


def main():
    print("=" * 60)
    print("  M365 Copilot 配置向导 v2.0")
    print("=" * 60)
    print()
    print("只需一步: 在浏览器 Console 运行一行 JS，粘贴输出即可。")
    print()

    tenant, oid, refresh_token = get_config_from_browser()
    rt_file, cache_file = verify_token(tenant, oid, refresh_token)
    save_env(tenant, oid)

    step("配置完成!")
    print()
    print("使用方式:")
    print("  m365-copilot \"你好\"              # CLI 提问")
    print("  m365-copilot -i                  # 交互模式")
    print("  m365-copilot --list-models       # 列出模型")
    print("  m365-copilot-server --port 8000  # 启动 API 服务")
    print()
    print(f"Token 存储: {DATA_DIR}")
    print(f"配置文件:   {ENV_FILE}")


if __name__ == "__main__":
    main()
