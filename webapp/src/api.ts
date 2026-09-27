const AUTH_KEY = "m365_admin_session";

export async function api(path: string, opts: { method?: string; body?: string } = {}): Promise<any> {
  const res = await fetch(path, {
    method: opts.method ?? "GET",
    headers: opts.body ? { "Content-Type": "application/json" } : undefined,
    body: opts.body,
    credentials: "same-origin",
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg =
      data?.error?.message ??
      data?.message ??
      `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data;
}

export async function login(password: string): Promise<{ must_change_password?: boolean }> {
  const res = await fetch("/api/admin/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
    credentials: "same-origin",
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data?.error?.code === "admin_login_locked" ? "admin_login_locked" : data?.error?.message ?? "login failed");
  }
  try {
    if (localStorage.getItem("m365_remember") === "1") {
      sessionStorage.setItem(AUTH_KEY, "1");
    }
  } catch {}
  return data;
}

export function isLoggedIn(): boolean {
  try {
    return sessionStorage.getItem(AUTH_KEY) === "1";
  } catch {
    return false;
  }
}

export function setRemember(on: boolean) {
  try {
    if (on) localStorage.setItem("m365_remember", "1");
    else localStorage.removeItem("m365_remember");
  } catch {}
}

export function wantsRemember(): boolean {
  try {
    return localStorage.getItem("m365_remember") === "1";
  } catch {
    return false;
  }
}
