import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

const RANGES = [1, 7, 30, 90];
const PAGE_SIZE = 15;

export function fmtTok(n: number | undefined): string {
  const v = n ?? 0;
  if (v >= 1e6) return (v / 1e6).toFixed(1) + "M";
  if (v >= 1e3) return (v / 1e3).toFixed(1) + "K";
  return String(v);
}

type Push = (m: string, k?: "success" | "error" | "info") => void;

function BarList({ title, rows, valueKey }: { title: string; rows: any[]; valueKey: "tokens" | "requests" }) {
  const top = [...(rows ?? [])].sort((a, b) => (b[valueKey] ?? 0) - (a[valueKey] ?? 0)).slice(0, 8);
  const max = Math.max(1, ...top.map((r) => r[valueKey] ?? 0));
  return (
    <div className="card">
      <div className="card-head">{t(title)}</div>
      <div className="card-body" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {top.length === 0 ? (
          <div className="empty">{t("No data")}</div>
        ) : (
          top.map((r, i) => {
            const label = r.name ?? r.endpoint ?? r.api_key_prefix ?? "-";
            const val = r[valueKey] ?? 0;
            return (
              <div key={i} style={{ fontSize: 12 }}>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 3 }}>
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: "70%" }}>{String(label)}</span>
                  <span style={{ color: "var(--muted)" }}>{fmtTok(val)}</span>
                </div>
                <div style={{ height: 5, borderRadius: 3, background: "var(--line)" }}>
                  <div style={{ width: `${(val / max) * 100}%`, height: "100%", borderRadius: 3, background: "var(--accent)" }} />
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function TrendChart({ trend }: { trend: any[] }) {
  const pts = trend ?? [];
  if (pts.length === 0) {
    return <div className="card"><div className="card-head">{t("Token usage trend")}</div><div className="card-body"><div className="empty">{t("No data")}</div></div></div>;
  }
  const max = Math.max(1, ...pts.map((p) => p.tokens ?? 0));
  const w = 100;
  const h = 30;
  const step = pts.length > 1 ? w / (pts.length - 1) : w;
  const path = pts.map((p, i) => `${(i * step).toFixed(2)},${(h - ((p.tokens ?? 0) / max) * h).toFixed(2)}`).join(" ");
  return (
    <div className="card">
      <div className="card-head">{t("Token usage trend")}</div>
      <div className="card-body">
        <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ width: "100%", height: 120, overflow: "visible" }}>
          <polyline points={path} fill="none" stroke="var(--accent)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
        </svg>
        <div style={{ display: "flex", justifyContent: "space-between", fontSize: 10.5, color: "var(--muted)", marginTop: 4 }}>
          <span>{pts[0]?.date}</span>
          <span>{pts[pts.length - 1]?.date}</span>
        </div>
      </div>
    </div>
  );
}

export function UsagePage({ push }: { push: Push }) {
  const [days, setDays] = useState(7);
  const [usage, setUsage] = useState<any>(null);
  const [logs, setLogs] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);

  const loadUsage = useCallback(async () => {
    try {
      setUsage(await api(`/api/usage?days=${days}`));
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  }, [days, push]);

  const loadLogs = useCallback(async () => {
    setLoading(true);
    try {
      const d = await api(`/api/usage/logs?limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`);
      setLogs(d.logs ?? []);
      setTotal(d.total ?? 0);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [page, push]);

  useEffect(() => { loadUsage(); }, [loadUsage]);
  useEffect(() => { loadLogs(); }, [loadLogs]);

  const s = usage?.stats?.summary ?? {};
  const stats = usage?.stats ?? {};
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  useEffect(() => {
    if (page > 0 && page >= totalPages) setPage(totalPages - 1);
  }, [page, totalPages]);

  const cards = [
    { label: "Total requests", value: fmtTok(s.requests), sub: `${t("Today")} +${fmtTok(s.today_requests)}` },
    { label: "Total tokens", value: fmtTok(s.tokens), sub: `${t("Input")} ${fmtTok(s.input)} · ${t("Output")} ${fmtTok(s.output)}` },
    { label: "Cached tokens", value: fmtTok(s.cache), sub: "" },
    { label: "Average latency", value: `${Math.round(s.avg_ms ?? 0)} ms`, sub: "" },
  ];

  return (
    <div>
      <div style={{ display: "flex", gap: 8, marginBottom: 16, flexWrap: "wrap" }}>
        {RANGES.map((d) => (
          <button
            key={d}
            className={`btn btn-sm${days === d ? " primary" : ""}`}
            onClick={() => { setDays(d); setPage(0); }}
          >
            {d === 1 ? t("24h") : `${d} ${t("days")}`}
          </button>
        ))}
        <button className="btn btn-sm" style={{ marginLeft: "auto" }} onClick={() => { loadUsage(); loadLogs(); }}>
          {t("Reload")}
        </button>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: 12, marginBottom: 16 }}>
        {cards.map((c) => (
          <div key={c.label} className="card">
            <div className="card-body">
              <div style={{ fontSize: 11, color: "var(--muted)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em" }}>{t(c.label)}</div>
              <div style={{ fontSize: 24, fontWeight: 700, marginTop: 4, letterSpacing: "-0.02em" }}>{c.value}</div>
              {c.sub ? <div style={{ fontSize: 11.5, color: "var(--muted)", marginTop: 3 }}>{c.sub}</div> : null}
            </div>
          </div>
        ))}
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))", gap: 12, marginBottom: 16 }}>
        <TrendChart trend={stats.trend ?? []} />
        <BarList title="Model distribution" rows={stats.models ?? []} valueKey="tokens" />
        <BarList title="Endpoint distribution" rows={stats.endpoints ?? []} valueKey="tokens" />
        <BarList title="API key usage" rows={stats.keys ?? []} valueKey="requests" />
      </div>

      <div className="card">
        <div className="card-head">
          <span>{t("Request details")}</span>
          <span style={{ fontSize: 12, color: "var(--muted)" }}>{t("Page")} {page + 1} / {totalPages} · {total} {t("total")}</span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("Time")}</th><th>{t("Key")}</th><th>{t("Model")}</th><th>{t("Endpoint")}</th><th>{t("Tokens")}</th><th>{t("Latency")}</th></tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={6} className="empty">{t("Loading")}</td></tr>
              ) : logs.length === 0 ? (
                <tr><td colSpan={6} className="empty">{t("No data")}</td></tr>
              ) : (
                logs.map((l, i) => (
                  <tr key={i}>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{l.time ? new Date(l.time).toLocaleString() : "-"}</td>
                    <td>{l.api_key_prefix ?? "-"}</td>
                    <td>{l.model ?? "-"}</td>
                    <td>{String(l.endpoint ?? "-").replace(/^\/v1\//, "")}</td>
                    <td style={{ fontSize: 12 }}>
                      {l.input_tokens ?? 0} / {l.output_tokens ?? 0} / {l.cache_tokens ?? 0}
                    </td>
                    <td>{Math.round(l.duration_ms ?? 0)} ms</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <div className="card-body" style={{ display: "flex", gap: 8, justifyContent: "flex-end", borderTop: "1px solid var(--line)" }}>
          <button className="btn btn-sm" disabled={page <= 0} onClick={() => setPage((p) => Math.max(0, p - 1))}>{t("Previous")}</button>
          <button className="btn btn-sm" disabled={page + 1 >= totalPages} onClick={() => setPage((p) => p + 1)}>{t("Next")}</button>
        </div>
      </div>
    </div>
  );
}
