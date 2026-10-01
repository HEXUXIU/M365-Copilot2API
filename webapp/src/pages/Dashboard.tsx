import { useCallback, useEffect, useState } from "react";
import { Flame, TrendingUp, Activity, PiggyBank, Plus, Key, Trash2 } from "lucide-react";
import { api } from "../api";
import { t } from "../i18n";
import { fmtTok } from "./Usage";

type Push = (m: string, k?: "success" | "error" | "info") => void;

export function DashboardPage({ push, nav }: { push: Push; nav: (id: "accounts" | "apikeys" | "conversations") => void }) {
  const [stats, setStats] = useState<any>(null);
  const [day, setDay] = useState<any>(null);
  const [all, setAll] = useState<any>(null);
  const [logs, setLogs] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const [s, d, a, l] = await Promise.all([
        api("/api/stats"),
        api("/api/usage?days=1"),
        api("/api/usage?days=365"),
        api("/api/usage/logs?limit=6&offset=0"),
      ]);
      setStats(s);
      setDay(d?.stats?.summary ?? {});
      setAll(a?.stats?.summary ?? {});
      setLogs(l?.logs ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => {
    load();
    const id = setInterval(load, 20000);
    return () => clearInterval(id);
  }, [load]);

  const cache = stats?.stats ?? {};
  const overview = [
    { Icon: Flame, label: t("Last 24 hours"), value: fmtTok(day?.tokens), sub: `${fmtTok(day?.requests)} ${t("requests")}` },
    { Icon: TrendingUp, label: t("All-time usage"), value: fmtTok(all?.tokens), sub: `${fmtTok(all?.requests)} ${t("requests")}` },
    { Icon: Activity, label: t("Request count"), value: fmtTok(all?.today_tokens), sub: `${fmtTok(all?.today_requests)} ${t("Today")}` },
    { Icon: PiggyBank, label: t("Cache hits"), value: fmtTok(cache?.tokens_saved), sub: `${t("Hit rate")} ${Math.round(cache?.hit_rate ?? 0)}%` },
  ];
  const cards = [
    { label: "Total sessions", value: stats?.conv_cache?.cached_conversations ?? 0, unit: "total" },
    { label: "Cache hits", value: fmtTok(cache?.cache_hits), unit: "hits" },
    { label: "Hit rate", value: Math.round(cache?.hit_rate ?? 0), unit: "%" },
    { label: "Tokens saved", value: fmtTok(cache?.tokens_saved), unit: "saved" },
  ];

  return (
    <div>
      <div className="usage-overview">
        {overview.map((o) => (
          <div className="usage-overview-item" key={o.label}>
            <div className="usage-overview-top"><o.Icon size={14} /> {o.label}</div>
            <span className="usage-overview-value">{o.value}</span>
            <span className="usage-overview-sub">{o.sub}</span>
          </div>
        ))}
      </div>

      <div className="stats">
        {cards.map((c) => (
          <div className="stat" key={c.label}>
            <span className="stat-label">{t(c.label)}</span>
            <span className="stat-value">{String(c.value)}<span className="stat-unit">{c.unit === "%" ? "%" : t(c.unit)}</span></span>
          </div>
        ))}
      </div>

      <div className="card">
        <div className="card-head"><span className="card-title">{t("Quick actions")}</span></div>
        <div className="card-body">
          <div className="quick-grid">
            <button type="button" className="quick-action" onClick={() => nav("accounts")}>
              <span className="quick-action-icon blue"><Plus size={18} aria-hidden="true" /></span>
              <span className="quick-action-text"><h4>{t("Add account")}</h4><p>{t("Sign in via Microsoft OAuth")}</p></span>
            </button>
            <button type="button" className="quick-action" onClick={() => nav("apikeys")}>
              <span className="quick-action-icon green"><Key size={18} aria-hidden="true" /></span>
              <span className="quick-action-text"><h4>{t("Create API key")}</h4><p>{t("Generate a key for API access")}</p></span>
            </button>
            <button type="button" className="quick-action" onClick={() => nav("conversations")}>
              <span className="quick-action-icon purple"><Trash2 size={18} aria-hidden="true" /></span>
              <span className="quick-action-text"><h4>{t("Clean conversations")}</h4><p>{t("Delete conversations you no longer need")}</p></span>
            </button>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><span className="card-title">{t("Recent requests")}</span></div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{t("Time")}</th><th>{t("Model")}</th><th>{t("Endpoint")}</th><th>{t("Tokens")}</th><th>{t("Status")}</th></tr></thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={5} className="empty">{t("Loading")}…</td></tr>
              ) : logs.length === 0 ? (
                <tr><td colSpan={5} className="empty">{t("No data")}</td></tr>
              ) : (
                logs.map((l, i) => (
                  <tr key={i}>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{l.time ? new Date(l.time).toLocaleTimeString() : "-"}</td>
                    <td>{l.model ?? "-"}</td>
                    <td>{String(l.endpoint ?? "-").replace(/^\/v1\//, "")}</td>
                    <td>{(l.input_tokens ?? 0) + (l.output_tokens ?? 0)}</td>
                    <td>{l.status ?? "-"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
