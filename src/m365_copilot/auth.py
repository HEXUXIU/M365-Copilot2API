import json, urllib.request, urllib.parse, uuid, time, os, ssl, hashlib, base64

TOKEN_URL = "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token"
AUTHORIZE_URL = "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/authorize"


class TokenRefreshError(Exception):
    pass


class TokenManager:
    def __init__(self, tenant, client_id, scope, rt_file, cache_file):
        self.tenant = tenant
        self.client_id = client_id
        self.scope = scope
        self.rt_file = rt_file
        self.cache_file = cache_file
        self._token_url = TOKEN_URL.format(tenant=tenant)

    def _read_rt(self):
        with open(self.rt_file) as f:
            raw = f.read().strip()
        if not raw:
            raise TokenRefreshError("Refresh token file is empty")
        try:
            from .scripts.crypto import decrypt
            return decrypt(raw)
        except Exception:
            return raw

    def _write_rt(self, token):
        from .scripts.crypto import encrypt
        with open(self.rt_file, 'w') as f:
            f.write(encrypt(token))

    def refresh(self):
        if not os.path.exists(self.rt_file):
            raise TokenRefreshError(f"Refresh token not found: {self.rt_file}")
        rt = self._read_rt()
        data = urllib.parse.urlencode({
            'client_id': self.client_id, 'refresh_token': rt,
            'grant_type': 'refresh_token', 'scope': self.scope,
        }).encode()
        req = urllib.request.Request(self._token_url, data=data)
        req.add_header('Content-Type', 'application/x-www-form-urlencoded')
        req.add_header('Origin', 'https://m365.cloud.microsoft')
        req.add_header('User-Agent', 'Mozilla/5.0')
        try:
            with urllib.request.urlopen(req, context=ssl.create_default_context()) as resp:
                result = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            err = json.loads(e.read())
            raise TokenRefreshError(
                f"Refresh failed: {err.get('error')}: "
                f"{err.get('error_description', '')[:200]}"
            )
        if 'refresh_token' in result:
            self._write_rt(result['refresh_token'])
        cache = {
            'access_token': result['access_token'],
            'expires_at': time.time() + result.get('expires_in', 3600),
        }
        with open(self.cache_file, 'w') as f:
            json.dump(cache, f)
        return result['access_token']

    def get(self):
        try:
            with open(self.cache_file) as f:
                cache = json.load(f)
            if cache['expires_at'] > time.time() + 60:
                return cache['access_token']
        except Exception:
            pass
        return self.refresh()

    def setup(self):
        if self._try_wam():
            return True
        return self._try_auth_code()

    def _try_wam(self):
        try:
            from msal.broker import _signin_interactively
            print("正在打开 Windows 账户选择窗口...")
            result = _signin_interactively(
                f"https://login.microsoftonline.com/{self.tenant}",
                self.client_id,
                [self.scope, "openid", "profile", "offline_access"],
                parent_window_handle=None,
                prompt="select_account",
            )
            if "access_token" in result:
                self._save_tokens(result)
                return True
            err = result.get("error", "?")
            desc = result.get("error_description", "")
            print(f"  WAM 失败: {err}" + (f" {desc[:200]}" if desc else ""))
        except ImportError:
            print("  WAM broker 未安装 (pip install msal[broker])")
        except Exception as e:
            print(f"  WAM 不可用: {e}")
        return False

    def _try_auth_code(self):
        verifier = base64.urlsafe_b64encode(os.urandom(64)).rstrip(b'=').decode()
        challenge = base64.urlsafe_b64encode(
            hashlib.sha256(verifier.encode()).digest()
        ).rstrip(b'=').decode()
        params = {
            'client_id': self.client_id, 'response_type': 'code',
            'response_mode': 'fragment',
            'scope': f'{self.scope} openid profile offline_access',
            'redirect_uri': 'https://m365.cloud.microsoft',
            'state': uuid.uuid4().hex, 'nonce': uuid.uuid4().hex,
            'code_challenge': challenge, 'code_challenge_method': 'S256',
            'prompt': 'select_account',
        }
        url = AUTHORIZE_URL.format(tenant=self.tenant) + "?" + urllib.parse.urlencode(params)
        print("\n" + "=" * 60)
        print("浏览器认证:")
        print("1. 打开链接并登录")
        print("2. 从地址栏复制 code=... 的值")
        print("\n链接:")
        print(url)
        print("=" * 60)
        code = input("\n请输入 authorization code: ").strip()
        if not code:
            print("未输入 code")
            return False
        data = urllib.parse.urlencode({
            'client_id': self.client_id, 'code': code,
            'redirect_uri': 'https://m365.cloud.microsoft',
            'grant_type': 'authorization_code', 'code_verifier': verifier,
        }).encode()
        req = urllib.request.Request(self._token_url, data=data)
        req.add_header('Content-Type', 'application/x-www-form-urlencoded')
        req.add_header('Origin', 'https://m365.cloud.microsoft')
        try:
            with urllib.request.urlopen(req, context=ssl.create_default_context()) as resp:
                result = json.loads(resp.read())
            if 'refresh_token' in result:
                self._save_tokens(result)
                return True
            print(f"\n失败: {result.get('error')}: {result.get('error_description', '')[:200]}")
        except urllib.error.HTTPError as e:
            err = json.loads(e.read())
            print(f"\n失败: {err.get('error')}: {err.get('error_description', '')[:200]}")
        return False

    def _save_tokens(self, result):
        if 'refresh_token' in result:
            from .scripts.crypto import encrypt
            with open(self.rt_file, 'w') as f:
                f.write(encrypt(result['refresh_token']))
        cache = {
            'access_token': result['access_token'],
            'expires_at': time.time() + result.get('expires_in', 3600),
        }
        with open(self.cache_file, 'w') as f:
            json.dump(cache, f)
        print("认证成功！Refresh token 已保存")
