import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  LayoutDashboard, ChartLine, Users, KeyRound, MessageSquare,
  Globe, Settings, LogOut,
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
import { SettingsPage } from "./pages/Settings";

const NAV = [
  { id: "dashboard", icon: LayoutDashboard, label: "Dashboard" },
  { id: "usage", icon: ChartLine, label: "Usage" },
  { id: "accounts", icon: Users, label: "Accounts" },
  { id: "apikeys", icon: KeyRound, label: "API Keys" },
  { id: "conversations", icon: MessageSquare, label: "Conversations" },
  { id: "proxies", icon: Globe, label: "Proxy Pool" },
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
    <div className="app">
      <div className="sidebar">
        <div className="logo">
          <span className="logo-mark" aria-hidden="true"><span /><span /><span /><span /></span>
          <span className="logo-text">M365 Copilot2API</span>
        </div>
        <div className="nav-label">{t("Navigation")}</div>
        <nav className="nav" aria-label={t("Navigation")}>
          {NAV.map((n) => (
            <button key={n.id} className={page === n.id ? "active" : ""} onClick={() => setPage(n.id)} aria-current={page === n.id ? "page" : undefined} title={t(n.label)}>
              <n.icon size={16} strokeWidth={2} aria-hidden="true" />
              <span>{t(n.label)}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar-foot" id="sidebarVersion">dev</div>
      </div>

      <div className="main">
        <div className="topbar">
          <h1>{t(NAV.find((n) => n.id === page)!.label)}</h1>
          <div className="topbar-right">
            <span className="topbar-status" style={{ color: "var(--muted)" }}>
              <span className="status-dot" /> {t("Running")}
            </span>
            <select
              className="topbar-locale"
              value={locale}
              onChange={(e) => {
                const l = e.target.value as Locale;
                setLocale(l);
                setLocaleState(l);
              }}
              aria-label={t("Language")}
              style={{ background: "transparent", border: "1px solid var(--line)", borderRadius: 8, padding: "4px 8px", fontSize: 12, color: "var(--text)" }}
            >
              {LOCALES.map((l) => (<option key={l.id} value={l.id}>{l.label}</option>))}
            </select>
            <button className="btn btn-sm logout-btn" onClick={async () => {
              await api("/api/admin/logout", { method: "POST" }).catch(() => {});
              try { sessionStorage.clear(); } catch {}
              setAuthed(false);
            }}>
              <LogOut size={13} /> {t("Log out")}
            </button>
          </div>
        </div>

        <div className="content">
          <AnimatePresence mode="wait">
            <motion.div
              key={page}
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -4 }}
              transition={{ duration: 0.16, ease: [0.23, 1, 0.32, 1] }}
            >
              {page === "dashboard" && <DashboardPage push={push} nav={setPage} />}
              {page === "usage" && <UsagePage push={push} />}
              {page === "accounts" && <AccountsPage push={push} />}
              {page === "apikeys" && <ApiKeysPage push={push} />}
              {page === "conversations" && <ConversationsPage push={push} />}
              {page === "proxies" && <ProxiesPage push={push} />}
              {page === "settings" && <SettingsPage push={push} />}
            </motion.div>
          </AnimatePresence>
        </div>
      </div>

      <ToastHost toasts={toasts} dismiss={dismiss} />
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
  const pwdRef = useRef<HTMLInputElement>(null);
  void locale;
  return (
    <div className="app" style={{ display: "grid", placeItems: "center", minHeight: "100vh" }}>
      <motion.div
        initial={{ opacity: 0, y: 10, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ duration: 0.26, ease: [0.23, 1, 0.32, 1] }}
        className="card"
        style={{ width: "min(400px, calc(100vw - 32px))", padding: 28 }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10, justifyContent: "center", marginBottom: 6 }}>
          <span className="logo-mark" aria-hidden="true"><span /><span /><span /><span /></span>
          <span style={{ fontSize: 18, fontWeight: 700, letterSpacing: "-0.02em" }}>M365 Copilot2API</span>
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
            ref={pwdRef}
            type="password"
            value={pwd}
            onChange={(e) => setPwd(e.target.value)}
            onAnimationStart={(e) => {
              if (e.animationName === "onAutoFillStart") setPwd(pwdRef.current?.value ?? "");
            }}
            placeholder={t("Password")}
            autoFocus
            required
            autoComplete="current-password"
            className="form-input"
            style={{ marginBottom: 12 }}
          />
          <label style={{ display: "flex", alignItems: "center", gap: 7, fontSize: 12, color: "var(--muted)", marginBottom: 14 }}>
            <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            {t("Remember me for 30 days")}
          </label>
          <button type="submit" className="login-submit" disabled={busy || !pwd}>
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
      </motion.div>
      <ToastHost toasts={toasts} dismiss={dismiss} />
    </div>
  );
}
