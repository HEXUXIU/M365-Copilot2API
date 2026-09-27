import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

type KeyRec = {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt?: string;
  revoked: boolean;
};

type Push = (m: string, k?: "success" | "error" | "info") => void;

export function ApiKeysPage({ push }: { push: Push }) {
  const [keys, setKeys] = useState<KeyRec[]>([]);
  const [loading, setLoading] = useState(true);
  const [name, setName] = useState("default");
  const [busy, setBusy] = useState(false);
  const [createdKey, setCreatedKey] = useState<string>("");
  const [editId, setEditId] = useState<string>("");
  const [editName, setEditName] = useState("");

  const load = useCallback(async () => {
    try {
      const d = await api("/api/admin/keys");
      setKeys(d.keys ?? []);
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setLoading(false);
    }
  }, [push]);

  useEffect(() => { load(); }, [load]);

  const create = async () => {
    setBusy(true);
    try {
      const d = await api("/api/admin/keys", { method: "POST", body: JSON.stringify({ name: name.trim() || "default" }) });
      setCreatedKey(d.key ?? "");
      push(t("Key created"), "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    } finally {
      setBusy(false);
    }
  };

  const copyCreated = async () => {
    try {
      await navigator.clipboard.writeText(createdKey);
      push(t("Copied"), "success");
    } catch {
      push(t("Copy failed"), "error");
    }
  };

  const saveEdit = async () => {
    try {
      await api("/api/admin/keys", { method: "PUT", body: JSON.stringify({ id: editId, name: editName }) });
      setEditId("");
      push(t("Key renamed"), "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const toggleRevoked = async (k: KeyRec) => {
    try {
      await api("/api/admin/keys", { method: "PUT", body: JSON.stringify({ id: k.id, revoked: !k.revoked }) });
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  const remove = async (k: KeyRec) => {
    if (!confirm(`${t("Delete")} "${k.name}"?`)) return;
    try {
      await api(`/api/admin/keys?id=${encodeURIComponent(k.id)}`, { method: "DELETE" });
      push(t("Deleted"), "success");
      await load();
    } catch (e: any) {
      push(String(e?.message ?? e), "error");
    }
  };

  return (
    <div>
      <div className="card">
        <div className="card-head">
          <span>{t("Create key")}</span>
        </div>
        <div className="card-body" style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
          <div className="form-group" style={{ flex: 1, minWidth: 180, marginBottom: 0 }}>
            <label className="form-label">{t("Name")}</label>
            <input className="form-input" value={name} onChange={(e) => setName(e.target.value)} placeholder="default" />
          </div>
          <button className="btn primary" disabled={busy} onClick={create}>{busy ? "…" : t("Create key")}</button>
        </div>
        {createdKey ? (
          <div className="card-body" style={{ borderTop: "1px solid var(--line)" }}>
            <div className="form-hint" style={{ marginBottom: 6 }}>{t("Copy this key now — it will not be shown again.")}</div>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <code style={{ flex: 1, minWidth: 200, padding: "9px 12px", background: "var(--bg)", borderRadius: "var(--radius-sm)", border: "1px solid var(--line)", overflowWrap: "anywhere", fontSize: 12 }}>{createdKey}</code>
              <button className="btn" onClick={copyCreated}>{t("Copy")}</button>
              <button className="btn" onClick={() => setCreatedKey("")}>{t("Done")}</button>
            </div>
          </div>
        ) : null}
      </div>

      <div className="card">
        <div className="card-head">
          <span>{t("API keys")}</span>
          <span style={{ fontSize: 12, color: "var(--muted)" }}>{keys.length} {t("total")}</span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("Name")}</th><th>{t("Prefix")}</th><th>{t("Created")}</th><th>{t("Status")}</th><th>{t("Last used")}</th><th>{t("Actions")}</th></tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={6} className="empty">{t("Loading")}</td></tr>
              ) : keys.length === 0 ? (
                <tr><td colSpan={6} className="empty">{t("No data")}</td></tr>
              ) : (
                keys.map((k) => (
                  <tr key={k.id}>
                    <td>
                      {editId === k.id ? (
                        <span style={{ display: "inline-flex", gap: 6 }}>
                          <input className="form-input" style={{ minHeight: 30, padding: "4px 8px", fontSize: 12 }} value={editName} onChange={(e) => setEditName(e.target.value)} />
                          <button className="btn btn-sm primary" onClick={saveEdit}>{t("Save")}</button>
                          <button className="btn btn-sm" onClick={() => setEditId("")}>{t("Cancel")}</button>
                        </span>
                      ) : (
                        <b>{k.name}</b>
                      )}
                    </td>
                    <td><code style={{ fontSize: 12 }}>{k.prefix}</code></td>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{k.createdAt ? new Date(k.createdAt).toLocaleString() : "-"}</td>
                    <td>
                      <span className={`status ${k.revoked ? "offline" : "online"}`}><span className="dot" />{t(k.revoked ? "Disabled" : "Active")}</span>
                    </td>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{k.lastUsedAt ? new Date(k.lastUsedAt).toLocaleString() : "-"}</td>
                    <td>
                      <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
                        <button className="btn btn-sm" onClick={() => { setEditId(k.id); setEditName(k.name); }}>{t("Edit")}</button>
                        <button className="btn btn-sm" onClick={() => toggleRevoked(k)}>{t(k.revoked ? "Enable" : "Disable")}</button>
                        <button className="btn btn-sm danger" onClick={() => remove(k)}>{t("Delete")}</button>
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
