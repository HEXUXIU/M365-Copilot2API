import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  LayoutDashboard, ChartLine, Users, KeyRound, MessageSquare,
  Globe, FlaskConical, Settings, LogOut, CheckCircle2, AlertCircle,
} from "lucide-react";
import { api, isLoggedIn, login, wantsRemember, setRemember as setRememberPref } from "./api";
import { t, setLocale, getLocale, detectLocale, LOCALES, type Locale } from "./i18n";
import { ToastHost, useToasts } from "./components";
import { DashboardPage } from "./pages/Dashboard";
import { UsagePage } from "./pages/Usage";
import { AccountsPage } from "./pages/Accounts";
import { ApiKeysPage } from "./pages/ApiKeys";
import { ConversationsPage } from "./pages/Conversations";
import { ProxiesPage } from "./pages/Proxies";
import { ModelTestPage } from "./pages/ModelTest";
import { SettingsPage } from "./pages/Settings";

const NAV = [
  { id: "dashboard", icon: LayoutDashboard, label: "Dashboard" },
  { id: "usage", icon: ChartLine, label: "Usage" },
  { id: "accounts", icon: Users, label: "Accounts" },
  { id: "apikeys", icon: KeyRound, label: "API Keys" },
  { id: "conversations", icon: MessageSquare, label: "Conversations" },
  { id: "proxies", icon: Globe, label: "Proxy Pool" },
  { id: "modeltest", icon: FlaskConical, label: "Model Test" },
  { id: "settings", icon: Settings, label: "Settings" },
] as const;

type PageId = (typeof NAV)[number]["id"];

export default function App() {
  const [authed, setAuthed] = useState<boolean>(() => isLoggedIn());
  const [page, setPage] = useState<PageId>("dashboard");
  const { toasts, push, dismiss } = useToasts();
  const [locale, setLocaleState] = useState<Locale>(getLocale());

  useEffect(() => {
    const l = detectLocale();
    setLocale(l);
    setLocaleState(l);
  }, []);

  useEffect(() => {
    fetch("/api/admin/session")
      .then((r) => r.json())
      .then((d) => setAuthed(!!d.authenticated))
      .catch(() => setAuthed(false));
  }, []);

  if (!authed) {
    return (
      <LoginScreen
        onSuccess={() => setAuthed(true)}
        push={push}
        toasts={toasts}
        dismiss={dismiss}
        locale={locale}
        onLocale={(l) => {
          setLocale(l);
          setLocaleState(l);
        }}
      />
    );
  }

  return (
    <div style={{ display: "flex", minHeight: "100vh", maxWidth: "100%", overflowX: "hidden" }}>
      {/* Sidebar: horizontal scroll strip on mobile, fixed column on desktop */}
      <nav
        style={{
          width: "var(--sidebar-w)",
          flexShrink: 0,
          borderRight: "1px solid var(--line)",
          background: "var(--surface)",
          backdropFilter: "blur(24px)",
          padding: "22px 12px",
          position: "sticky",
          top: 0,
          height: "100vh",
          overflowY: "auto",
          display: "flex",
          flexDirection: "column",
          gap: 2,
        }}
        className="sidebar-nav"
      >
        <div style={{ fontWeight: 700, fontSize: 14, padding: "0 10px 18px", letterSpacing: "-0.01em" }}>
          M365 Copilot2API
        </div>
        {NAV.map((n) => {
          const active = page === n.id;
          return (
            <button
              key={n.id}
              onClick={() => setPage(n.id)}
              className="nav-item"
              aria-current={active ? "page" : undefined}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 10,
                border: 0,
                background: active ? "color-mix(in srgb, var(--accent) 12%, transparent)" : "transparent",
                color: "var(--text)",
                borderRadius: "var(--radius-sm)",
                padding: "9px 12px",
                cursor: "pointer",
                fontSize: 13,
                fontWeight: active ? 600 : 500,
                textAlign: "left",
                position: "relative",
                transition: "background 140ms ease-out",
              }}
            >
              <n.icon size={16} strokeWidth={2} style={{ flexShrink: 0, opacity: active ? 1 : 0.72 }} />
              <span>{t(n.label)}</span>
              {active && (
                <motion.span
                  layoutId="nav-pill"
                  transition={{ type: "spring", stiffness: 500, damping: 38 }}
                  style={{
                    position: "absolute",
                    left: 0,
                    top: 6,
                    bottom: 6,
                    width: 3,
                    borderRadius: 3,
                    background: "var(--accent)",
                  }}
                />
              )}
            </button>
          );
        })}
        <div style={{ marginTop: "auto", padding: "10px" }}>
          <button
            className="nav-item"
            onClick={async () => {
              await api("/api/admin/logout", { method: "POST" }).catch(() => {});
              try { sessionStorage.clear(); } catch {}
              setAuthed(false);
            }}
            style={{
              display: "flex", alignItems: "center", gap: 10, width: "100%",
              border: 0, background: "transparent", color: "var(--muted)",
              padding: "9px 12px", borderRadius: "var(--radius-sm)", cursor: "pointer", fontSize: 13,
            }}
          >
            <LogOut size={16} />
            {t("Log out")}
          </button>
        </div>
      </nav>

      <main style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        <header
          style={{
            height: 52,
            borderBottom: "1px solid var(--line)",
            background: "var(--surface)",
            backdropFilter: "blur(20px)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "0 22px",
            position: "sticky",
            top: 0,
            zIndex: 10,
          }}
        >
          <h1 style={{ margin: 0, fontSize: 15, fontWeight: 700 }}>{t(NAV.find((n) => n.id === page)!.label)}</h1>
          <div style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 12, color: "var(--muted)" }}>
            <select
              value={locale}
              onChange={(e) => {
                const l = e.target.value as Locale;
                setLocale(l);
                setLocaleState(l);
              }}
              aria-label={t("Language")}
              style={{
                background: "var(--surface-solid)",
                border: "1px solid var(--line)",
                borderRadius: "var(--radius-sm)",
                padding: "5px 8px",
                fontSize: 12,
                color: "var(--text)",
              }}
            >
              {LOCALES.map((l) => (
                <option key={l.id} value={l.id}>{l.label}</option>
              ))}
            </select>
            <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
              <CheckCircle2 size={13} color="var(--green)" />
              {t("Running")}
            </span>
          </div>
        </header>

        <div style={{ padding: 24, maxWidth: 1200, width: "100%", margin: "0 auto", flex: 1 }}>
          <AnimatePresence mode="wait">
            <motion.div
              key={page}
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -4 }}
              transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
            >
              {page === "dashboard" && <DashboardPage push={push} />}
              {page === "usage" && <UsagePage push={push} />}
              {page === "accounts" && <AccountsPage push={push} />}
              {page === "apikeys" && <ApiKeysPage push={push} />}
              {page === "conversations" && <ConversationsPage push={push} />}
              {page === "proxies" && <ProxiesPage push={push} />}
              {page === "modeltest" && <ModelTestPage push={push} />}
              {page === "settings" && <SettingsPage push={push} />}
            </motion.div>
          </AnimatePresence>
        </div>
      </main>

      <ToastHost toasts={toasts} dismiss={dismiss} />
      <style>{sharedStyles}</style>
    </div>
  );
}

function LoginScreen({
  onSuccess,
  push,
  toasts,
  dismiss,
  locale,
  onLocale,
}: {
  onSuccess: () => void;
  push: (m: string, k?: "success" | "error" | "info") => void;
  toasts: { id: number; message: string; kind: "success" | "error" | "info" }[];
  dismiss: (id: number) => void;
  locale: Locale;
  onLocale: (l: Locale) => void;
}) {
  const [pwd, setPwd] = useState("");
  const [busy, setBusy] = useState(false);
  const [remember, setRemember] = useState(wantsRemember());
  void locale;
  return (
    <div style={{ minHeight: "100vh", display: "grid", placeItems: "center", padding: 16 }}>
      <motion.div
        initial={{ opacity: 0, y: 10, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ duration: 0.26, ease: [0.22, 1, 0.36, 1] }}
        style={{
          width: "min(400px, 100%)",
          background: "var(--surface-solid)",
          border: "1px solid var(--line)",
          borderRadius: "var(--radius)",
          boxShadow: "var(--shadow)",
          padding: 28,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10, justifyContent: "center", marginBottom: 6 }}>
          <div style={{ width: 30, height: 30, borderRadius: 8, background: "var(--accent)", display: "grid", placeItems: "center", color: "#fff", fontWeight: 800, fontSize: 13 }}>M</div>
          <span style={{ fontSize: 19, fontWeight: 700, letterSpacing: "-0.02em" }}>M365 Copilot2API</span>
        </div>
        <p style={{ textAlign: "center", color: "var(--muted)", fontSize: 13, margin: "0 0 18px" }}>{t("Administrator Login")}</p>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              setRememberPref(remember);
              await login(pwd);
              onSuccess();
              push(t("Login successful"), "success");
            } catch (err: any) {
              push(err?.message === "admin_login_locked" ? t("Too many failed attempts, try again later") : String(err?.message ?? err), "error");
            } finally {
              setBusy(false);
            }
          }}
        >
          <input
            type="password"
            value={pwd}
            onChange={(e) => setPwd(e.target.value)}
            placeholder={t("Password")}
            autoFocus
            required
            style={{
              width: "100%",
              padding: "11px 13px",
              borderRadius: "var(--radius-sm)",
              border: "1px solid var(--line-strong)",
              background: "var(--bg)",
              marginBottom: 12,
              outline: "none",
              minHeight: 44,
            }}
          />
          <label style={{ display: "flex", alignItems: "center", gap: 7, fontSize: 12, color: "var(--muted)", marginBottom: 14 }}>
            <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            {t("Remember me for 30 days")}
          </label>
          <button
            className="btn primary"
            disabled={busy || !pwd}
            style={{ width: "100%", display: "inline-flex", alignItems: "center", justifyContent: "center", gap: 8 }}
          >
            {busy ? "…" : <><KeyRound size={15} /> {t("Sign in")}</>}
          </button>
        </form>
        <div style={{ marginTop: 14, display: "flex", justifyContent: "center" }}>
          <select
            value={locale}
            onChange={(e) => onLocale(e.target.value as Locale)}
            aria-label={t("Language")}
            style={{ background: "transparent", border: "1px solid var(--line)", borderRadius: 8, padding: "5px 8px", fontSize: 12, color: "var(--muted)" }}
          >
            {LOCALES.map((l) => (<option key={l.id} value={l.id}>{l.label}</option>))}
          </select>
        </div>
        <div style={{ display: "none" }}><AlertCircle size={1} /></div>
      </motion.div>
      <ToastHost toasts={toasts} dismiss={dismiss} />
      <style>{sharedStyles}</style>
    </div>
  );
}

const sharedStyles = `
.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  border: 1px solid var(--line-strong); background: var(--surface-solid); color: var(--text);
  border-radius: var(--radius-sm); padding: 8px 15px; cursor: pointer;
  font-size: 12.5px; font-weight: 600; min-height: 36px; white-space: nowrap;
  text-decoration: none;
  transition: transform 100ms ease-out, background 140ms ease-out, border-color 140ms ease-out;
  user-select: none;
}
.btn:hover { border-color: var(--accent); }
.btn:active { transform: scale(0.97); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; transform: none; }
.btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn.primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
.btn.danger { color: var(--red); }
.btn.danger:hover { border-color: var(--red); }
.btn-sm { padding: 5px 10px; min-height: 28px; font-size: 11.5px; }

.card {
  background: var(--surface-solid);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  box-shadow: var(--shadow-sm);
  overflow: hidden;
}
.card + .card { margin-top: 16px; }
.card-head {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  padding: 13px 18px; border-bottom: 1px solid var(--line); font-weight: 600; font-size: 13.5px;
  flex-wrap: wrap; min-width: 0;
}
.card-body { padding: 16px 18px; overflow-x: auto; -webkit-overflow-scrolling: touch; }

.table-wrap { overflow-x: auto; -webkit-overflow-scrolling: touch; overscroll-behavior-x: contain; }
.table { width: 100%; border-collapse: collapse; min-width: 640px; }
.table th {
  text-align: left; color: var(--muted); font-size: 11px; font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.05em;
  border-bottom: 1px solid var(--line); padding: 9px 16px;
}
.table td { padding: 11px 16px; border-bottom: 1px solid var(--line); font-size: 13px; white-space: nowrap; }
.table tr:last-child td { border-bottom: 0; }
.table tr:hover td { background: color-mix(in srgb, var(--accent) 4%, transparent); }

.form-group { margin-bottom: 14px; min-width: 0; }
.form-label { display: block; font-size: 12px; font-weight: 600; margin-bottom: 5px; color: var(--muted); }
.form-input {
  width: 100%; padding: 9px 12px; border-radius: var(--radius-sm);
  border: 1px solid var(--line-strong); background: var(--bg);
  outline: none; min-height: 40px;
  transition: border-color 140ms ease-out, box-shadow 140ms ease-out;
}
.form-input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 18%, transparent); }
.form-hint { font-size: 11.5px; color: var(--muted); margin: 5px 0 0; }

.status { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; font-weight: 600; }
.status i, .status .dot { width: 6px; height: 6px; border-radius: 50%; background: currentColor; display: inline-block; }
.status.online { color: var(--green); }
.status.offline { color: var(--muted); }
.status.cooldown { color: var(--orange); }
.status.warn { color: var(--orange); }

.empty { text-align: center; color: var(--muted); padding: 28px 12px; font-size: 12.5px; }

@media (max-width: 720px) {
  :root { --sidebar-w: 100vw; }
  .sidebar-nav {
    position: sticky; top: 0; height: auto; width: 100%;
    flex-direction: row; align-items: center; padding: 6px 8px;
    overflow-x: auto; overflow-y: hidden; border-right: 0;
    border-bottom: 1px solid var(--line); gap: 2px; z-index: 30;
    scrollbar-width: none;
  }
  .sidebar-nav::-webkit-scrollbar { display: none; }
  .sidebar-nav > div:first-child { display: none; }
  .sidebar-nav .nav-item span { display: none; }
  .sidebar-nav .nav-item { padding: 10px; min-width: 42px; justify-content: center; }
  .sidebar-nav > div:last-child { margin: 0 0 0 auto; padding: 0; }
  .sidebar-nav > div:last-child .nav-item span { display: none; }
  .sidebar-nav .nav-item[aria-current="page"]::after {
    content: ""; position: absolute; left: 10px; right: 10px; bottom: 2px;
    height: 3px; border-radius: 3px; background: var(--accent);
  }
}
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
}
`;
