import sys, os, asyncio, argparse, re

sys.stdout.reconfigure(encoding="utf-8")

from .. import __version__
from ..auth import TokenManager
from ..client import M365Client
from ..models import MODELS, TENANT_ID, USER_OID, CLIENT_ID, SCOPE
from ..tools import provider, builtin, ToolCallDetector  # noqa: F401 (registers tools)
from ..tools.detector import detect_tool_intent, extract_tool_args
from ..scripts.plugin_loader import load_user_tools
from ..cookie_store import CookieStore
from .api import ConversationAPI

load_user_tools()


BASE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
RT_FILE = os.path.join(BASE_DIR, "data", "tokens", "rt_90day.txt")
CACHE_FILE = os.path.join(BASE_DIR, "data", "tokens", "token_cache.json")

MAX_TOOL_ROUNDS = 3


async def _tool_loop(client, text, message_history, tone, gpt_override, conversation_id=None,
                     enable_image_gen=False):
    tool_prompt = provider.get_tool_prompt()

    msgs = list(message_history)
    content = (tool_prompt + "\n\n" + text) if tool_prompt else text
    msgs.append({"role": "user", "content": content})

    for _round in range(MAX_TOOL_ROUNDS):
        resp_text, _, _ = await client.chat_conversation(msgs, tone, gpt_override, conversation_id,
                                                         enable_image_gen=enable_image_gen)

        detected = ToolCallDetector.detect(resp_text)
        if not detected:
            msgs.append({"role": "assistant", "content": resp_text})
            message_history[:] = [m for m in msgs if m["role"] in ("user", "assistant")]
            return resp_text

        tool_name, tool_args = detected
        print(f"  [tool] {tool_name} -> executing...", file=sys.stderr)
        try:
            result_str = str(provider.execute_tool(tool_name, tool_args))
        except Exception as e:
            result_str = f"Error: {e}"
        print(f"  [tool] {tool_name} -> {result_str[:120]}", file=sys.stderr)

        msgs.append({"role": "assistant", "content": resp_text})
        msgs.append({"role": "tool", "content": result_str, "name": tool_name})

    message_history[:] = [m for m in msgs if m["role"] in ("user", "assistant")]
    return resp_text


def execute_and_prepare(text):
    name = detect_tool_intent(text)
    if not name:
        return text, None
    tool = provider.tools.get(name)
    if not tool:
        return text, None
    args = extract_tool_args(text, name)
    try:
        result = tool.execute(**args)
        result_str = str(result)
    except Exception as e:
        result_str = f"Error: {e}"
    augmented = (
        f"[Tool Result: {name} returned '{result_str}']\n\n"
        f"User question: {text}"
    )
    return augmented, (name, result_str)


def main():
    parser = argparse.ArgumentParser(description=f"M365 Copilot v{__version__}")
    parser.add_argument("prompt", nargs="?", help="提问内容")
    parser.add_argument("--model", default="auto", choices=list(MODELS.keys()), help="模型")
    parser.add_argument("--reasoning", action="store_true", help="深度思考")
    parser.add_argument("-i", "--interactive", action="store_true", help="交互模式")
    parser.add_argument("--no-stream", action="store_true", help="禁用流式")
    parser.add_argument("--no-tools", action="store_true", help="禁用工具检测")
    parser.add_argument("--setup", action="store_true", help="首次设置 (刷新 Token)")
    parser.add_argument("--setup-cookies", action="store_true", help="提取浏览器 Cookies (需要关闭 Edge)")
    parser.add_argument("--list-models", action="store_true", help="列出模型")
    parser.add_argument("--list-tools", action="store_true", help="列出工具")
    parser.add_argument("--create-conv", action="store_true", help="创建新对话")
    parser.add_argument("--delete-conv", type=str, help="删除对话 (ID)")
    parser.add_argument("--delete-all", action="store_true", help="删除所有测试对话")
    parser.add_argument("--image", action="store_true", help="启用图像生成")
    parser.add_argument("--auto-delete", action="store_true", help="每条消息后自动删除对话")
    args = parser.parse_args()

    if args.list_models:
        print("可用模型:")
        for k, v in MODELS.items():
            desc = v["tone"]
            if v["override"]:
                desc += f" ({v['override']})"
            print(f"  {k:12s} - {desc}")
        return

    if args.list_tools:
        print("已注册工具:")
        for t in provider.get_tools():
            print(f"  {t['name']}: {t['description']}")
        return

    os.makedirs(os.path.dirname(RT_FILE), exist_ok=True)
    tm = TokenManager(TENANT_ID, CLIENT_ID, SCOPE, RT_FILE, CACHE_FILE)

    api = ConversationAPI(tm)

    if args.create_conv:
        conv_id = api.create()
        if conv_id:
            print(f"Created: {conv_id}")
        else:
            print("无法创建对话: REST API 对当前账号不可用 (Web UI 已封锁)")
            print("提示: 首次使用请运行 --setup-cookies 提取浏览器会话")
            print("提示: SignalR WebSocket 会自动创建对话, 无需手动 create")
        return

    if args.delete_conv:
        api.delete(args.delete_conv)
        print(f"Deleted: {args.delete_conv}")
        return

    if args.delete_all:
        if not CookieStore().has_cookies():
            print("请先运行 --setup-cookies 提取浏览器会话 cookies")
            print("或手动在浏览器中删除历史对话")
            return
        convs = api.list()
        if not convs:
            print("没有找到可删除的对话")
            return
        print(f"找到 {len(convs)} 个对话, 正在删除...")
        deleted = 0
        for c in convs:
            cid = c['conversationId']
            name = c.get('chatName', '?')
            api.delete(cid)
            deleted += 1
            print(f"  [{deleted}/{len(convs)}] {name[:30]} ({cid[:12]}...)")
        print(f"已尝试删除 {deleted} 个对话")
        print("注意: 因 Web UI 封锁限制, 删除可能未实际生效。")
        print("建议在浏览器中打开 M365 Copilot 手动验证。")
        return

    client = M365Client(tm)

    if args.setup:
        from ..scripts.setup_wizard import main as setup_main
        setup_main()
        return

    if args.setup_cookies:
        print("正在启动浏览器提取 M365 Cookies...")
        print("请确保 Edge 已关闭。")
        cs = CookieStore()
        cookies = CookieStore.extract_from_browser()
        cs.save(cookies)
        print(f"已保存 {len(cookies)} 个 cookies 到 data/tokens/m365_cookies.json")
        return

    if not TENANT_ID or not USER_OID:
        print("错误: M365_TENANT_ID 和 M365_USER_OID 未配置")
        print()
        print("获取方法:")
        print("  1. 在浏览器登录 https://m365.cloud.microsoft")
        print("  2. F12 → Console, 粘贴运行:")
        print("     let k=Object.keys(localStorage).find(k=>k.startsWith('msal.')&&k.includes('|'));let p=k.split('|')[1].split('.');console.log('OID:',p[0]);console.log('TENANT:',p[1]);")
        print()
        print("或运行: m365-copilot-setup")
        print()
        print("然后写入 .env 文件:")
        print("  M365_TENANT_ID=<your-tenant-id>")
        print("  M365_USER_OID=<your-user-oid>")
        print()
        print("或运行一键配置: python -m m365_copilot.scripts.setup")
        return

    if not args.prompt and not args.interactive:
        if not os.path.exists(RT_FILE):
            print(f"首次使用, 需要先认证: python -m m365_copilot --setup")
            return
        parser.print_help()
        return

    if not os.path.exists(RT_FILE):
        print(f"首次使用, 需要先认证: python -m m365_copilot --setup")
        return

    cfg = MODELS[args.model]
    tone = "Reasoning" if args.reasoning else cfg["tone"]
    gpt_override = cfg["override"]
    stream = not args.no_stream
    enable_image_gen = args.image

    async def run():
        if args.interactive:
            tools_count = len(provider.get_tools())
            print(f"M365 Copilot v{__version__} (交互模式)")
            print(f"模型: {args.model}{', 工具: ' + str(tools_count) if not args.no_tools else ''}")
            print("退出: Ctrl+C")
            message_history = []
            conv_id = None
            try:
                if not args.auto_delete:
                    conv_id = api.create()
                    if conv_id:
                        print(f"对话 ID: {conv_id[:12]}... (多轮上下文已启用)")
                    else:
                        print("提示: 无法创建持久对话，多轮上下文可能不连续")
                while True:
                    try:
                        text = input("\n> ")
                        if not text:
                            continue
                        try:
                            if args.no_tools:
                                if stream:
                                    print()
                                    await client.chat_stream(text, tone, gpt_override, conversation_id=conv_id,
                                                             enable_image_gen=enable_image_gen)
                                else:
                                    result = await client.chat(text, tone, gpt_override, conversation_id=conv_id,
                                                               enable_image_gen=enable_image_gen)
                                    if result:
                                        print(result)
                                print()
                            else:
                                print()
                                result = await _tool_loop(client, text, message_history, tone, gpt_override,
                                                          conversation_id=conv_id, enable_image_gen=enable_image_gen)
                                print(result)
                                print()
                        finally:
                            if conv_id and args.auto_delete:
                                try: api.delete(conv_id)
                                except: pass
                    except (KeyboardInterrupt, EOFError):
                        print("\n再见！")
                        break
            finally:
                await client.close()
        else:
            text = args.prompt or ""
            if not text:
                return
            conv_id = api.create() if args.auto_delete else None
            try:
                if args.no_tools:
                    final = text
                else:
                    final, info = execute_and_prepare(text)
                    if info:
                        print(f"  [tool] {info[0]} -> {info[1][:120]}", file=sys.stderr)
                if stream:
                    await client.chat_stream(final, tone, gpt_override, conversation_id=conv_id,
                                             enable_image_gen=enable_image_gen)
                else:
                    result = await client.chat(final, tone, gpt_override, conversation_id=conv_id,
                                               enable_image_gen=enable_image_gen)
                    if result:
                        print(result)
                print()
            finally:
                if conv_id:
                    try: api.delete(conv_id)
                    except: pass
            await client.close()

    asyncio.run(run())
