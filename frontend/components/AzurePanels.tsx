"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { azure, ApiError, type AzureCredential, type AzureQuarter } from "@/lib/api";
import { Icon } from "./Icon";

const money = (n: number) => (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const errMsg = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

// AzureTenantPanel manages an Azure tenant record's connection: the service
// principal's client secret (write-only), a connection test, and syncing
// subscriptions + usage (optionally back-filling from a date).
export function AzureTenantPanel({ id, editable, admin }: { id: string; editable: boolean; admin: boolean }) {
  const [cred, setCred] = useState<AzureCredential | null>(null);
  const [secret, setSecret] = useState("");
  const [expires, setExpires] = useState("");
  const [from, setFrom] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    azure.credential(id).then(setCred).catch(() => setCred(null));
  }, [id]);

  async function act(name: string, fn: () => Promise<string>) {
    setBusy(name);
    setErr("");
    setMsg("");
    try {
      setMsg(await fn());
    } catch (e) {
      setErr(errMsg(e, "Request failed"));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="card p-4 space-y-4">
      <div className="flex items-center gap-2 text-sm font-medium text-slate-600">
        <Icon name="key-round" className="h-4 w-4" /> Connection
        <span className="text-xs font-normal text-slate-400">
          {cred?.has_secret
            ? `client secret set${cred.expires_on ? ` · expires ${cred.expires_on}` : ""}`
            : "no client secret yet"}
        </span>
      </div>

      {admin && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="grow">
            <label className="label">Client secret (value)</label>
            <input
              className="input"
              type="password"
              autoComplete="off"
              value={secret}
              placeholder={cred?.has_secret ? "•••••••• (enter a new one to replace)" : ""}
              onChange={(e) => setSecret(e.target.value)}
            />
          </div>
          <div>
            <label className="label">Expires on</label>
            <input className="input" type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />
          </div>
          <button
            type="button"
            className="btn-secondary"
            disabled={!secret || !!busy}
            onClick={() =>
              act("secret", async () => {
                setCred(await azure.setCredential(id, { client_secret: secret, expires_on: expires || undefined }));
                setSecret("");
                return "Secret saved.";
              })
            }
          >
            Save secret
          </button>
        </div>
      )}

      {editable && (
        <div className="flex flex-wrap items-end gap-2">
          <button
            type="button"
            className="btn-secondary"
            disabled={!cred?.has_secret || !!busy}
            onClick={() =>
              act("test", async () => {
                const r = await azure.test(id);
                return `Connected — ${r.subscriptions.length} subscription(s) visible: ${r.subscriptions
                  .map((s) => s.displayName)
                  .join(", ")}`;
              })
            }
          >
            <Icon name="plug-zap" className="h-4 w-4" /> {busy === "test" ? "Testing…" : "Test connection"}
          </button>
          <div>
            <label className="label">Back-fill from (optional)</label>
            <input className="input" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </div>
          <button
            type="button"
            className="btn-primary"
            disabled={!cred?.has_secret || !!busy}
            onClick={() =>
              act("sync", async () => {
                const r = await azure.sync(id, { from: from || undefined });
                return `Sync started for ${r.from} – ${r.to}. Reload this page to see its status.`;
              })
            }
          >
            <Icon name="refresh-cw" className="h-4 w-4" /> Sync now
          </button>
        </div>
      )}
      <p className="text-xs text-slate-400">
        A daily sync refreshes the last two months automatically. Back-fill older history once (Cost Management keeps
        about 13 months).
      </p>
      {msg && <p className="text-sm text-emerald-700">{msg}</p>}
      {err && <p className="text-sm text-red-600">{err}</p>}
    </div>
  );
}

// AzureAgreementPanel lists an Azure fee agreement's quarters (amount and
// whether it's a forecast, actual usage or invoiced), with per-quarter invoice
// creation and a forecast refresh. onChanged reloads the record.
export function AzureAgreementPanel({ id, editable, onChanged }: { id: string; editable: boolean; onChanged: () => void }) {
  const router = useRouter();
  const [qs, setQs] = useState<AzureQuarter[]>([]);
  const [through, setThrough] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");

  const load = () =>
    azure
      .quarters(id)
      .then((r) => {
        setQs(r.quarters);
        setThrough(r.synced_through);
      })
      .catch((e) => setErr(errMsg(e, "Failed to load quarters")));

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function invoice(q: AzureQuarter) {
    setErr("");
    setBusy(q.period_start);
    try {
      let res;
      try {
        res = await azure.invoice(id, q.period_start);
      } catch (e) {
        if (!(e instanceof ApiError && e.code === "usage_incomplete") || !confirm(`${e.message}\n\nCreate the invoice anyway?`))
          throw e;
        res = await azure.invoice(id, q.period_start, true);
      }
      if (res.warnings?.length) alert("Invoice created with warnings:\n• " + res.warnings.join("\n• "));
      router.push(`/m/${res.entity}/${res.data.id}`);
    } catch (e) {
      setErr(errMsg(e, "Invoice failed"));
      setBusy("");
    }
  }

  async function refresh() {
    if (!confirm("Re-price every quarter that isn't invoiced yet from the latest usage and forecast? Manual changes to those lines are replaced."))
      return;
    setBusy("refresh");
    setErr("");
    try {
      await azure.refresh(id);
      await load();
      onChanged();
    } catch (e) {
      setErr(errMsg(e, "Refresh failed"));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="card p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-slate-600">
            <Icon name="cloud" className="h-4 w-4" /> Azure fees by quarter
          </div>
          <p className="text-xs text-slate-400">
            Usage synced through {through || "—"}. Past quarters use real usage; later ones the 90-day run-rate. An
            invoice replaces its quarter’s amount.
          </p>
        </div>
        {editable && (
          <button type="button" className="btn-secondary" onClick={refresh} disabled={!!busy}>
            <Icon name="refresh-cw" className="h-4 w-4" /> {busy === "refresh" ? "Refreshing…" : "Refresh forecast"}
          </button>
        )}
      </div>
      <table className="w-full text-sm">
        <thead className="text-left text-xs uppercase text-slate-400">
          <tr>
            <th className="py-1">Quarter</th>
            <th>Basis</th>
            <th className="text-right">Fees (net)</th>
            <th className="text-right">Installment</th>
            <th>Payment</th>
            <th>Invoice</th>
          </tr>
        </thead>
        <tbody>
          {qs.map((q) => (
            <tr key={q.period_start} className="border-t border-slate-100">
              <td className="py-1.5">
                {q.label}
                <span className="ml-1 text-xs text-slate-400">
                  {q.period_start} – {q.period_end}
                </span>
              </td>
              <td>
                <span className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600">{q.basis || "—"}</span>
              </td>
              <td className="text-right tabular-nums">{money(q.amount)}</td>
              <td className="text-right tabular-nums">{q.installment ? money(q.installment.amount) : "—"}</td>
              <td className="text-xs text-slate-500">{q.installment?.status || "—"}</td>
              <td>
                {q.invoice ? (
                  <Link href={`/m/sales_invoices/${q.invoice.ID}`} className="text-brand-600 hover:underline">
                    {q.invoice.InvoiceNumber || "Invoice"} · {q.invoice.Status}
                  </Link>
                ) : editable ? (
                  <button
                    type="button"
                    className="text-brand-600 hover:underline disabled:text-slate-300"
                    disabled={!!busy}
                    title={q.usage_complete ? "" : "Usage for this quarter isn't fully synced yet"}
                    onClick={() => invoice(q)}
                  >
                    {busy === q.period_start ? "Creating…" : q.ended ? "Create invoice" : "Invoice early"}
                  </button>
                ) : (
                  "—"
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {err && <p className="mt-2 text-sm text-red-600">{err}</p>}
    </div>
  );
}
