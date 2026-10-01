import { useCallback, useEffect, useRef, useState } from "react";
import { AnimatePresence } from "motion/react";
import { FlaskConical, Play } from "lucide-react";
import { api } from "../api";
import { t } from "../i18n";
import { Modal } from "../components";

type Account = {
  id: string;
  email: string;
  displayName?: string;
  status: string;
  scheduleEnabled: boolean;
  webSearchEnabled: boolean;
  systemPrompt?: string;
  callCount?: number;
  cooldownUntil?: string;
  updatedAt?: string;
  boundProxy?: string;
};

type Push = (m: string, k?: "success" | "error" | "info") => void;

export function AccountsPage({ push }: { push: Push }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selection, setSelection] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<"all" | "online" | "cooldown" | "offline">("all");
  const [loading, setLoading] = useState(true);

  const [callback, setCallback] = useState("");
  const [authState, setAuthState] = useState("");
  const [authMsg, setAuthMsg] = useState<{ kind: "info" | "success" | "error"; text: string } | null>(null);
  const [authBusy, setAuthBusy] = useState(false);
  const popupRef = useRef<Window | null>(null);

  const [testOpen, setTestOpen] = useState(false);
  const [testModels, setTestModels] = useState<string[]>([]);
  const [testModel, setTestModel] = useState("");
  const [testPrompt, setTestPrompt] = useState('Say "OK" in one word.');
  const [testResult, setTestResult] = useState<{ status: "idle" | "busy" | "ok" | "fail"; latencyMs?: number; reply?: string; error?: string }>({ status: "idle" });

  const openTest = async () => {
    setTestResult({ status: "idle" });
    setTestOpen(true);
    if (testModels.length === 0) {
      try {
        const d = await api("/api/admin/models");
        const ids = (d.data ?? []).map((m: any) => m.id);
        setTestModels(ids);
        setTestModel((prev) => prev || ids[0] || "");
      } catch {}
    }
  };

  const runTest = async () => {
    if (!testModel) return;
    setTestResult({ status: "busy" });
    const started = performance.now();
    try {
      const d = await api("/api/admin/models/test", { method: "POST", body: JSON.stringify({ model: testModel, prompt: testPrompt }) });
      setTestResult({ status: "ok", latencyMs: d.latency_ms ?? Math.round(performance.now() - started), reply: d.reply ?? "" });
    } catch (e: any) {
      setTestResult({ status: "fail", error: String(e?.message ?? e) });
    }
  };

  const load = useCallback(async () => {
    try {
      const d = await api("/api/accounts");
      setAccounts(d.accounts ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);

  const startAuth = async () => {
    try {
      const d = await api("/api/auth/start");
      setAuthState(d.state ?? "");
      const popup = window.open(d.url, "pkce", "width=600,height=800");
      popupRef.current = popup;
      if (!popup) {
        setAuthMsg({ kind: "error", text: t("Popup blocked — allow popups and try again") });
        return;
      }
      setAuthMsg({ kind: "info", text: t("Sign in the popup, then paste the full callback URL below") });
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const submitCallback = async () => {
    const raw = callback.trim();
    if (!raw) {
      push(t("Paste the callback URL"), "error");
      return;
    }
    setAuthBusy(true);
    try {
      const url = raw.startsWith("http")
        ? `/api/auth/callback?url=${encodeURIComponent(raw)}`
        : `/api/auth/callback?${raw.replace(/^\?/, "")}`;
      await api(url);
      setAuthMsg({ kind: "success", text: t("Authorization succeeded") });
      setCallback("");
      await load();
    } catch (e: any) {
      setAuthMsg({ kind: "error", text: String(e?.message ?? e) });
    } finally {
      setAuthBusy(false);
    }
  };

  const visible = accounts.filter((a) => {
    if (filter !== "all" && a.status !== filter) return false;
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      return (a.email || "").toLowerCase().includes(q) || (a.displayName || "").toLowerCase().includes(q);
    }
    return true;
  });
  const allSelected = visible.length > 0 && visible.every((a) => selection.has(a.id));
  const toggleOne = (id: string, on: boolean) => setSelection((prev) => {
    const next = new Set(prev);
    if (on) next.add(id); else next.delete(id);
    return next;
  });
  const toggleAll = (on: boolean) => setSelection(on ? new Set(visible.map((a) => a.id)) : new Set());

  const batch = async (patch: Record<string, unknown>) => {
    const ids = [...selection];
    if (!ids.length) { push(t("Select at least one account"), "error"); return; }
    try {
      const r = await api("/api/accounts/batch", { method: "POST", body: JSON.stringify({ ids, ...patch }) });
      push(`${t("Settings saved")} (${r.updated})`, "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const setSchedule = async (id: string, enabled: boolean) => {
    try {
      await api("/api/accounts/schedule", { method: "POST", body: JSON.stringify({ id, enabled }) });
      setAccounts((prev) => prev.map((a) => (a.id === id ? { ...a, scheduleEnabled: enabled } : a)));
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const remove = async (id: string) => {
    if (!confirm(t("Delete this account?"))) return;
    try {
      await api("/api/accounts/delete", { method: "POST", body: JSON.stringify({ id }) });
      push(t("Deleted"), "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const msgClass = authMsg?.kind === "success" ? "highlight-box success" : authMsg?.kind === "error" ? "highlight-box warning" : "highlight-box";

  return (
    <div>
      <div className="page-head">
        <div>
          <h2 className="page-title">{t("Account management")}</h2>
          <p className="page-sub">{t("Authorize and manage Microsoft accounts")}</p>
        </div>
        <button className="btn primary" onClick={openTest}><FlaskConical size={14} /> {t("Test models")}</button>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-head"><span className="card-title">{t("Add account")}</span></div>
        <div className="card-body">
          <div className="onb-steps" style={{ marginBottom: 16 }}>
            {[
              { n: 1, title: t("Click Start authorization below"), sub: t("A popup opens the Microsoft sign-in page.") },
              { n: 2, title: t("Sign in, then copy the address-bar URL from the popup"), sub: t("The popup ends on a blank or error page — that is normal. Copy the whole URL, including the code and state parameters.") },
              { n: 3, title: t("Paste that long URL and confirm"), sub: t("Paste it into the callback field and click Confirm and add.") },
            ].map((s) => (
              <div className="onb-step" key={s.n}>
                <span className="onb-step-num">{s.n}</span>
                <span className="onb-step-text"><b>{s.title}</b><p>{s.sub}</p></span>
              </div>
            ))}
          </div>
          <button className="btn primary" style={{ width: "100%", marginBottom: 12 }} onClick={startAuth}>
            {t("Start authorization")}
          </button>
          <label className="form-label">{t("Callback URL")}</label>
          <div className="copy-field">
            <input
              className="form-input"
              value={callback}
              onChange={(e) => setCallback(e.target.value)}
              placeholder={t("Paste the full callback URL…")}
              autoComplete="off"
            />
            <button className="btn primary" style={{ minWidth: 96 }} disabled={authBusy} onClick={submitCallback}>
              {authBusy ? "…" : t("Confirm and add")}
            </button>
          </div>
          {authMsg ? (
            <div className={msgClass} style={{ marginTop: 12 }}>
              <div className="highlight-box-title">{authMsg.text}</div>
            </div>
          ) : null}
          <input type="hidden" value={authState} readOnly />
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <span className="card-title">{t("Authorized accounts")}</span>
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <input className="form-input" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("Search accounts…")} style={{ width: 180, padding: "6px 10px", fontSize: 12 }} />
            <div className="seg">
              {(["all", "online", "cooldown", "offline"] as const).map((f) => (
                <button key={f} className={filter === f ? "active" : ""} onClick={() => setFilter(f)}>
                  {t(f === "all" ? "All" : f === "online" ? "Online" : f === "cooldown" ? "Cooldown" : "Offline")}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="card-body" style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center", borderBottom: "1px solid var(--line)" }}>
          <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12.5 }}>
            <input type="checkbox" checked={allSelected} onChange={(e) => toggleAll(e.target.checked)} /> {t("Select all")}
          </label>
          <span className="form-hint" style={{ margin: 0 }}>{selection.size} {t("selected")}</span>
          <button className="btn btn-sm" onClick={() => batch({ scheduling: true })}>{t("Enable sched")}</button>
          <button className="btn btn-sm danger" onClick={() => batch({ scheduling: false })}>{t("Disable sched")}</button>
          <button className="btn btn-sm" onClick={() => batch({ webSearch: true })}>{t("Search on")}</button>
          <button className="btn btn-sm danger" onClick={() => batch({ webSearch: false })}>{t("Search off")}</button>
          <input className="form-input" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder={t("System prompt for selected…")} style={{ flex: 1, minWidth: 160, padding: "6px 10px", fontSize: 12 }} />
          <button className="btn btn-sm primary" onClick={() => prompt.trim() ? batch({ systemPrompt: prompt.trim() }) : push(t("Enter a system prompt first"), "error")}>{t("Apply prompt")}</button>
          <button className="btn btn-sm" onClick={() => { if (!selection.size) { push(t("Select at least one account"), "error"); return; } if (confirm(t("Clear custom system prompt for selected accounts?"))) batch({ systemPrompt: "" }); }}>{t("Clear prompt")}</button>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}></th>
                <th>{t("Account")}</th>
                <th>{t("Calls")}</th>
                <th>{t("Status")}</th>
                <th>{t("Updated")}</th>
                <th>{t("Scheduling")}</th>
                <th>{t("Actions")}</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={7} className="empty">{t("Loading")}</td></tr>
              ) : visible.length === 0 ? (
                <tr><td colSpan={7} className="empty">{t("No matching accounts")}</td></tr>
              ) : (
                visible.map((a) => {
                  const statusKey = a.status === "online" ? "Online" : a.status === "cooldown" ? "Cooldown" : "Offline";
                  return (
                    <tr key={a.id}>
                      <td><input type="checkbox" checked={selection.has(a.id)} onChange={(e) => toggleOne(a.id, e.target.checked)} aria-label={a.email} /></td>
                      <td>
                        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                          <div style={{ width: 28, height: 28, borderRadius: "50%", background: "linear-gradient(135deg, var(--accent), var(--purple))", color: "#fff", display: "grid", placeItems: "center", fontSize: 11, fontWeight: 700, flexShrink: 0 }}>
                            {(a.displayName || a.email || "M")[0]}
                          </div>
                          <div style={{ minWidth: 0 }}>
                            <b>{a.displayName || a.email}</b>
                            <div style={{ fontSize: 11, color: "var(--muted)", overflowWrap: "anywhere" }}>{a.email}</div>
                            <div style={{ display: "flex", gap: 4, marginTop: 3, flexWrap: "wrap" }}>
                              {!a.webSearchEnabled && <span className="status warn"><span className="dot" />{t("No search")}</span>}
                              {a.systemPrompt ? <span className="status online"><span className="dot" />{t("Custom prompt")}</span> : null}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td style={{ fontSize: 12 }}>{a.callCount ?? 0}</td>
                      <td>
                        <span className={`status ${a.status === "online" ? "online" : a.status === "cooldown" ? "cooldown" : "offline"}`}>
                          <span className="dot" />{t(statusKey)}
                        </span>
                        {a.cooldownUntil ? <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 2 }}>{new Date(a.cooldownUntil).toLocaleString()}</div> : null}
                      </td>
                      <td style={{ color: "var(--muted)", fontSize: 12 }}>{a.updatedAt ? new Date(a.updatedAt).toLocaleString() : "-"}</td>
                      <td>
                        <button className={`btn btn-sm${a.scheduleEnabled ? "" : " danger"}`} onClick={() => setSchedule(a.id, !a.scheduleEnabled)}>
                          {a.scheduleEnabled ? t("Enabled") : t("Disabled")}
                        </button>
                      </td>
                      <td>
                        <button className="btn btn-sm danger" onClick={() => remove(a.id)}>{t("Delete")}</button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      <AnimatePresence>
        {testOpen && (
          <Modal title={t("Test models")} subtitle={t("Send a minimal request to verify a model")} onClose={() => setTestOpen(false)}>
            <div className="form-group">
              <label className="form-label">{t("Model")}</label>
              <select className="form-input" value={testModel} onChange={(e) => setTestModel(e.target.value)}>
                {testModels.map((m) => (<option key={m} value={m}>{m}</option>))}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">{t("Prompt")}</label>
              <input className="form-input" value={testPrompt} onChange={(e) => setTestPrompt(e.target.value)} />
            </div>
            {testResult.status === "ok" ? (
              <div className="highlight-box success">
                <div className="highlight-box-title">{t("Healthy")} · {testResult.latencyMs} ms</div>
                <p>{testResult.reply}</p>
              </div>
            ) : testResult.status === "fail" ? (
              <div className="highlight-box warning">
                <div className="highlight-box-title">{t("Failed")}</div>
                <p>{testResult.error}</p>
              </div>
            ) : null}
            <div className="modal-actions">
              <button className="btn" onClick={() => setTestOpen(false)}>{t("Close")}</button>
              <button className="btn primary" disabled={testResult.status === "busy" || !testModel} onClick={runTest}>
                {testResult.status === "busy" ? t("Testing…") : <><Play size={14} /> {t("Test")}</>}
              </button>
            </div>
          </Modal>
        )}
      </AnimatePresence>
    </div>
  );
}
