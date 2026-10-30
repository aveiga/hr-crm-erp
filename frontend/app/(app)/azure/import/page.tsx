"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { azureImportCsv, ApiError, type AzureImportResult } from "@/lib/api";
import { useRefOptions } from "@/lib/useRefs";
import { Icon } from "@/components/Icon";

const money = (n: number) => (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

// AzureImportPage: the CSV fallback for Azure usage. Upload a Cost analysis
// download (Daily costs, grouped by subscription), review what's new and
// which stored days differ, choose which differences to overwrite, apply.
export default function AzureImportPage() {
  const tenants = useRefOptions("azure_tenants");
  const [tenantID, setTenantID] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<AzureImportResult | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [result, setResult] = useState<AzureImportResult | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  const tenant = tenantID || (tenants.length === 1 ? tenants[0].id : "");

  async function run(mode: "preview" | "apply") {
    if (!file) return;
    setBusy(mode);
    setError("");
    try {
      if (mode === "preview") {
        const pv = await azureImportCsv(file, { mode });
        setPreview(pv);
        setSelected(new Set());
        setResult(null);
      } else {
        setResult(await azureImportCsv(file, { mode, azure_tenant_id: tenant, overwrite: [...selected] }));
        setPreview(null);
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Import failed");
    } finally {
      setBusy("");
    }
  }

  const changes = preview?.changes || [];
  const allSelected = changes.length > 0 && selected.size === changes.length;
  const toggle = (key: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(key)) n.delete(key);
      else n.add(key);
      return n;
    });
  const delta = useMemo(() => changes.reduce((s, c) => (selected.has(c.key) ? s + c.incoming - c.existing : s), 0), [changes, selected]);

  return (
    <div className="max-w-5xl space-y-4">
      <div className="flex items-center gap-2">
        <Link href="/azure" className="text-slate-400 hover:text-slate-600">
          <Icon name="arrow-left" className="h-5 w-5" />
        </Link>
        <h1 className="text-2xl font-bold">Import Azure usage (CSV)</h1>
      </div>

      <div className="card space-y-4 p-4">
        <p className="text-sm text-slate-500">
          In the portal: <b>Cost Management → Cost analysis</b>, view <b>Daily costs</b>, cost type <b>Actual</b>,
          granularity <b>Daily</b>, group by <b>Subscription</b> (service optional), then <b>Download → CSV</b>.
          Subscriptions are matched by the ID in “Name(ID)”; unknown ones are created unmapped.
        </p>
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="label">Azure tenant *</label>
            <select className="select min-w-48" value={tenant} onChange={(e) => setTenantID(e.target.value)}>
              <option value="">—</option>
              {tenants.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.label}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="label">CSV file *</label>
            <label className="btn-secondary cursor-pointer">
              <Icon name="file-up" className="h-4 w-4" />
              {file ? file.name : "Choose file…"}
              <input
                className="sr-only"
                type="file"
                accept=".csv,text/csv"
                onChange={(e) => {
                  setFile(e.target.files?.[0] || null);
                  setPreview(null);
                  setResult(null);
                }}
              />
            </label>
          </div>
          <button type="button" className="btn-secondary" disabled={!file || !!busy} onClick={() => run("preview")}>
            <Icon name="search-check" className="h-4 w-4" /> {busy === "preview" ? "Checking…" : "Check file"}
          </button>
        </div>
        {tenants.length === 0 && (
          <p className="text-xs text-amber-700">
            No Azure tenant yet — <Link className="underline" href="/m/azure_tenants/new">create one</Link> (name and Directory ID are
            enough; no secret needed for imports).
          </p>
        )}
        {error && <p className="text-sm text-red-600">{error}</p>}
      </div>

      {preview && (
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-4">
            <Stat label="Rows in file" value={String(preview.rows)} />
            <Stat label="Period" value={`${preview.from} → ${preview.to}`} />
            <Stat label="New days (added)" value={String(preview.new_days)} />
            <Stat label="Unchanged days (skipped)" value={String(preview.unchanged_days)} />
          </div>
          {preview.warnings?.map((w) => (
            <div key={w} className="card flex items-center gap-2 border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              <Icon name="triangle-alert" className="h-4 w-4 shrink-0" /> {w}
            </div>
          ))}

          <div className="card overflow-x-auto">
            <div className="border-b border-slate-100 px-4 py-2 text-sm font-medium text-slate-600">Subscriptions in the file</div>
            <table className="min-w-full text-sm">
              <thead>
                <tr className="bg-slate-50 text-left text-slate-500">
                  <th className="px-4 py-2 font-medium">Subscription</th>
                  <th className="px-4 py-2 font-medium">ID</th>
                  <th className="px-4 py-2 text-right font-medium">Days</th>
                  <th className="px-4 py-2 text-right font-medium">Total</th>
                </tr>
              </thead>
              <tbody>
                {preview.subscriptions.map((s) => (
                  <tr key={s.key} className="border-t border-slate-100">
                    <td className="px-4 py-2">
                      {s.row_id ? (
                        <Link href={`/m/azure_subscriptions/${s.row_id}`} className="text-brand-600 hover:underline">
                          {s.name}
                        </Link>
                      ) : (
                        <>
                          {s.name} <span className="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800">new</span>
                        </>
                      )}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs text-slate-500">{s.subscription_id || "—"}</td>
                    <td className="px-4 py-2 text-right tabular-nums">{s.days}</td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {money(s.total)} {s.currency}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="card overflow-x-auto">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-4 py-2">
              <div className="text-sm font-medium text-slate-600">
                Changes — days already in the app with a different cost ({changes.length})
              </div>
              {changes.length > 0 && (
                <span className="text-xs text-slate-500">
                  {selected.size} selected to overwrite · net effect {delta >= 0 ? "+" : ""}
                  {money(delta)}
                </span>
              )}
            </div>
            {changes.length === 0 ? (
              <p className="px-4 py-4 text-sm text-slate-400">None — nothing stored would be changed.</p>
            ) : (
              <div className="max-h-[28rem] overflow-y-auto">
                <table className="min-w-full text-sm">
                  <thead className="sticky top-0">
                    <tr className="bg-slate-50 text-left text-slate-500">
                      <th className="px-4 py-2">
                        <input
                          type="checkbox"
                          checked={allSelected}
                          onChange={() => setSelected(allSelected ? new Set() : new Set(changes.map((c) => c.key)))}
                          title="Select all"
                        />
                      </th>
                      <th className="px-4 py-2 font-medium">Subscription</th>
                      <th className="px-4 py-2 font-medium">Date</th>
                      <th className="px-4 py-2 text-right font-medium">In the app</th>
                      <th className="px-4 py-2 text-right font-medium">In the file</th>
                      <th className="px-4 py-2 text-right font-medium">Difference</th>
                    </tr>
                  </thead>
                  <tbody>
                    {changes.map((c) => (
                      <tr key={c.key} className="cursor-pointer border-t border-slate-100 hover:bg-slate-50" onClick={() => toggle(c.key)}>
                        <td className="px-4 py-1.5">
                          <input type="checkbox" checked={selected.has(c.key)} onChange={() => toggle(c.key)} onClick={(e) => e.stopPropagation()} />
                        </td>
                        <td className="px-4 py-1.5">{c.subscription}</td>
                        <td className="px-4 py-1.5 tabular-nums">{c.date}</td>
                        <td className="px-4 py-1.5 text-right tabular-nums">
                          {money(c.existing)} {c.existing_currency}
                        </td>
                        <td className="px-4 py-1.5 text-right tabular-nums">
                          {money(c.incoming)} {c.currency}
                        </td>
                        <td className={`px-4 py-1.5 text-right tabular-nums ${c.incoming >= c.existing ? "text-slate-700" : "text-amber-700"}`}>
                          {c.incoming - c.existing >= 0 ? "+" : ""}
                          {money(c.incoming - c.existing)}
                          {c.existing_currency && c.currency && c.existing_currency !== c.currency && " (currency differs)"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <div className="card flex flex-wrap items-center justify-between gap-3 p-4">
            <p className="text-sm text-slate-600">
              Will add <b>{preview.new_days}</b> new day(s)
              {changes.length > 0 && (
                <>
                  , overwrite <b>{selected.size}</b> and keep <b>{changes.length - selected.size}</b> existing day(s)
                </>
              )}
              . Days in the app that aren’t in the file are left untouched.
            </p>
            <button
              type="button"
              className="btn-primary"
              disabled={!tenant || !!busy}
              title={tenant ? "" : "Pick the Azure tenant first"}
              onClick={() => {
                if (selected.size > 0 && !confirm(`Overwrite ${selected.size} day(s) already in the app with the file's values?`)) return;
                run("apply");
              }}
            >
              <Icon name="upload" className="h-4 w-4" /> {busy === "apply" ? "Importing…" : "Import"}
            </button>
          </div>
        </div>
      )}

      {result && (
        <div className="card space-y-2 p-4 text-sm">
          <div className="flex items-center gap-2 font-medium text-emerald-700">
            <Icon name="circle-check" className="h-4 w-4" /> Imported {result.from} → {result.to}
          </div>
          <ul className="list-disc pl-6 text-slate-600">
            <li>{result.added_days} new day(s) added</li>
            <li>{result.overwritten_days} day(s) overwritten</li>
            <li>{result.kept_days} changed day(s) kept as they were</li>
            <li>{result.unchanged_days} identical day(s) skipped</li>
            {!!result.new_subscriptions && (
              <li>
                {result.new_subscriptions} new subscription(s) —{" "}
                <Link className="text-brand-600 hover:underline" href="/m/azure_subscriptions?f_account_id=@empty">
                  map them to accounts and types
                </Link>
              </li>
            )}
            <li>Usage complete through {result.synced_through || "—"}</li>
          </ul>
          <Link href="/azure" className="text-brand-600 hover:underline">
            Open the Azure dashboard →
          </Link>
        </div>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="card p-4">
      <div className="text-xs text-slate-500">{label}</div>
      <div className="mt-1 text-lg font-semibold tabular-nums text-slate-800">{value}</div>
    </div>
  );
}
