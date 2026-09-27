import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

type Model = { id: string; display_name?: string; slug?: string };
type Result = { status: "idle" | "testing" | "ok" | "fail"; latencyMs?: number; reply?: string; error?: string };

type Push = (m: string, k?: "success" | "error" | "info") => void;

export function ModelTestPage({ push }: { push: Push }) {
  const [models, setModels] = useState<Model[]>([]);
  const [results, setResults] = useState<Record<string, Result>>({});
  const [testing, setTesting] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const d = await api("/api/admin/models");
      setModels(d.data ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => { load(); }, [load]);

  const testAll = async () => {
    setTesting(true);
    const next: Record<string, Result> = {};
    for (const m of models) {
      next[m.id] = { status: "testing" };
      setResults({ ...next });
      const started = performance.now();
      try {
        const d = await api("/api/admin/models/test", { method: "POST", body: JSON.stringify({ model: m.id }) });
        next[m.id] = { status: "ok", latencyMs: d.latency_ms ?? Math.round(performance.now() - started), reply: d.reply ?? "" };
      } catch (e: any) {
        next[m.id] = { status: "fail", error: String(e?.message ?? e) };
      }
      setResults({ ...next });
      await new Promise((r) => setTimeout(r, 250));
    }
    setTesting(false);
  };

  return (
    <div>
      <div className="card">
        <div className="card-head">
          <span>{t("Model Test")}</span>
          <span style={{ display: "inline-flex", gap: 8 }}>
            <span style={{ fontSize: 12, color: "var(--muted)", alignSelf: "center" }}>{models.length} {t("total")}</span>
            <button className="btn btn-sm" onClick={load} disabled={testing}>{t("Reload")}</button>
            <button className="btn btn-sm primary" onClick={testAll} disabled={testing || models.length === 0}>
              {testing ? "…" : t("Test all")}
            </button>
          </span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("Model")}</th><th>{t("Status")}</th><th>{t("Latency")}</th><th>{t("Reply")}</th></tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={4} className="empty">{t("Loading")}</td></tr>
              ) : models.length === 0 ? (
                <tr><td colSpan={4} className="empty">{t("No data")}</td></tr>
              ) : (
                models.map((m) => {
                  const r = results[m.id] ?? { status: "idle" as const };
                  const cls = r.status === "ok" ? "online" : r.status === "fail" ? "offline" : r.status === "testing" ? "cooldown" : "offline";
                  const label = r.status === "ok" ? "Healthy" : r.status === "fail" ? "Failed" : r.status === "testing" ? "Testing…" : "Not tested";
                  return (
                    <tr key={m.id}>
                      <td>
                        <b>{m.id}</b>
                        {m.display_name && m.display_name !== m.id ? <div style={{ fontSize: 11, color: "var(--muted)" }}>{m.display_name}</div> : null}
                      </td>
                      <td><span className={`status ${cls}`}><span className="dot" />{t(label)}</span></td>
                      <td>{r.latencyMs != null ? `${r.latencyMs} ms` : "-"}</td>
                      <td style={{ whiteSpace: "normal", maxWidth: 360, fontSize: 12 }}>
                        {r.status === "fail" ? <span style={{ color: "var(--red)" }}>{r.error}</span> : (r.reply ? (r.reply.length > 120 ? r.reply.slice(0, 120) + "…" : r.reply) : "-")}
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
