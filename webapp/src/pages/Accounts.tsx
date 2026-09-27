import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

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

export function AccountsPage({ push }: { push: (m: string, k?: "success" | "error" | "info") => void }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selection, setSelection] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(true);

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

  const allSelected = accounts.length > 0 && accounts.every((a) => selection.has(a.id));

  const toggleOne = (id: string, on: boolean) => {
    setSelection((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const toggleAll = (on: boolean) => {
    setSelection(on ? new Set(accounts.map((a) => a.id)) : new Set());
  };

  const batch = async (patch: Record<string, unknown>) => {
    const ids = [...selection];
    if (!ids.length) {
      push("Select at least one account", "error");
      return;
    }
    try {
      const r = await api("/api/accounts/batch", { method: "POST", body: JSON.stringify({ ids, ...patch }) });
      push(`${t("Settings saved")} (${r.updated})`, "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const applyPrompt = async () => {
    const v = prompt.trim();
    if (!v) {
      push("Enter a system prompt first", "error");
      return;
    }
    await batch({ systemPrompt: v });
  };

  const clearPrompt = async () => {
    if (!selection.size) {
      push("Select at least one account", "error");
      return;
    }
    if (!confirm("Clear custom system prompt for selected accounts?")) return;
    await batch({ systemPrompt: "" });
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
    if (!confirm("Delete this account?")) return;
    try {
      await api("/api/accounts/delete", { method: "POST", body: JSON.stringify({ id }) });
      push("Deleted", "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  return (
    <div>
      <div className="card">
        <div className="card-head">
          <span>{t("Authorized accounts")}</span>
          <span style={{ fontSize: 12, color: "var(--muted)" }}>
            {selection.size} {t("0 selected").replace("0 ", "")}
          </span>
        </div>
        <div className="card-body" style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center", borderBottom: "1px solid var(--line)" }}>
          <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12.5 }}>
            <input
              type="checkbox"
              checked={allSelected}
              onChange={(e) => toggleAll(e.target.checked)}
              aria-label={t("Select all")}
            />
            {t("Select all")}
          </label>
          <button className="btn btn-sm" onClick={() => batch({ scheduling: true })}>{t("Enable sched")}</button>
          <button className="btn btn-sm danger" onClick={() => batch({ scheduling: false })}>{t("Disable sched")}</button>
          <button className="btn btn-sm" onClick={() => batch({ webSearch: true })}>{t("Search on")}</button>
          <button className="btn btn-sm danger" onClick={() => batch({ webSearch: false })}>{t("Search off")}</button>
          <input
            className="form-input"
            style={{ flex: 1, minWidth: 160, minHeight: 30, padding: "5px 10px", fontSize: 12 }}
            placeholder={t("System prompt for selected…")}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
          <button className="btn btn-sm primary" onClick={applyPrompt}>{t("Apply prompt")}</button>
          <button className="btn btn-sm" onClick={clearPrompt}>{t("Clear prompt")}</button>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}></th>
                <th>{t("Account")}</th>
                <th>{t("Calls")}</th>
                <th>{t("Status")}</th>
                <th>{t("Scheduling")}</th>
                <th>{t("Actions")}</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={6} className="empty">{t("Loading")}</td></tr>
              ) : accounts.length === 0 ? (
                <tr><td colSpan={6} className="empty">{t("No matching accounts")}</td></tr>
              ) : (
                accounts.map((a) => {
                  const statusKey = a.status === "online" ? "Online" : a.status === "cooldown" ? "Cooldown" : "Offline";
                  const statusCls = a.status === "online" ? "online" : a.status === "cooldown" ? "cooldown" : "offline";
                  const flags = (
                    <div style={{ display: "flex", gap: 4, marginTop: 3, flexWrap: "wrap" }}>
                      {!a.webSearchEnabled && <span className="status warn"><span className="dot" />{t("No search")}</span>}
                      {a.systemPrompt ? <span className="status online"><span className="dot" />{t("Custom prompt")}</span> : null}
                    </div>
                  );
                  return (
                    <tr key={a.id}>
                      <td>
                        <input
                          type="checkbox"
                          checked={selection.has(a.id)}
                          onChange={(e) => toggleOne(a.id, e.target.checked)}
                          aria-label={a.email}
                        />
                      </td>
                      <td>
                        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                          <div style={{ width: 28, height: 28, borderRadius: "50%", background: "linear-gradient(135deg, var(--accent), var(--purple))", color: "#fff", display: "grid", placeItems: "center", fontSize: 11, fontWeight: 700, flexShrink: 0 }}>
                            {(a.displayName || a.email || "M")[0]}
                          </div>
                          <div style={{ minWidth: 0 }}>
                            <b>{a.displayName || a.email}</b>
                            <div style={{ fontSize: 11, color: "var(--muted)", overflowWrap: "anywhere" }}>{a.email}</div>
                            {flags}
                          </div>
                        </div>
                      </td>
                      <td style={{ fontSize: 12 }}>{a.callCount ?? 0}</td>
                      <td>
                        <span className={`status ${statusCls}`}><span className="dot" />{t(statusKey)}</span>
                        {a.cooldownUntil ? (
                          <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 2 }}>{new Date(a.cooldownUntil).toLocaleString()}</div>
                        ) : null}
                      </td>
                      <td>
                        <button
                          className={`btn btn-sm${a.scheduleEnabled ? "" : " danger"}`}
                          onClick={() => setSchedule(a.id, !a.scheduleEnabled)}
                        >
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
    </div>
  );
}
