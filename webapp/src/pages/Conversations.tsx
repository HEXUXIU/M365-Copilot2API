import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

type Conv = {
  conversationId: string;
  chatName?: string;
  accountEmail?: string;
  accountId?: string;
  messageCount?: number;
  createTimeUtc?: number;
  updateTimeUtc?: number;
  source?: string;
};

type Push = (m: string, k?: "success" | "error" | "info") => void;

function fmtTime(ms?: number): string {
  if (!ms) return "-";
  return new Date(ms).toLocaleString();
}

export function ConversationsPage({ push }: { push: Push }) {
  const [rows, setRows] = useState<Conv[]>([]);
  const [warning, setWarning] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const d = await api("/api/m365/conversations");
      setRows(d.data ?? []);
      setWarning(d.warning ?? "");
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => { load(); }, [load]);

  const remove = async (c: Conv) => {
    if (!confirm(`${t("Delete")} "${c.chatName || t("Untitled")}"?`)) return;
    try {
      await api("/api/m365/conversations/delete", { method: "POST", body: JSON.stringify({ conversation_id: c.conversationId }) });
      push(t("Deleted"), "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const cleanup = async () => {
    if (!confirm(t("Clean up old conversations on the server?"))) return;
    setBusy(true);
    try {
      const d = await api("/api/m365/conversations/cleanup", { method: "POST", body: JSON.stringify({}) });
      push(`${t("Cleaned")}: ${d.deleted ?? 0}`, "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      {warning ? (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card-body" style={{ color: "var(--orange)", fontSize: 12.5 }}>{warning}</div>
        </div>
      ) : null}
      <div className="card">
        <div className="card-head">
          <span>{t("Conversations")}</span>
          <span style={{ display: "inline-flex", gap: 8 }}>
            <span style={{ fontSize: 12, color: "var(--muted)", alignSelf: "center" }}>{rows.length} {t("total")}</span>
            <button className="btn btn-sm" onClick={load}>{t("Reload")}</button>
            <button className="btn btn-sm danger" disabled={busy} onClick={cleanup}>{busy ? "…" : t("Clean up all")}</button>
          </span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("Name")}</th><th>{t("Messages")}</th><th>{t("Account")}</th><th>{t("Last updated")}</th><th>{t("Actions")}</th></tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={5} className="empty">{t("Loading")}</td></tr>
              ) : rows.length === 0 ? (
                <tr><td colSpan={5} className="empty">{t("No data")}</td></tr>
              ) : (
                rows.map((c) => (
                  <tr key={c.conversationId}>
                    <td>
                      <b>{c.chatName || t("Untitled")}</b>
                      <div style={{ fontSize: 11, color: "var(--muted)", overflowWrap: "anywhere" }}>{c.conversationId}</div>
                    </td>
                    <td>{c.messageCount ?? 0}</td>
                    <td style={{ fontSize: 12 }}>{c.accountEmail || c.accountId || "-"}</td>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{fmtTime(c.updateTimeUtc || c.createTimeUtc)}</td>
                    <td>
                      <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
                        <a className="btn btn-sm" href={`/conversation?id=${encodeURIComponent(c.conversationId)}`} target="_blank" rel="noreferrer">{t("View")}</a>
                        <button className="btn btn-sm danger" onClick={() => remove(c)}>{t("Delete")}</button>
                      </span>
                    </td>
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
