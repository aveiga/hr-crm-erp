"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, type InstallmentRow, type SalesReport } from "@/lib/api";
import { formatDate } from "@/lib/format";
import { Icon } from "@/components/Icon";

const money = (n: number) => (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

// list links into a module's list page with filter params (the same ones the
// saved views use), so every figure here can be drilled into.
const list = (entity: string, q = "") => `/m/${entity}${q ? `?${q}` : ""}`;

// ReportsPage: sales reporting across quotes, service agreements, their
// payment schedules and payments. Sections the user can't access are omitted
// by the server. Amounts are per currency where documents may differ.
export default function ReportsPage() {
  const [r, setR] = useState<SalesReport | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .salesReport()
      .then(setR)
      .catch(() => setError("Failed to load the report"));
  }, []);

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!r) return <div className="text-slate-400">Loading…</div>;

  const overdueTotal = (r.overdue_installments || []).reduce((s, i) => s + Number(i.outstanding), 0);
  const conv = r.quotes?.conversion;

  return (
    <div className="max-w-6xl space-y-8">
      <div className="flex items-center gap-2">
        <Icon name="chart-column" className="h-6 w-6 text-brand-600" />
        <h1 className="text-2xl font-bold">Reports</h1>
      </div>

      {r.agreements && (
        <section className="space-y-3">
          <h2 className="text-lg font-semibold">Service agreements</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {r.agreements.by_currency.map((c) => (
              <div key={c.currency} className="card p-4 sm:col-span-2 lg:col-span-4">
                <div className="mb-2 text-xs font-medium uppercase text-slate-400">
                  {c.currency || "No currency"} · {c.count} active agreements (excl. cancelled)
                </div>
                <div className="grid gap-3 sm:grid-cols-3">
                  <Stat label="Contracted" value={money(c.contracted)} />
                  <Stat label="Paid" value={money(c.paid)} />
                  <Stat label="Outstanding" value={money(c.outstanding)} tone="amber" />
                </div>
              </div>
            ))}
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <Table
              title="By payment status"
              head={["Payment status", "Agreements", "With overdue", "Outstanding"]}
              rows={r.agreements.by_payment_status.map((s) => [
                <Link key="l" className="text-brand-600 hover:underline" href={list("invoices", `f_payment_status=${encodeURIComponent(s.status)}`)}>
                  {s.status}
                </Link>,
                s.count,
                s.overdue ? (
                  <Link
                    key="o"
                    className="text-red-600 hover:underline"
                    href={list("invoices", `f_payment_status=${encodeURIComponent(s.status)}&fmax_next_due_date=@today-1`)}
                  >
                    {s.overdue}
                  </Link>
                ) : (
                  0
                ),
                money(s.outstanding),
              ])}
            />
            <Table
              title="By agreement status"
              head={["Status", "Agreements", "Contracted"]}
              rows={r.agreements.by_status.map((s) => [
                <Link key="l" className="text-brand-600 hover:underline" href={list("invoices", `f_status=${encodeURIComponent(s.status)}`)}>
                  {s.status || "—"}
                </Link>,
                s.count,
                money(s.contracted),
              ])}
            />
          </div>
        </section>
      )}

      {r.overdue_installments && (
        <section className="space-y-3">
          <div className="flex items-baseline justify-between">
            <h2 className="text-lg font-semibold">
              Overdue scheduled payments{" "}
              <span className="text-sm font-normal text-red-600">({money(overdueTotal)} outstanding)</span>
            </h2>
            <Link className="text-sm text-brand-600 hover:underline" href={list("invoices", "fin_payment_status=Not Paid,Partially Paid&fmax_next_due_date=@today-1")}>
              Overdue agreements →
            </Link>
          </div>
          <Installments rows={r.overdue_installments} empty="Nothing overdue." />
        </section>
      )}

      {r.upcoming_installments && (
        <section className="space-y-3">
          <h2 className="text-lg font-semibold">Due in the next 30 days</h2>
          <Installments rows={r.upcoming_installments} empty="Nothing due in the next 30 days." />
        </section>
      )}

      {r.quotes && conv && (
        <section className="space-y-3">
          <h2 className="text-lg font-semibold">Quotes</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Stat label="Quotes" value={String(conv.quotes)} href={list("quotes")} />
            <Stat
              label="Contracted (have an agreement)"
              value={`${conv.contracted}${conv.quotes ? ` · ${Math.round((conv.contracted / conv.quotes) * 100)}%` : ""}`}
              href={list("quotes", "fin_agreement_status=Draft,Sent,Signed,Active,Completed,Issued,Paid,Overdue,Cancelled")}
            />
            <Stat
              label="Accepted, no agreement yet"
              value={String(conv.accepted_not_contracted)}
              tone={conv.accepted_not_contracted ? "amber" : undefined}
              href={list("quotes", "f_status=Accepted&f_agreement_status=@empty")}
            />
            <Stat
              label="Open, past valid date"
              value={String(conv.open_expired)}
              tone={conv.open_expired ? "amber" : undefined}
              href={list("quotes", "fin_status=Draft,Sent&fmax_valid_until=@today-1")}
            />
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <Table
              title="By status"
              head={["Status", "Quotes", "Net", "Grand"]}
              rows={r.quotes.by_status.map((s) => [
                <Link key="l" className="text-brand-600 hover:underline" href={list("quotes", `f_status=${encodeURIComponent(s.status)}`)}>
                  {s.status || "—"}
                </Link>,
                s.count,
                money(s.net),
                money(s.grand),
              ])}
            />
            <Table
              title="Quoted per month (last 12)"
              head={["Month", "Quotes", "Net"]}
              rows={r.quotes.by_month.map((m) => [m.month, m.count, money(m.net)])}
            />
          </div>
        </section>
      )}

      {r.payments && (
        <section className="space-y-3">
          <div className="flex items-baseline justify-between">
            <h2 className="text-lg font-semibold">Payments</h2>
            {r.payments.unallocated.count > 0 && (
              <Link className="text-sm text-brand-600 hover:underline" href={list("payments", "f_schedule_id=@empty")}>
                {r.payments.unallocated.count} not linked to an installment ({money(r.payments.unallocated.amount)}) →
              </Link>
            )}
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <Table
              title="Received per month (last 12)"
              head={["Month", "Payments", "Amount"]}
              rows={r.payments.by_month.map((m) => [m.month, m.count, money(m.amount)])}
            />
            <Table
              title="By method"
              head={["Method", "Payments", "Amount"]}
              rows={r.payments.by_method.map((m) => [
                <Link key="l" className="text-brand-600 hover:underline" href={list("payments", `f_method=${encodeURIComponent(m.method)}`)}>
                  {m.method || "—"}
                </Link>,
                m.count,
                money(m.amount),
              ])}
            />
          </div>
        </section>
      )}
    </div>
  );
}

function Stat({ label, value, tone, href }: { label: string; value: string; tone?: "amber"; href?: string }) {
  const body = (
    <div className={`card p-4 ${href ? "hover:bg-slate-50" : ""}`}>
      <div className="text-xs text-slate-500">{label}</div>
      <div className={`mt-1 text-xl font-semibold tabular-nums ${tone === "amber" ? "text-amber-600" : "text-slate-800"}`}>{value}</div>
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

function Installments({ rows, empty }: { rows: InstallmentRow[]; empty: string }) {
  return (
    <div className="card overflow-x-auto">
      <table className="min-w-full text-sm">
        <thead>
          <tr className="bg-slate-50 text-left text-slate-500">
            <th className="px-4 py-2 font-medium">Due</th>
            <th className="px-4 py-2 font-medium">Service agreement</th>
            <th className="px-4 py-2 font-medium">Account</th>
            <th className="px-4 py-2 font-medium">Description</th>
            <th className="px-4 py-2 text-right font-medium">Amount</th>
            <th className="px-4 py-2 text-right font-medium">Paid</th>
            <th className="px-4 py-2 text-right font-medium">Outstanding</th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={7} className="px-4 py-4 text-center text-slate-400">
                {empty}
              </td>
            </tr>
          )}
          {rows.map((i) => (
            <tr key={i.id} className="border-t border-slate-100">
              <td className="px-4 py-2 whitespace-nowrap">{formatDate(i.due_date)}</td>
              <td className="px-4 py-2">
                <Link className="text-brand-600 hover:underline" href={`/m/invoices/${i.agreement_id}`}>
                  {i.agreement}
                </Link>
              </td>
              <td className="px-4 py-2">{i.account || "—"}</td>
              <td className="px-4 py-2 text-slate-500">{i.description || ""}</td>
              <td className="px-4 py-2 text-right tabular-nums">
                {money(i.amount)} <span className="text-xs text-slate-400">{i.currency}</span>
              </td>
              <td className="px-4 py-2 text-right tabular-nums">{money(i.paid)}</td>
              <td className="px-4 py-2 text-right font-medium tabular-nums">{money(i.outstanding)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
