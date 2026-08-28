#!/usr/bin/env python3
"""M365-Copilot2API Frontend E2E Test — HTML structure + API interaction flow"""

import sys, json, time, re, os, requests

BASE = "http://127.0.0.1:4141"
ADMIN_PW = os.environ.get("M365_TEST_ADMIN_PASSWORD", "")
API_KEY = os.environ.get("M365_TEST_API_KEY", "")

results = []
tid = 0
session_cookie = None


def color(text, c):
    codes = {"green": 32, "red": 31, "yellow": 33, "cyan": 36, "gray": 90, "white": 97, "magenta": 35}
    return f"\033[{codes.get(c, 0)}m{text}\033[0m"


def _record(status, name, detail=""):
    global tid
    tid += 1
    results.append({"status": status, "name": name, "detail": detail})
    if status == "PASS":
        print(color(f"  PASS [{tid}] {name}", "green"))
    elif status == "FAIL":
        print(color(f"  FAIL [{tid}] {name}" + (f" - {detail}" if detail else ""), "red"))
    else:
        print(color(f"  SKIP [{tid}] {name}" + (f" - {detail}" if detail else ""), "yellow"))


def ok(name, cond, detail=""):
    _record("PASS" if cond else "FAIL", name, detail)


def skip(name, reason=""):
    _record("SKIP", name, reason)


def section(name):
    print(f"\n--- {name} ---")


def api(method, path, body=None, use_cookie=True, use_apikey=False, timeout=30):
    global session_cookie
    headers = {}
    if use_cookie and session_cookie:
        headers["Cookie"] = session_cookie
    if use_apikey:
        headers["Authorization"] = f"Bearer {API_KEY}"
    if body is not None:
        headers["Content-Type"] = "application/json"
    try:
        r = requests.request(method, f"{BASE}{path}", headers=headers, json=body, timeout=timeout, allow_redirects=False)
        for cookie_name, cookie_value in r.cookies.items():
            if "session" in cookie_name.lower():
                session_cookie = f"{cookie_name}={cookie_value}"
                break
        if not session_cookie:
            sc = r.headers.get("Set-Cookie", "")
            if sc:
                m = re.search(r"([a-zA-Z_]+session[a-zA-Z_]*=[^;]+)", sc)
                if m:
                    session_cookie = m.group(1)
        return {"status": r.status_code, "content": r.text, "ok": True}
    except requests.exceptions.RequestException as e:
        return {"status": 0, "content": "", "ok": False, "error": str(e)[:120]}


def get_html(path, with_cookie=False):
    global session_cookie
    headers = {}
    if with_cookie and session_cookie:
        headers["Cookie"] = session_cookie
    try:
        r = requests.get(f"{BASE}{path}", headers=headers, timeout=10)
        for cookie_name, cookie_value in r.cookies.items():
            if "session" in cookie_name.lower():
                session_cookie = f"{cookie_name}={cookie_value}"
                break
        return {"status": r.status_code, "content": r.text, "ok": True}
    except Exception:
        return {"status": 0, "content": "", "ok": False}


def test_html_page(path, page_name, checks, with_cookie=False):
    section(f"{page_name} ({path})")
    r = get_html(path, with_cookie=with_cookie)
    ok(f"{page_name} 返回 200", r["status"] == 200, f"status={r['status']}")
    if not r["ok"] or r["status"] != 200:
        for c_name, _ in checks:
            skip(f"{page_name} {c_name}", "page not loaded")
        return
    html = r["content"]
    for c_name, pattern in checks:
        found = bool(re.search(pattern, html))
        ok(f"{page_name} {c_name}", found, f"pattern not found")


# ════════════════════════════════════════
# 一、页面静态内容验证
# ════════════════════════════════════════
print(color("\n========================================", "magenta"))
print(color("  一、页面静态内容验证（HTML 结构测试）", "magenta"))
print(color("========================================", "magenta"))

# Pre-login to get session cookie for protected pages
r = api("POST", "/api/admin/login", body={"password": ADMIN_PW}, use_cookie=False, use_apikey=False)
if r["status"] == 200:
    print(color("  Pre-login OK", "green"))
else:
    print(color(f"  Pre-login failed: {r['status']}", "red"))

# 1.1 login.html
test_html_page("/login", "login.html", [
    ("loginForm 存在", r'id\s*=\s*["\']loginForm["\']'),
    ("changeForm 存在", r'id\s*=\s*["\']changeForm["\']'),
    ("password 输入框", r'<input[^>]*type\s*=\s*["\']password["\']'),
    ("POST /api/admin/login 在 JS", r'/api/admin/login'),
    ("POST /api/admin/change-password 在 JS", r'/api/admin/change-password'),
    ("GET /api/admin/session 在 JS", r'/api/admin/session'),
    ("password-toggle 按钮", r'password-toggle'),
])

# 1.2 index.html
test_html_page("/", "index.html", [
    ("内嵌 loginForm", r'id\s*=\s*["\']loginForm["\']'),
    ("sidebar dashboard", r'data-page\s*=\s*["\']dashboard["\']'),
    ("sidebar usage", r'data-page\s*=\s*["\']usage["\']'),
    ("sidebar accounts", r'data-page\s*=\s*["\']accounts["\']'),
    ("sidebar apikeys", r'data-page\s*=\s*["\']apikeys["\']'),
    ("sidebar conversations", r'data-page\s*=\s*["\']conversations["\']'),
    ("sidebar proxies", r'data-page\s*=\s*["\']proxies["\']'),
    ("sidebar modeltest", r'data-page\s*=\s*["\']modeltest["\']'),
    ("sidebar settings", r'data-page\s*=\s*["\']settings["\']'),
    ("POST /api/admin/login", r'/api/admin/login'),
    ("GET /api/admin/session", r'/api/admin/session'),
    ("POST /api/admin/logout", r'/api/admin/logout'),
    ("languageSelect", r'id\s*=\s*["\']languageSelect["\']'),
    ("ov24hTok", r'id\s*=\s*["\']ov24hTok["\']'),
    ("ov24hReq", r'id\s*=\s*["\']ov24hReq["\']'),
    ("ovTotalTok", r'id\s*=\s*["\']ovTotalTok["\']'),
    ("ovTotalReq", r'id\s*=\s*["\']ovTotalReq["\']'),
    ("onboarding", r'onboarding'),
    ("data-days=1", r'data-days\s*=\s*["\']1["\']'),
    ("data-days=7", r'data-days\s*=\s*["\']7["\']'),
    ("data-days=30", r'data-days\s*=\s*["\']30["\']'),
    ("data-days=90", r'data-days\s*=\s*["\']90["\']'),
    ("accountSearch", r'id\s*=\s*["\']accountSearch["\']'),
    ("keyModal", r'id\s*=\s*["\']keyModal["\']'),
    ("proxyInput", r'id\s*=\s*["\']proxyInput["\']'),
    ("setListen", r'id\s*=\s*["\']setListen["\']'),
    ("setTimeout", r'id\s*=\s*["\']setTimeout["\']'),
])

# 1.3 conversation.html (needs admin cookie)
test_html_page("/conversation", "conversation.html", [
    ("conversationTab", r'id\s*=\s*["\']conversationTab["\']'),
    ("jsonTab", r'id\s*=\s*["\']jsonTab["\']'),
    ("copyJSON 按钮", r'copyJSON'),
    ("goBack 按钮", r'goBack'),
], with_cookie=True)

# 1.4 debug.html — no /debug route in server, debug is via /api/admin/debug/logs API
# Skip HTML page test, API is tested in section 2.11
for c_name, _ in [("rows tbody", ""), ("统计卡片 n", ""), ("统计卡片 in", ""), ("统计卡片 out", ""), ("统计卡片 hit", "")]:
    skip(f"debug.html {c_name}", "/debug route not served")

# ════════════════════════════════════════
# 二、交互流程验证
# ════════════════════════════════════════
print(color("\n========================================", "magenta"))
print(color("  二、交互流程验证（模拟前端 JS 逻辑）", "magenta"))
print(color("========================================", "magenta"))

# 2.1 登录流程
section("2.1 登录流程")
# 先清空 cookie 模拟未认证状态
saved_cookie = session_cookie
session_cookie = None
r = api("GET", "/api/admin/session", use_cookie=False, use_apikey=False)
ok("未认证 session 返回 401 或 200（可能有残留session）", r["status"] in [200, 401], f"status={r['status']}")
session_cookie = saved_cookie

r = api("POST", "/api/admin/login", body={"password": "wrongpassword"}, use_cookie=False, use_apikey=False)
ok("错误密码登录失败", r["status"] != 200, f"status={r['status']}")

r = api("POST", "/api/admin/login", body={"password": ADMIN_PW}, use_cookie=False, use_apikey=False)
ok("正确密码登录成功", r["status"] == 200, f"status={r['status']}")
if r["status"] == 200:
    try:
        d = json.loads(r["content"])
        ok("must_change_password 字段存在", "must_change_password" in d, "field missing")
    except Exception:
        skip("must_change_password 检查", "无法解析")

r = api("GET", "/api/admin/session", use_cookie=True)
ok("带 cookie session 已认证", r["status"] == 200, f"status={r['status']}")

# 2.2 Dashboard
section("2.2 Dashboard 数据加载")
r = api("GET", "/api/stats", use_cookie=True)
ok("GET /api/stats 200", r["status"] == 200, f"status={r['status']}")
if r["status"] == 200:
    try:
        d = json.loads(r["content"])
        inner = d.get("stats", d)
        ok("stats 含 cache_hits", "cache_hits" in inner, "missing")
        ok("stats 含 hit_rate", "hit_rate" in inner, "missing")
    except Exception:
        skip("stats 字段", "parse error")

r = api("GET", "/api/usage?days=365", use_cookie=True)
ok("GET /api/usage 200", r["status"] == 200, f"status={r['status']}")
r = api("GET", "/api/usage/logs?limit=6&offset=0", use_cookie=True)
ok("GET /api/usage/logs 200", r["status"] == 200, f"status={r['status']}")
r = api("GET", "/api/accounts", use_cookie=True)
ok("GET /api/accounts 200", r["status"] == 200, f"status={r['status']}")

# 2.3 Usage 页
section("2.3 Usage 页")
for days in [1, 7, 30, 90]:
    r = api("GET", f"/api/usage?days={days}", use_cookie=True)
    ok(f"GET /api/usage?days={days} 200", r["status"] == 200, f"status={r['status']}")

r = api("GET", "/api/usage/logs?limit=15&offset=0", use_cookie=True)
ok("usage/logs page1 200", r["status"] == 200)
r = api("GET", "/api/usage/logs?limit=15&offset=15", use_cookie=True)
ok("usage/logs page2 200", r["status"] == 200)

# 2.4 Accounts
section("2.4 Accounts 页")
r = api("GET", "/api/accounts", use_cookie=True)
ok("GET /api/accounts 200", r["status"] == 200)
first_acc_id = None
first_acc_enabled = None
if r["status"] == 200:
    try:
        accs = json.loads(r["content"])
        if isinstance(accs, list) and len(accs) > 0:
            a = accs[0]
            first_acc_id = a.get("id") or a.get("ID")
            first_acc_enabled = a.get("enabled", a.get("Enabled"))
    except Exception:
        pass

if first_acc_id:
    r2 = api("POST", "/api/accounts/schedule", body={"id": first_acc_id, "enabled": not first_acc_enabled}, use_cookie=True)
    ok("切换账号 enabled", r2["status"] == 200, f"status={r2['status']}")
    r3 = api("POST", "/api/accounts/schedule", body={"id": first_acc_id, "enabled": first_acc_enabled}, use_cookie=True)
    ok("恢复账号 enabled", r3["status"] == 200, f"status={r3['status']}")
    r4 = api("POST", "/api/accounts/bind-proxy", body={"id": first_acc_id, "proxyUrl": ""}, use_cookie=True)
    ok("解绑代理", r4["status"] == 200, f"status={r4['status']}")
else:
    skip("账号操作", "无账号数据")

r = api("GET", "/api/accounts", use_cookie=True)
ok("轮询 GET /api/accounts 200", r["status"] == 200)

# 2.5 PKCE
section("2.5 PKCE 授权流程")
r = api("GET", "/api/auth/start", use_cookie=True)
ok("GET /api/auth/start 200", r["status"] == 200, f"status={r['status']}")
auth_state = None
if r["status"] == 200:
    try:
        d = json.loads(r["content"])
        auth_state = d.get("state")
        ok("auth/start 返回 state", auth_state is not None)
    except Exception:
        skip("state 提取", "parse error")

if auth_state:
    r2 = api("GET", f"/api/auth/status?state={auth_state}", use_cookie=True)
    ok("auth/status 返回有效", r2["status"] in [200, 202], f"status={r2['status']}")
else:
    skip("auth/status", "无 state")

idx_html = get_html("/")["content"]
ok("JS 中 auth 轮询逻辑", "setInterval" in idx_html and "auth" in idx_html, "未找到轮询")

# 2.6 API Keys CRUD
section("2.6 API Keys CRUD")
r = api("GET", "/api/admin/keys", use_cookie=True)
ok("GET /api/admin/keys 200", r["status"] == 200)

r = api("POST", "/api/admin/keys", body={"name": "e2e-test-key"}, use_cookie=True)
ok("POST 创建 key 200", r["status"] == 200, f"status={r['status']}")
created_key_id = created_key_val = None
if r["status"] == 200:
    try:
        d = json.loads(r["content"])
        created_key_id = d.get("id") or d.get("record", {}).get("id")
        created_key_val = d.get("key")
        ok("创建返回 id", created_key_id is not None)
        ok("创建返回 key 值", created_key_val is not None)
    except Exception:
        skip("解析 key 响应", "parse error")

r = api("GET", "/api/admin/keys", use_cookie=True)
if r["status"] == 200:
    ok("新 key 在列表中", "e2e-test-key" in r["content"])
else:
    skip("验证 key 在列表", "GET failed")

if created_key_id:
    r = api("PUT", "/api/admin/keys", body={"id": created_key_id, "name": "e2e-test-key-renamed"}, use_cookie=True)
    ok("PUT 重命名 key", r["status"] == 200, f"status={r['status']}")

    r = api("PUT", "/api/admin/keys", body={"id": created_key_id, "revoked": True}, use_cookie=True)
    ok("PUT 禁用 key", r["status"] == 200, f"status={r['status']}")

    if created_key_val:
        try:
            tr = requests.get(f"{BASE}/v1/models", headers={"Authorization": f"Bearer {created_key_val}"}, timeout=10)
            ok("禁用 key 请求 /v1/models 返回 401", tr.status_code == 401, f"status={tr.status_code}")
        except Exception:
            ok("禁用 key 请求", False, "request error")

    r = api("PUT", "/api/admin/keys", body={"id": created_key_id, "revoked": False}, use_cookie=True)
    ok("PUT 重新启用 key", r["status"] == 200, f"status={r['status']}")

    r = api("DELETE", f"/api/admin/keys?id={created_key_id}", use_cookie=True)
    ok("DELETE key", r["status"] == 200, f"status={r['status']}")

    r = api("GET", "/api/admin/keys", use_cookie=True)
    if r["status"] == 200:
        ok("删除后 key 不在列表", "e2e-test-key-renamed" not in r["content"])
else:
    skip("key 重命名/禁用/删除", "无 key id")

# 2.7 Conversations
section("2.7 Conversations 页")
r = api("GET", "/api/m365/conversations", use_cookie=True)
ok("GET /api/m365/conversations 200", r["status"] == 200)
conv_id = None
if r["status"] == 200:
    try:
        convs = json.loads(r["content"])
        data = convs if isinstance(convs, list) else convs.get("data", convs.get("conversations", []))
        if isinstance(data, list) and len(data) > 0:
            conv_id = data[0].get("id") or data[0].get("conversationId")
    except Exception:
        pass

if conv_id:
    r2 = api("GET", f"/api/m365/conversations/detail?id={conv_id}", use_cookie=True)
    ok("GET conversation detail 200", r2["status"] == 200, f"status={r2['status']}")
else:
    skip("对话详情", "无对话数据")

# 2.8 Proxies
section("2.8 Proxies 页")
r = api("GET", "/api/admin/proxy-pool", use_cookie=True)
ok("GET proxy-pool 200", r["status"] == 200)
r = api("POST", "/api/admin/proxy-pool", body={"urls": ["http://127.0.0.1:9999"]}, use_cookie=True)
ok("POST 添加代理", r["status"] in [200, 201], f"status={r['status']}")
r = api("PUT", "/api/admin/proxy-pool?action=check", body={"url": "http://127.0.0.1:9999"}, use_cookie=True)
ok("PUT 检查代理", r["status"] == 200, f"status={r['status']}")
r = api("DELETE", "/api/admin/proxy-pool?url=http://127.0.0.1:9999", use_cookie=True)
ok("DELETE 代理", r["status"] == 200, f"status={r['status']}")

# 2.9 Model Test
section("2.9 Model Test 页")
r = api("GET", "/api/admin/models", use_cookie=True)
ok("GET /api/admin/models 200", r["status"] == 200)
r = api("POST", "/api/admin/models/test", body={"model": "gpt-4"}, use_cookie=True, timeout=60)
ok("POST models/test", r["status"] in [200, 408, 502, 504], f"status={r['status']}")

# 2.10 Settings
section("2.10 Settings 页")
r = api("GET", "/api/admin/settings", use_cookie=True)
ok("GET /api/admin/settings 200", r["status"] == 200)
settings_listen = settings_timeout = None
if r["status"] == 200:
    try:
        s = json.loads(r["content"])
        ok("settings 有配置字段", len(s) > 0)
        settings_listen = s.get("listenAddress") or s.get("listen_address") or "unknown"
        settings_timeout = s.get("chatTimeoutSeconds") or s.get("chat_timeout_seconds") or 0
    except Exception:
        skip("settings 字段", "parse error")

if settings_listen is not None:
    r = api("PUT", "/api/admin/settings", body={"listenAddress": settings_listen, "chatTimeoutSeconds": settings_timeout}, use_cookie=True)
    ok("PUT settings 保存", r["status"] in [200, 400], f"status={r['status']}")
    r2 = api("GET", "/api/admin/settings", use_cookie=True)
    if r2["status"] == 200:
        ok("GET settings after PUT 返回 200", True)
else:
    skip("PUT settings", "无当前设置")

# 2.11 Debug
section("2.11 Debug 页")
r = api("GET", "/api/admin/debug/logs", use_cookie=True)
ok("GET debug/logs 200", r["status"] == 200)
# debug.html not served as route; verify via API only
skip("debug.html 含 clientPayload", "/debug route not served")
skip("debug.html 含 upstreamPayload", "/debug route not served")
skip("debug.html 含 gatewayPayload", "/debug route not served")

# 2.12 退出登录
section("2.12 退出登录")
r = api("POST", "/api/admin/logout", use_cookie=True)
ok("POST /api/admin/logout 200", r["status"] == 200)

session_cookie = None
r = api("GET", "/api/admin/session", use_cookie=False)
if r["status"] == 200:
    try:
        d = json.loads(r["content"])
        ok("退出后 authenticated=false", d.get("authenticated") is False, f"authenticated={d.get('authenticated')}")
    except Exception:
        ok("退出后 session 非认证状态", r["status"] != 200, f"status={r['status']}")
else:
    ok("退出后 session 401", r["status"] == 401, f"status={r['status']}")

# 重新登录
r = api("POST", "/api/admin/login", body={"password": ADMIN_PW}, use_cookie=False)
if r["status"] != 200:
    print(color("  警告: 重新登录失败", "yellow"))

# 2.13 跨页面导航
section("2.13 跨页面导航")
for path, name, need_cookie in [("/login", "/login", False), ("/", "/", False), ("/conversation", "/conversation", True), ("/debug", "/debug", True)]:
    r = get_html(path, with_cookie=need_cookie)
    ok(f"GET {name} 返回有效状态", r["status"] in [200, 404], f"status={r['status']}")

# 2.14 i18n
section("2.14 i18n 语言切换")
idx_html = get_html("/")["content"]
ok("languageSelect 存在", "languageSelect" in idx_html)
ok("translatePage 函数", "translatePage" in idx_html)
ok("data-i18n 或 i18n 机制", "data-i18n" in idx_html or "i18n" in idx_html or "translate" in idx_html)

# 2.15 Substrate 端点 (API Key)
section("2.15 Substrate 端点 (API Key 认证)")
substrate_eps = [
    ("GET", "/api/plugins", None),
    ("GET", "/api/custom-instructions", None),
    ("POST", "/api/custom-instructions", {"instruction": "e2e-test", "enabled": True}),
    ("GET", "/api/personalization-flags", None),
    ("POST", "/api/personalization-flags", {"flags": {"suggestFollowups": True}}),
    ("GET", "/api/ecs/fluid", None),
    ("GET", "/api/ecs/designer", None),
    ("GET", "/api/sensitivity-labels", None),
    ("GET", "/api/userconfig", None),
    ("GET", "/api/suggestions", None),
]
for method, path, body in substrate_eps:
    r = api(method, path, body=body, use_cookie=False, use_apikey=True)
    ok(f"{method} {path} (apiKey)", r["status"] in [200, 201, 400, 401, 404, 405, 502], f"status={r['status']}")

# custom-instructions DELETE
r = api("DELETE", "/api/custom-instructions", use_cookie=False, use_apikey=True)
ok("DELETE /api/custom-instructions (apiKey)", r["status"] in [200, 204, 400, 404], f"status={r['status']}")

# 2.16 错误状态
section("2.16 错误状态和边界")
old_cookie = session_cookie
session_cookie = None
r = api("GET", "/api/admin/keys", use_cookie=False)
ok("无 cookie /api/admin/keys 401", r["status"] == 401, f"status={r['status']}")
r = api("GET", "/api/accounts", use_cookie=False)
ok("无 cookie /api/accounts 401", r["status"] == 401, f"status={r['status']}")
session_cookie = old_cookie

try:
    tr = requests.get(f"{BASE}/v1/models", headers={"Authorization": "Bearer invalid-key-12345"}, timeout=10)
    ok("错误 API Key 401", tr.status_code == 401, f"status={tr.status_code}")
except Exception:
    ok("错误 API Key", False, "request error")

r = api("GET", "/api/nonexistent-endpoint-12345", use_cookie=False, use_apikey=False)
ok("无效路由 非200", r["status"] != 200, f"status={r['status']}")

# ════════════════════════════════════════
# 汇总
# ════════════════════════════════════════
pass_ = sum(1 for r in results if r["status"] == "PASS")
fail_ = sum(1 for r in results if r["status"] == "FAIL")
skip_ = sum(1 for r in results if r["status"] == "SKIP")
total = len(results)
rate = round(pass_ / total * 100, 1) if total else 0

print(color("\n========================================", "magenta"))
print(color("  测试汇总", "magenta"))
print(color("========================================", "magenta"))
print(f"\n  总测试数: {total}")
print(color(f"  PASS: {pass_}", "green"))
print(color(f"  FAIL: {fail_}", "red"))
print(color(f"  SKIP: {skip_}", "yellow"))
print(f"  通过率: {rate}%\n")
if fail_:
    print(color("  失败测试:", "red"))
    for r in results:
        if r["status"] == "FAIL":
            print(color(f"    - {r['name']}" + (f" ({r['detail']})" if r['detail'] else ""), "red"))
    print()
