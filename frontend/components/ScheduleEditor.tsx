"use client";

import { useEffect, useState } from "react";
import { Icon } from "./Icon";

// Months between payments for each billing frequency.
const STEP_MONTHS: globalThis.Record<string, number> = {
  Monthly: 1,
  Quarterly: 3,
  "Semi-annual": 6,
  Annual: 12,
};
const DEFAULT_COUNT: globalThis.Record<string, number> = { Monthly: 12, Quarterly: 4, "Semi-annual": 2, Annual: 1 };

// ScheduleEditor edits a service agreement's payment schedule: due dates, each
// with an amount. The agreement total is split evenly across the dates; an
// amount you type is kept and the remaining rows share what's left. The
// schedule must add up to the total (the server rejects it otherwise). Paid
// amounts and statuses come from registered payments and are read-only.
//
// Rows carry a client-only `_auto` flag (true = amount follows the split);
// rows loaded from the server count as typed.
export function ScheduleEditor({
  label,
  rows,
  onChange,
  disabled,
  lines,
  markupPct,
  vatPct,
  startDate,
  frequency,
  currency,
}: {
  label: string;
  rows: any[];
  onChange: (rows: any[]) => void;
  disabled?: boolean;
  lines: any[];
  markupPct: number;
  vatPct: number;
  startDate?: string;
  frequency?: string;
  currency?: string;
}) {
  const [count, setCount] = useState<number | "">("");

  // Same formula as the server's ComputeTotals.
  const net = lines.reduce((s, l) => s + Number(l.qty || 0) * Number(l.unit_price || 0), 0);
  const total = round2(net * (1 + markupPct / 100) * (1 + vatPct / 100));
  const scheduled = round2(rows.reduce((s, r) => s + Number(r.amount || 0), 0));
  const diff = round2(total - scheduled);

  // Keep auto rows in step when the agreement total changes.
  useEffect(() => {
    if (rows.some((r) => r._auto)) {
      const next = rebalance(rows, total);
      if (next.some((r, i) => r.amount !== rows[i].amount)) onChange(next);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [total]);

  const set = (next: any[]) => onChange(rebalance(next, total));

  function addDate() {
    const step = STEP_MONTHS[frequency || ""] || 1;
    const last = rows.length ? rows[rows.length - 1].due_date : "";
    const due = last ? iso(addMonths(parse(last), step)) : startDate || iso(new Date());
    set([...rows, { due_date: due, amount: "", description: "", _auto: true }]);
  }

  function generate() {
    const step = STEP_MONTHS[frequency || ""] || 0;
    const n = Number(count || DEFAULT_COUNT[frequency || ""] || 1);
    if (rows.length > 0 && !confirm(`Replace the ${rows.length} existing payment(s)?`)) return;
    const start = startDate ? parse(startDate) : new Date();
    const out = [];
    for (let i = 0; i < n; i++) {
      out.push({
        due_date: iso(addMonths(start, i * step)),
        amount: "",
        description: n === 1 ? "Full amount" : `Payment ${i + 1} of ${n}`,
        _auto: true,
      });
    }
    set(out);
  }

  function update(i: number, key: string, v: any) {
    const next = rows.map((r, idx) => (idx === i ? { ...r, [key]: v, ...(key === "amount" ? { _auto: v === "" } : {}) } : r));
    set(next);
  }

  return (
    <div className="card p-4">
      <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h3 className="font-semibold">{label}</h3>
          <p className="text-xs text-slate-400">
            The total is split evenly across the dates; type an amount to fix it and the rest share the remainder.
          </p>
        </div>
        {!disabled && (
          <div className="flex flex-wrap items-end gap-2">
            <div>
              <label className="label">Payments</label>
              <input
                className="input w-20"
                type="number"
                min={1}
                placeholder={String(DEFAULT_COUNT[frequency || ""] || 1)}
                value={count}
                onChange={(e) => setCount(e.target.value === "" ? "" : Number(e.target.value))}
              />
            </div>
            <button type="button" className="btn-secondary" onClick={generate} title="Dates from the service start date, spaced by the billing frequency">
              <Icon name="wand-sparkles" className="h-4 w-4" /> Generate{frequency ? ` (${frequency})` : ""}
            </button>
            {rows.length > 1 && (
              <button
                type="button"
                className="btn-secondary"
                onClick={() => set(rows.map((r) => ({ ...r, _auto: true })))}
                title="Reset every amount to an even share"
              >
                <Icon name="divide" className="h-4 w-4" /> Split evenly
              </button>
            )}
            <button type="button" className="btn-secondary" onClick={addDate}>
              <Icon name="calendar-plus" className="h-4 w-4" /> Add date
            </button>
          </div>
        )}
      </div>

      {rows.length === 0 ? (
        <p className="text-sm text-slate-400">
          No payment schedule — the agreement total is due as a whole. Add dates or Generate from the billing frequency.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="text-left text-slate-500">
                <th className="px-1 py-1 font-medium">#</th>
                <th className="px-1 py-1 font-medium">Due Date *</th>
                <th className="px-1 py-1 font-medium text-right">Amount{currency ? ` (${currency})` : ""}</th>
                <th className="px-1 py-1 font-medium">Description</th>
                <th className="px-1 py-1 font-medium text-right">Paid</th>
                <th className="px-1 py-1 font-medium">Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={row.id || i} className="border-t border-slate-100">
                  <td className="px-1 py-1 text-slate-400">{i + 1}</td>
                  <td className="px-1 py-1">
                    <input
                      className="input"
                      type="date"
                      disabled={disabled}
                      value={(row.due_date || "").slice(0, 10)}
                      onChange={(e) => update(i, "due_date", e.target.value)}
                    />
                  </td>
                  <td className="px-1 py-1">
                    <input
                      className={`input text-right ${row._auto ? "text-slate-500" : ""}`}
                      type="number"
                      step="0.01"
                      disabled={disabled}
                      title={row._auto ? "Even share — type to fix this amount" : "Fixed amount — clear to share evenly"}
                      value={row.amount ?? ""}
                      onChange={(e) => update(i, "amount", e.target.value === "" ? "" : Number(e.target.value))}
                    />
                  </td>
                  <td className="px-1 py-1">
                    <input
                      className="input"
                      disabled={disabled}
                      value={row.description ?? ""}
                      onChange={(e) => update(i, "description", e.target.value)}
                    />
                  </td>
                  <td className="px-1 py-1 text-right tabular-nums text-slate-600">{money(Number(row.paid_amount || 0))}</td>
                  <td className="px-1 py-1">
                    {row.status && (
                      <span
                        className={`rounded px-1.5 py-0.5 text-xs ${
                          row.status === "Paid"
                            ? "bg-green-100 text-green-700"
                            : row.status === "Partially Paid"
                              ? "bg-amber-100 text-amber-700"
                              : "bg-slate-100 text-slate-600"
                        }`}
                      >
                        {row.status}
                      </span>
                    )}
                  </td>
                  <td className="px-1 py-1">
                    {!disabled && (
                      <button
                        type="button"
                        className="text-slate-400 hover:text-red-600"
                        onClick={() => {
                          if (Number(row.paid_amount || 0) > 0 && !confirm("This payment has payments registered against it; they'll become unallocated. Remove it?"))
                            return;
                          set(rows.filter((_, idx) => idx !== i));
                        }}
                        title="Remove"
                      >
                        <Icon name="x" className="h-4 w-4" />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {rows.length > 0 && (
        <div className="mt-3 flex flex-wrap justify-end gap-6 text-sm">
          <span className="text-slate-500">
            Scheduled <span className="font-medium text-slate-700">{money(scheduled)}</span> of{" "}
            <span className="font-medium text-slate-700">{money(total)}</span>
          </span>
          {diff !== 0 ? (
            <span className="font-medium text-red-600">
              {diff > 0 ? `${money(diff)} not scheduled` : `${money(-diff)} over the total`} — the schedule must equal
              the agreement total to save
            </span>
          ) : (
            <span className="text-green-600">✓ Matches the total</span>
          )}
        </div>
      )}
    </div>
  );
}

// rebalance gives the auto rows an even share of what the fixed rows leave
// (the last auto row absorbs rounding), so the schedule always sums to total
// unless fixed amounts already exceed it.
function rebalance(rows: any[], total: number): any[] {
  const auto = rows.filter((r) => r._auto);
  if (auto.length === 0) return rows;
  const fixed = rows.filter((r) => !r._auto).reduce((s, r) => s + Number(r.amount || 0), 0);
  const left = Math.max(0, total - fixed);
  const share = Math.floor((left / auto.length) * 100) / 100;
  let n = 0;
  return rows.map((r) => {
    if (!r._auto) return r;
    n++;
    return { ...r, amount: n === auto.length ? round2(left - share * (auto.length - 1)) : share };
  });
}

const round2 = (n: number) => Math.round(n * 100) / 100;
const money = (n: number) => (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

function parse(d: string): Date {
  return new Date(d.slice(0, 10) + "T00:00:00");
}

function addMonths(d: Date, n: number): Date {
  const out = new Date(d);
  out.setMonth(out.getMonth() + n);
  return out;
}

function iso(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
