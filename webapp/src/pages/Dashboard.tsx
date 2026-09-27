import { useEffect, useState } from "react";
import { api } from "../api";
import { t } from "../i18n";

export function DashboardPage({ push }: { push: (m: string, k?: "success" | "error" | "info") => void }) {
  const [stats, setStats] = useState<any>(null);
  const [accounts, setAccounts] = useState<any[]>([]);
  useEffect(() => {
    api("/api/health").then(setStats).catch(() => {});
    api("/api/accounts").then((d) => setAccounts(d.accounts ?? [])).catch(() => {});
  }, []);
  const online = accounts.filter((a) => a.status === "online").length;
  const cards = [
    { label: "Accounts", value: `${online}/${accounts.length}` },
    { label: "Account concurrency", value: stats?.accountConcurrency?.limit ?? "—" },
    { label: "Chat", value: stats?.chat ?? "—" },
  ];
  return (
    <div>
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: 12, marginBottom: 16 }}>
        {cards.map((c) => (
          <div key={c.label} className="card">
            <div className="card-body">
              <div style={{ fontSize: 11, color: "var(--muted)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em" }}>{t(c.label)}</div>
              <div style={{ fontSize: 24, fontWeight: 700, marginTop: 4, letterSpacing: "-0.02em" }}>{String(c.value)}</div>
            </div>
          </div>
        ))}
      </div>
      <div className="card">
        <div className="card-head">{t("Accounts")}</div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{t("Account")}</th><th>{t("Status")}</th><th>{t("Updated")}</th></tr></thead>
            <tbody>
              {accounts.length === 0 ? (
                <tr><td colSpan={3} className="empty">{t("Loading")}</td></tr>
              ) : (
                accounts.slice(0, 8).map((a) => (
                  <tr key={a.id}>
                    <td><b>{a.displayName || a.email}</b><div style={{ fontSize: 11, color: "var(--muted)" }}>{a.email}</div></td>
                    <td><span className={`status ${a.status === "online" ? "online" : a.status === "cooldown" ? "cooldown" : "offline"}`}><span className="dot" />{t(a.status === "online" ? "Online" : a.status === "cooldown" ? "Cooldown" : "Offline")}</span></td>
                    <td style={{ color: "var(--muted)", fontSize: 12 }}>{a.updatedAt ? new Date(a.updatedAt).toLocaleString() : "-"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
      <button className="btn" style={{ marginTop: 14 }} onClick={() => push(t("Settings saved"), "info")}>{t("Reload")}</button>
    </div>
  );
}
