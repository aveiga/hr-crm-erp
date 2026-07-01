"use client";

import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { Icon } from "./Icon";

export interface LineItem {
  sku?: string;
  description?: string;
  qty?: number | string;
  unit_price?: number | string;
}

interface Product {
  sku: string;
  name: string;
  sales_price: number;
}

const money = (n: number) =>
  (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

export function LineItemsEditor({
  lines,
  onChange,
  currency,
  markupPct,
  vatPct,
  disabled,
}: {
  lines: LineItem[];
  onChange: (lines: LineItem[]) => void;
  currency?: string;
  markupPct?: number;
  vatPct?: number;
  disabled?: boolean;
}) {
  const [products, setProducts] = useState<Product[]>([]);

  useEffect(() => {
    api
      .list("products", "page_size=500")
      .then((res) =>
        setProducts(
          res.data.map((r) => ({
            sku: String(r.sku ?? ""),
            name: String(r.name ?? ""),
            sales_price: Number(r.sales_price ?? 0),
          }))
        )
      )
      .catch(() => setProducts([]));
  }, []);

  const bySku = useMemo(() => {
    const m: Record<string, Product> = {};
    for (const p of products) if (p.sku) m[p.sku] = p;
    return m;
  }, [products]);

  function update(i: number, patch: Partial<LineItem>) {
    const next = lines.map((l, idx) => (idx === i ? { ...l, ...patch } : l));
    onChange(next);
  }

  function pickSku(i: number, sku: string) {
    const p = bySku[sku];
    const patch: Partial<LineItem> = { sku };
    if (p) {
      const cur = lines[i];
      if (!cur.description) patch.description = p.name;
      if (cur.unit_price === undefined || cur.unit_price === "" || Number(cur.unit_price) === 0)
        patch.unit_price = p.sales_price;
    }
    update(i, patch);
  }

  function addRow() {
    onChange([...lines, { sku: "", description: "", qty: 1, unit_price: 0 }]);
  }
  function removeRow(i: number) {
    onChange(lines.filter((_, idx) => idx !== i));
  }

  const net = lines.reduce((s, l) => s + Number(l.qty || 0) * Number(l.unit_price || 0), 0);
  const markup = (net * (markupPct || 0)) / 100;
  const base = net + markup;
  const vat = (base * (vatPct || 0)) / 100;
  const grand = base + vat;

  return (
    <div className="card p-4">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="font-semibold">Line Items</h3>
        {!disabled && (
          <button type="button" className="btn-secondary" onClick={addRow}>
            <Icon name="plus" className="h-4 w-4" /> Add line
          </button>
        )}
      </div>

      <div className="overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="text-left text-slate-500">
              <th className="px-2 py-1 font-medium w-40">SKU</th>
              <th className="px-2 py-1 font-medium">Description</th>
              <th className="px-2 py-1 font-medium w-20">Qty</th>
              <th className="px-2 py-1 font-medium w-28">Unit Price</th>
              <th className="px-2 py-1 font-medium w-28 text-right">Line Net</th>
              <th className="w-8" />
            </tr>
          </thead>
          <tbody>
            {lines.length === 0 && (
              <tr>
                <td colSpan={6} className="px-2 py-4 text-center text-slate-400">
                  No lines. {!disabled && "Click “Add line”."}
                </td>
              </tr>
            )}
            {lines.map((l, i) => {
              const lineNet = Number(l.qty || 0) * Number(l.unit_price || 0);
              return (
                <tr key={i} className="border-t border-slate-100">
                  <td className="px-2 py-1">
                    <select
                      className="select"
                      disabled={disabled}
                      value={l.sku ?? ""}
                      onChange={(e) => pickSku(i, e.target.value)}
                    >
                      <option value="">—</option>
                      {products.map((p) => (
                        <option key={p.sku} value={p.sku}>
                          {p.sku}
                          {p.name ? ` — ${p.name}` : ""}
                        </option>
                      ))}
                      {/* keep a stored sku that no longer exists selectable */}
                      {l.sku && !bySku[l.sku] && <option value={l.sku}>{l.sku}</option>}
                    </select>
                  </td>
                  <td className="px-2 py-1">
                    <input
                      className="input"
                      disabled={disabled}
                      value={l.description ?? ""}
                      onChange={(e) => update(i, { description: e.target.value })}
                    />
                  </td>
                  <td className="px-2 py-1">
                    <input
                      className="input"
                      type="number"
                      step="any"
                      disabled={disabled}
                      value={l.qty ?? ""}
                      onChange={(e) => update(i, { qty: e.target.value === "" ? "" : Number(e.target.value) })}
                    />
                  </td>
                  <td className="px-2 py-1">
                    <input
                      className="input"
                      type="number"
                      step="any"
                      disabled={disabled}
                      value={l.unit_price ?? ""}
                      onChange={(e) => update(i, { unit_price: e.target.value === "" ? "" : Number(e.target.value) })}
                    />
                  </td>
                  <td className="px-2 py-1 text-right tabular-nums">{money(lineNet)}</td>
                  <td className="px-2 py-1">
                    {!disabled && (
                      <button type="button" className="text-slate-400 hover:text-red-600" onClick={() => removeRow(i)}>
                        <Icon name="x" className="h-4 w-4" />
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div className="mt-3 flex justify-end">
        <div className="w-64 space-y-1 text-sm">
          <Row label="Net" value={`${money(net)} ${currency || ""}`} />
          {markupPct ? <Row label={`Markup (${markupPct}%)`} value={`${money(markup)} ${currency || ""}`} /> : null}
          <Row label={`VAT (${vatPct || 0}%)`} value={`${money(vat)} ${currency || ""}`} />
          <div className="flex justify-between border-t border-slate-200 pt-1 font-semibold">
            <span>Grand Total</span>
            <span>{money(grand)} {currency || ""}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between text-slate-600">
      <span>{label}</span>
      <span className="tabular-nums">{value}</span>
    </div>
  );
}
