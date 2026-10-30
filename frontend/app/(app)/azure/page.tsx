"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { azure, ApiError, type AzureDashboard } from "@/lib/api";
import { useRefOptions } from "@/lib/useRefs";
import { useAuth, canWrite } from "@/lib/auth";
import { Icon } from "@/components/Icon";

const money = (n: number) => (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const year = new Date().getFullYear();

interface Filters {
  from: string;
  to: string;
  account_id: string;
  subscription_id: string;
  azure_tenant_id: string;
  subscription_type: string;
  markup_pct: string;
}

// AzureDashboardPage: Azure usage and fees — actual (synced from Cost
// Management) and forecast (90-day run-rate) — with the fees invoiced / paid /
// due, filterable by account, subscription, Azure tenant and type, plus a
// what-if markup. Also starts new Azure fee agreements.
export default function AzureDashboardPage() {
  const { role } = useAuth();
  const [f, setF] = useState<Filters>({
    from: `${year}-01-01`,
    to: `${year}-12-31`,
    account_id: "",
    subscription_id: "",
    azure_tenant_id: "",
    subscription_type: "",
    markup_pct: "",
  });
  const [d, setD] = useState<AzureDashboard | null>(null);
  const [error, setError] = useState("");
  const [showNew, setShowNew] = useState(false);

  const accounts = useRefOptions("accounts");
  const subs = useRefOptions("azure_subscriptions");
  const tenants = useRefOptions("azure_tenants");

  useEffect(() => {
    const qs = new URLSearchParams(Object.entries(f).filter(([, v]) => v !== "")).toString();
    setError("");
    azure
      .dashboard(qs)
      .then(setD)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Failed to load the dashboard"));
  }, [f]);

  const set = (k: keyof Filters) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF((s) => ({ ...s, [k]: e.target.value }));

  return (
    <div className="max-w-6xl space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Icon name="cloud" className="h-6 w-6 text-brand-600" />
          <h1 className="text-2xl font-bold">Azure Dashboard</h1>
        </div>
        <div className="flex gap-2">
          {canWrite(role) && (
            <Link href="/azure/import" className="btn-secondary">
              <Icon name="upload" className="h-4 w-4" /> Import CSV
            </Link>
          )}
          <Link href="/m/azure_subscriptions?f_account_id=@empty" className="btn-secondary">
            <Icon name="link" className="h-4 w-4" /> Unmapped subscriptions
          </Link>
          {canWrite(role) && (
            <button type="button" className="btn-primary" onClick={() => setShowNew((v) => !v)}>
              <Icon name="file-plus" className="h-4 w-4" /> New Azure agreement
            </button>
          )}
        </div>
      </div>

      {showNew && <NewAgreement accounts={accounts} />}

      <div className="card flex flex-wrap items-end gap-3 p-4">
        <div>
          <label className="label">From</label>
          <input className="input" type="date" value={f.from} onChange={set("from")} />
        </div>
        <div>
          <label className="label">To</label>
          <input className="input" type="date" value={f.to} onChange={set("to")} />
        </div>
        <Picker label="Account" value={f.account_id} onChange={set("account_id")} options={accounts} />
        <Picker label="Subscription" value={f.subscription_id} onChange={set("subscription_id")} options={subs} />
        <Picker label="Azure tenant" value={f.azure_tenant_id} onChange={set("azure_tenant_id")} options={tenants} />
        <Picker
          label="Type"
          value={f.subscription_type}
          onChange={set("subscription_type")}
          options={["TAN", "TSS"].map((t) => ({ id: t, label: t }))}
        />
        <div>
          <label className="label">Markup %</label>
          <input className="input w-24" type="number" step="0.1" value={f.markup_pct} onChange={set("markup_pct")} />
        </div>
      </div>

      {error && <p className="text-sm text-red-600">{error}</p>}
      {!d && !error && <div className="text-slate-400">Loading…</div>}

      {d && (
        <>
          <p className="text-xs text-slate-400">
            Usage synced through <b>{d.synced_through || "—"}</b>. Later days are forecast from each subscription’s
            90-day daily average. Fees = usage × fee % (subscription override, else type rate).
          </p>
          {d.warnings && d.warnings.length > 0 && (
            <div className="card border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              {d.warnings.map((w) => (
                <div key={w} className="flex items-center gap-2">
                  <Icon name="triangle-alert" className="h-4 w-4" /> {w}
                </div>
              ))}
            </div>
          )}

          {d.totals.length === 0 && <p className="text-sm text-slate-400">No subscriptions match these filters.</p>}
          {d.totals.map((t) => {
            const inv = d.invoiced?.find((i) => i.currency === t.currency);
            return (
              <section key={t.currency} className="space-y-3">
                <div className="text-xs font-medium uppercase text-slate-400">{t.currency || "No currency"}</div>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
                  <Stat label="Azure usage (actual)" value={money(t.usage)} />
                  <Stat label="Fees (actual)" value={money(t.fee)} />
                  <Stat label="Usage forecast" value={money(t.forecast_usage)} muted />
                  <Stat label="Fee forecast" value={money(t.forecast_fee)} muted />
                  <Stat label={`Fees total${d.markup_pct ? ` + ${d.markup_pct}% markup` : ""}`} value={money(t.fee_with_markup)} />
                </div>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <Stat label="Fees invoiced" value={money(inv?.invoiced || 0)} href="/m/sales_invoices" />
                  <Stat label="Paid" value={money(inv?.paid || 0)} href="/m/sales_invoices?f_status=Paid" />
                  <Stat label="Due" value={money(inv?.due || 0)} tone="amber" href="/m/sales_invoices?fin_status=Draft,Issued" />
                  <Stat
                    label="Overdue"
                    value={money(inv?.overdue || 0)}
                    tone="amber"
                    href="/m/sales_invoices?f_status=Issued&fmax_due_date=@today-1"
                  />
                </div>
                <MonthlyFees rows={d.by_month.filter((m) => m.currency === t.currency)} />
              </section>
            );
          })}

          <Table
            title="By account"
            head={["Account", "Subs", "Usage", "Fees", "Usage forecast", "Fee forecast", "Fees total"]}
            rows={d.by_account.map((a) => [
              a.account_id ? (
                <Link key="a" href={`/m/accounts/${a.account_id}`} className="text-brand-600 hover:underline">
                  {a.account || "—"}
                </Link>
              ) : (
                <span key="a" className="text-amber-700">Unmapped</span>
              ),
              a.subscriptions,
              money(a.usage),
              money(a.fee),
              money(a.forecast_usage),
              money(a.forecast_fee),
              money(a.fee_with_markup),
            ])}
          />
          <Table
            title="By subscription"
            head={["Subscription", "Account", "Type", "Fee %", "Daily run-rate", "Usage", "Fees", "Fee forecast"]}
            rows={d.by_subscription.map((s) => [
              <Link key="s" href={`/m/azure_subscriptions/${s.id}`} className="text-brand-600 hover:underline">
                {s.name}
              </Link>,
              s.account || "—",
              s.type || "—",
              s.fee_pct != null ? `${s.fee_pct}%` : "type rate",
              money(s.daily_run_rate),
              money(s.usage),
              money(s.fee),
              money(s.forecast_fee),
            ])}
          />
          {d.agreement_forecast && d.agreement_forecast.length > 0 && (
            <Table
              title="Azure agreements in this period (by line basis)"
              head={["Basis", "Currency", "Quarters", "Fees (net)", "With markup"]}
              rows={d.agreement_forecast.map((a) => [a.basis || "—", a.currency, a.quarters, money(a.net), money(a.with_markup)])}
            />
          )}
        </>
      )}
    </div>
  );
}

// MonthlyFees: fees per month as bars — actual (solid) and forecast (light) of
// the same hue, stacked when a month is partly synced. Hover shows values.
function MonthlyFees({ rows }: { rows: AzureDashboard["by_month"] }) {
  if (rows.length === 0) return null;
  const max = Math.max(...rows.map((r) => r.fee + r.forecast_fee), 1);
  return (
    <div className="card p-4">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-sm font-medium text-slate-600">Fees by month</div>
        <div className="flex gap-4 text-xs text-slate-500">
          <span className="flex items-center gap-1">
            <span className="inline-block h-2.5 w-2.5 rounded-sm bg-brand-600" /> Actual
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block h-2.5 w-2.5 rounded-sm bg-brand-100 ring-1 ring-brand-500/40" /> Forecast
          </span>
        </div>
      </div>
      <div className="flex h-40 items-end gap-1 border-b border-slate-200">
        {rows.map((r) => {
          const tip = `${r.month}\nActual fees ${money(r.fee)} (usage ${money(r.usage)})\nForecast fees ${money(r.forecast_fee)} (usage ${money(r.forecast_usage)})`;
          return (
            <div key={r.month} className="group flex h-full flex-1 flex-col justify-end" title={tip}>
              {r.forecast_fee > 0 && (
                <div
                  className="mx-auto w-full max-w-10 rounded-t bg-brand-100 ring-1 ring-inset ring-brand-500/40 group-hover:bg-brand-50"
                  style={{ height: `${(r.forecast_fee / max) * 100}%` }}
                />
              )}
              {r.fee > 0 && (
                <div
                  className={`mx-auto w-full max-w-10 bg-brand-600 group-hover:bg-brand-700 ${r.forecast_fee > 0 ? "mt-0.5" : "rounded-t"}`}
                  style={{ height: `${(r.fee / max) * 100}%` }}
                />
              )}
            </div>
          );
        })}
      </div>
      <div className="mt-1 flex gap-1 text-[10px] text-slate-400">
        {rows.map((r) => (
          <div key={r.month} className="flex-1 text-center">
            {r.month.slice(5)}/{r.month.slice(2, 4)}
          </div>
        ))}
      </div>
    </div>
  );
}

// NewAgreement opens a quarterly Azure fee agreement for an account; past
// quarters are priced from real usage, future ones from the forecast.
function NewAgreement({ accounts }: { accounts: { id: string; label: string }[] }) {
  const router = useRouter();
  const [v, setV] = useState({ account_id: "", period_start: `${year}-01-01`, period_end: `${year}-12-31`, markup_pct: "", vat_pct: "", description: "" });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setV((s) => ({ ...s, [k]: e.target.value }));

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await azure.createAgreement({
        account_id: v.account_id,
        period_start: v.period_start,
        period_end: v.period_end,
        description: v.description || undefined,
        markup_pct: v.markup_pct === "" ? undefined : Number(v.markup_pct),
        vat_pct: v.vat_pct === "" ? undefined : Number(v.vat_pct),
      });
      router.push(`/m/${res.entity}/${res.data.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create the agreement");
      setBusy(false);
    }
  }

  return (
    <form onSubmit={create} className="card space-y-3 p-4">
      <div className="text-sm font-medium text-slate-600">New Azure fee agreement (quarterly)</div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Picker label="Account *" value={v.account_id} onChange={set("account_id")} options={accounts} />
        <div>
          <label className="label">Period start *</label>
          <input className="input" type="date" value={v.period_start} onChange={set("period_start")} />
        </div>
        <div>
          <label className="label">Period end *</label>
          <input className="input" type="date" value={v.period_end} onChange={set("period_end")} />
        </div>
        <div>
          <label className="label">Description</label>
          <input className="input" value={v.description} onChange={set("description")} placeholder="Azure fees …" />
        </div>
        <div>
          <label className="label">Markup %</label>
          <input className="input" type="number" step="0.1" value={v.markup_pct} onChange={set("markup_pct")} />
        </div>
        <div>
          <label className="label">VAT %</label>
          <input className="input" type="number" step="0.1" value={v.vat_pct} onChange={set("vat_pct")} />
        </div>
      </div>
      <p className="text-xs text-slate-400">
        One line and one installment per calendar quarter. Quarters already past use the real usage (handy to bill a
        period that was free); the rest use the forecast. It’s created as a Draft you can still edit.
      </p>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <button className="btn-primary" disabled={busy || !v.account_id}>
        {busy ? "Creating…" : "Create agreement"}
      </button>
    </form>
  );
}

function Picker({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void;
  options: { id: string; label: string }[];
}) {
  return (
    <div>
      <label className="label">{label}</label>
      <select className="select min-w-40" value={value} onChange={onChange}>
        <option value="">All</option>
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function Stat({ label, value, tone, muted, href }: { label: string; value: string; tone?: "amber"; muted?: boolean; href?: string }) {
  const body = (
    <div className={`card p-4 ${href ? "hover:bg-slate-50" : ""}`}>
      <div className="text-xs text-slate-500">{label}</div>
      <div
        className={`mt-1 text-xl font-semibold tabular-nums ${
          tone === "amber" ? "text-amber-600" : muted ? "text-slate-500" : "text-slate-800"
        }`}
      >
        {value}
      </div>
    </div>
  );
  return href ? <Link href={href}>{body}</Link> : body;
}

function Table({ title, head, rows }: { title: string; head: string[]; rows: React.ReactNode[][] }) {
  return (
    <div className="card overflow-x-auto">
      <div className="border-b border-slate-100 px-4 py-2 text-sm font-medium text-slate-600">{title}</div>
      <table className="min-w-full text-sm">
        <thead>
          <tr className="bg-slate-50 text-left text-slate-500">
            {head.map((h, i) => (
              <th key={h} className={`px-4 py-2 font-medium ${i > 0 ? "text-right" : ""}`}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={head.length} className="px-4 py-4 text-center text-slate-400">
                No data.
              </td>
            </tr>
          )}
          {rows.map((r, i) => (
            <tr key={i} className="border-t border-slate-100">
              {r.map((c, j) => (
                <td key={j} className={`px-4 py-2 ${j > 0 ? "text-right tabular-nums" : ""}`}>
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
