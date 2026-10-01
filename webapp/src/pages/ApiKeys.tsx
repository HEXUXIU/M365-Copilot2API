import { useCallback, useEffect, useState } from "react";
import { AnimatePresence } from "motion/react";
import { KeyRound, Copy, Check } from "lucide-react";
import { api } from "../api";
import { t } from "../i18n";
import { Modal } from "../components";

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
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("default");
  const [busy, setBusy] = useState(false);
  const [createdKey, setCreatedKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [editId, setEditId] = useState("");
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

  const openCreate = () => {
    setName("default");
    setCreatedKey("");
    setCopied(false);
    setCreateOpen(true);
  };

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
      setCopied(true);
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
      <div className="page-head">
        <div>
          <h2 className="page-title">{t("API Keys")}</h2>
          <p className="page-sub">{t("Manage access keys")}</p>
        </div>
        <button className="btn primary" onClick={openCreate}><KeyRound size={14} /> {t("Create key")}</button>
      </div>

      <div className="card">
        <div className="card-head">
          <span className="card-title">{t("Key list")}</span>
          <span style={{ fontSize: 11, color: "var(--muted)" }}>{t("The full key is shown only once after creation")}</span>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>{t("Name")}</th><th>{t("Prefix")}</th><th>{t("Created")}</th><th>{t("Status")}</th><th>{t("Last used")}</th><th style={{ textAlign: "right" }}>{t("Actions")}</th></tr>
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
                    <td><span className={`status ${k.revoked ? "offline" : "online"}`}><span className="dot" />{t(k.revoked ? "Disabled" : "Active")}</span></td>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{k.lastUsedAt ? new Date(k.lastUsedAt).toLocaleString() : "-"}</td>
                    <td style={{ textAlign: "right" }}>
                      <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap", justifyContent: "flex-end" }}>
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

      <AnimatePresence>
        {createOpen && (
          <Modal title={t("Create key")} subtitle={t("The full key is shown only once after creation")} onClose={() => setCreateOpen(false)}>
            {createdKey ? (
              <div>
                <div className="form-hint" style={{ marginBottom: 8 }}>{t("Copy this key now — it will not be shown again.")}</div>
                <div className="copy-field">
                  <input className="form-input" readOnly value={createdKey} onFocus={(e) => e.currentTarget.select()} />
                  <button className="btn primary" style={{ minWidth: 96 }} onClick={copyCreated}>
                    {copied ? <><Check size={14} /> {t("Copied")}</> : <><Copy size={14} /> {t("Copy")}</>}
                  </button>
                </div>
                <div className="modal-actions">
                  <button className="btn primary" onClick={() => setCreateOpen(false)}>{t("Done")}</button>
                </div>
              </div>
            ) : (
              <div>
                <div className="form-group">
                  <label className="form-label">{t("Name")}</label>
                  <input className="form-input" value={name} onChange={(e) => setName(e.target.value)} placeholder="default" autoFocus />
                </div>
                <div className="modal-actions">
                  <button className="btn" onClick={() => setCreateOpen(false)}>{t("Cancel")}</button>
                  <button className="btn primary" disabled={busy} onClick={create}>{busy ? "…" : t("Create key")}</button>
                </div>
              </div>
            )}
          </Modal>
        )}
      </AnimatePresence>
    </div>
  );
}
