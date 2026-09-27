import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

type Proxy = {
  url: string;
  failures: number;
  cooldownUntil: string;
  lastCheck: string;
  latencyMs: number;
  lastError: string;
  health: "" | "reachable" | "unreachable" | "upstream_error" | "cooldown";
};

type Push = (m: string, k?: "success" | "error" | "info") => void;

function healthInfo(p: Proxy): { cls: string; label: string } {
  const inCooldown = p.cooldownUntil && new Date(p.cooldownUntil).getTime() > Date.now();
  if (inCooldown) return { cls: "cooldown", label: "Cooling down" };
  switch (p.health) {
    case "reachable": return { cls: "online", label: "Healthy" };
    case "unreachable": return { cls: "offline", label: "Unreachable" };
    case "upstream_error": return { cls: "warn", label: "Upstream error" };
    default: return { cls: "offline", label: "Not checked" };
  }
}

export function ProxiesPage({ push }: { push: Push }) {
  const [proxies, setProxies] = useState<Proxy[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const d = await api("/api/admin/proxy-pool");
      setProxies(d.proxies ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => { load(); }, [load]);

  const add = async () => {
    if (!input.trim()) {
      push(t("Enter at least one proxy URL"), "error");
      return;
    }
    setBusy(true);
    try {
      const d = await api("/api/admin/proxy-pool", { method: "POST", body: JSON.stringify({ urls: input.split(/\r?\n/).map((s) => s.trim()).filter(Boolean) }) });
      push(`${t("Added")}: ${d.added ?? 0}`, "success");
      setInput("");
      setProxies(d.proxies ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setBusy(false);
    }
  };

  const checkAll = async () => {
    setBusy(true);
    try {
      const d = await api("/api/admin/proxy-pool?action=check", { method: "PUT" });
      setProxies(d.proxies ?? []);
      push(t("Connectivity checked"), "success");
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setBusy(false);
    }
  };

  const remove = async (url: string) => {
    if (!confirm(`${t("Delete")} ${url}?`)) return;
    try {
      const d = await api(`/api/admin/proxy-pool?url=${encodeURIComponent(url)}`, { method: "DELETE" });
      setProxies(d.proxies ?? []);
      push(t("Deleted"), "success");
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  return (
    <div>
      <div className="card">
        <div className="card-head">{t("Add proxies")}</div>
        <div className="card-body">
          <textarea
            className="form-input"
            style={{ minHeight: 96, fontFamily: "ui-monospace, monospace", fontSize: 12, resize: "vertical" }}
            placeholder={"http://user:pass@host:port\nsocks5://host:port"}
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <div className="form-hint">{t("One proxy per line. http/https/socks5 supported.")}</div>
          <div style={{ display: "flex", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
            <button className="btn primary" disabled={busy} onClick={add}>{busy ? "…" : t("Add")}</button>
            <button className="btn" disabled={busy} onClick={checkAll}>{t("Test connectivity")}</button>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <span>{t("Proxy Pool")}</span>
          <span style={{ display: "inline-flex", gap: 8 }}>
            <span style={{ fontSize: 12, color: "var(--muted)", alignSelf: "center" }}>{proxies.length} {t("total")}</span>
            <button className="btn btn-sm" disabled={busy} onClick={checkAll}>{t("Check all")}</button>
          </span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("URL")}</th><th>{t("Status")}</th><th>{t("Latency")}</th><th>{t("Failures")}</th><th>{t("Cooldown until")}</th><th>{t("Actions")}</th></tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={6} className="empty">{t("Loading")}</td></tr>
              ) : proxies.length === 0 ? (
                <tr><td colSpan={6} className="empty">{t("No data")}</td></tr>
              ) : (
                proxies.map((p) => {
                  const h = healthInfo(p);
                  return (
                    <tr key={p.url}>
                      <td style={{ overflowWrap: "anywhere", whiteSpace: "normal", maxWidth: 260 }}>{p.url}</td>
                      <td>
                        <span className={`status ${h.cls}`}><span className="dot" />{t(h.label)}</span>
                        {p.lastError ? <div style={{ fontSize: 11, color: "var(--muted)", marginTop: 2 }}>{p.lastError}</div> : null}
                      </td>
                      <td>{p.latencyMs ? `${p.latencyMs} ms` : "-"}</td>
                      <td>{p.failures ?? 0}</td>
                      <td style={{ color: "var(--muted)", fontSize: 12 }}>
                        {p.cooldownUntil && new Date(p.cooldownUntil).getTime() > Date.now() ? new Date(p.cooldownUntil).toLocaleString() : "-"}
                      </td>
                      <td>
                        <button className="btn btn-sm danger" onClick={() => remove(p.url)}>{t("Delete")}</button>
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
