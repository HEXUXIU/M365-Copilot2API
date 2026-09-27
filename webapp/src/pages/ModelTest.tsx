import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

type Model = { id: string; display_name?: string; slug?: string };
type Result = { status: "idle" | "testing" | "ok" | "fail"; latencyMs?: number; reply?: string; error?: string };

type Push = (m: string, k?: "success" | "error" | "info") => void;

export function ModelTestPage({ push }: { push: Push }) {
  const [models, setModels] = useState<Model[]>([]);
  const [results, setResults] = useState<Record<string, Result>>({});
  const [selection, setSelection] = useState<Set<string>>(new Set());
  const [testing, setTesting] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const d = await api("/api/admin/models");
      const list: Model[] = d.data ?? [];
      setModels(list);
      setSelection(new Set(list.map((m) => m.id)));
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => { load(); }, [load]);

  const allSelected = models.length > 0 && selection.size === models.length;
  const toggleOne = (id: string, on: boolean) => {
    setSelection((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  };
  const toggleAll = (on: boolean) => setSelection(on ? new Set(models.map((m) => m.id)) : new Set());

  const runQueue = useCallback(async (ids: string[]) => {
    if (ids.length === 0) {
      push(t("Select at least one model"), "error");
      return;
    }
    setTesting(true);
    const next: Record<string, Result> = { ...results };
    for (const id of ids) {
      next[id] = { status: "testing" };
      setResults({ ...next });
      const started = performance.now();
      try {
        const d = await api("/api/admin/models/test", { method: "POST", body: JSON.stringify({ model: id }) });
        next[id] = { status: "ok", latencyMs: d.latency_ms ?? Math.round(performance.now() - started), reply: d.reply ?? "" };
      } catch (e: any) {
        next[id] = { status: "fail", error: String(e?.message ?? e) };
      }
      setResults({ ...next });
    }
    setTesting(false);
  }, [push, results]);

  const runSelected = () => runQueue(models.filter((m) => selection.has(m.id)).map((m) => m.id));

  const cards = useMemo(() => {
    const vals = Object.values(results);
    return {
      ok: vals.filter((r) => r.status === "ok").length,
      fail: vals.filter((r) => r.status === "fail").length,
    };
  }, [results]);

  return (
    <div>
      <div className="card">
        <div className="card-head">
          <span>{t("Model Test")}</span>
          <span style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
            <span style={{ fontSize: 12, color: "var(--muted)" }}>
              {t("Healthy")}: {cards.ok} · {t("Failed")}: {cards.fail}
            </span>
            <button className="btn btn-sm" onClick={load} disabled={testing}>{t("Reload")}</button>
            <button className="btn btn-sm primary" onClick={runSelected} disabled={testing || selection.size === 0}>
              {testing ? t("Testing…") : `${t("Test selected")} (${selection.size})`}
            </button>
          </span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}>
                  <input type="checkbox" checked={allSelected} onChange={(e) => toggleAll(e.target.checked)} aria-label={t("Select all")} />
                </th>
                <th>{t("Model")}</th>
                <th>{t("Status")}</th>
                <th>{t("Latency")}</th>
                <th>{t("Reply")}</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={5} className="empty">{t("Loading")}</td></tr>
              ) : models.length === 0 ? (
                <tr><td colSpan={5} className="empty">{t("No data")}</td></tr>
              ) : (
                models.map((m) => {
                  const r = results[m.id] ?? { status: "idle" as const };
                  const cls = r.status === "ok" ? "online" : r.status === "fail" ? "offline" : r.status === "testing" ? "cooldown" : "offline";
                  const label = r.status === "ok" ? "Healthy" : r.status === "fail" ? "Failed" : r.status === "testing" ? "Testing…" : "Not tested";
                  return (
                    <tr key={m.id}>
                      <td>
                        <input type="checkbox" checked={selection.has(m.id)} onChange={(e) => toggleOne(m.id, e.target.checked)} aria-label={m.id} />
                      </td>
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
