"use client";

import { useEffect, useMemo, useState } from "react";
import { Icon } from "./Icon";
import { useRefOptions } from "@/lib/useRefs";
import type { Entity, Field } from "@/lib/types";

// List filter params understood by the API: f_<field> (equals, or @empty),
// fin_<field> (any of, comma-separated), fmin_/fmax_<field> (inclusive range).
export const FILTER_PREFIXES = ["f_", "fin_", "fmin_", "fmax_"];
export const isFilterParam = (k: string) => FILTER_PREFIXES.some((p) => k.startsWith(p));

const RANGE_TYPES = ["date", "datetime", "money", "number", "int", "percent"];
const FILTER_TYPES = ["select", "ref", "bool", ...RANGE_TYPES];

// ListFilters renders a filter panel built from the entity's field metadata
// (pick-lists, references, yes/no, date and number ranges) plus chips for the
// active filters. It edits a filter query string; the list page owns the URL.
export function ListFilters({
  entity,
  query,
  onChange,
}: {
  entity: Entity;
  query: string;
  onChange: (query: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<URLSearchParams>(new URLSearchParams(query));
  useEffect(() => setDraft(new URLSearchParams(query)), [query]);

  const fields = useMemo(
    () => entity.fields.filter((f) => (f.in_list || entity.columns?.includes(f.key)) && FILTER_TYPES.includes(f.type)),
    [entity]
  );
  const active = new URLSearchParams(query);
  const chips = fields.flatMap((f) => chipFor(f, active));

  function set(k: string, v: string) {
    setDraft((d) => {
      const n = new URLSearchParams(d);
      if (v) n.set(k, v);
      else n.delete(k);
      return n;
    });
  }
  function apply() {
    onChange(draft.toString());
    setOpen(false);
  }
  function remove(f: Field) {
    const n = new URLSearchParams(query);
    for (const p of FILTER_PREFIXES) n.delete(p + f.key);
    onChange(n.toString());
  }

  if (fields.length === 0) return null;

  return (
    <div className="flex flex-1 flex-wrap items-center gap-2">
      <button type="button" className="btn-secondary" onClick={() => setOpen((o) => !o)}>
        <Icon name="filter" className="h-4 w-4" /> Filters{chips.length ? ` (${chips.length})` : ""}
      </button>
      {chips.map((c) => (
        <span key={c.field.key} className="inline-flex items-center gap-1 rounded-full bg-slate-100 px-2.5 py-1 text-xs text-slate-700">
          <span className="font-medium">{c.field.label}:</span> {c.text}
          <button type="button" className="text-slate-400 hover:text-slate-700" onClick={() => remove(c.field)} title="Remove filter">
            <Icon name="x" className="h-3 w-3" />
          </button>
        </span>
      ))}
      {chips.length > 0 && (
        <button type="button" className="text-xs text-slate-500 hover:underline" onClick={() => onChange("")}>
          Clear all
        </button>
      )}

      {open && (
        <div className="card w-full p-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {fields.map((f) => (
              <div key={f.key}>
                <label className="label">{f.label}</label>
                <FilterInput field={f} draft={draft} set={set} />
              </div>
            ))}
          </div>
          <div className="mt-4 flex gap-2">
            <button type="button" className="btn-primary" onClick={apply}>
              Apply
            </button>
            <button type="button" className="btn-secondary" onClick={() => setDraft(new URLSearchParams())}>
              Reset
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function FilterInput({ field: f, draft, set }: { field: Field; draft: URLSearchParams; set: (k: string, v: string) => void }) {
  const refOpts = useRefOptions(f.type === "ref" ? f.ref : undefined);
  if (f.type === "select") {
    const chosen = (draft.get("fin_" + f.key) || draft.get("f_" + f.key) || "").split(",").filter(Boolean);
    const toggle = (o: string) => {
      const next = chosen.includes(o) ? chosen.filter((x) => x !== o) : [...chosen, o];
      set("f_" + f.key, "");
      set("fin_" + f.key, next.join(","));
    };
    return (
      <div className="flex flex-wrap gap-x-3 gap-y-1">
        {(f.options || []).map((o) => (
          <label key={o} className="inline-flex items-center gap-1 text-sm text-slate-600">
            <input type="checkbox" className="checkbox" checked={chosen.includes(o)} onChange={() => toggle(o)} />
            {o}
          </label>
        ))}
      </div>
    );
  }
  if (f.type === "ref") {
    return (
      <select className="select" value={draft.get("f_" + f.key) || ""} onChange={(e) => set("f_" + f.key, e.target.value)}>
        <option value="">Any</option>
        <option value="@empty">(none)</option>
        {refOpts.map((o) => (
          <option key={o.id} value={o.id}>
            {o.label}
          </option>
        ))}
      </select>
    );
  }
  if (f.type === "bool") {
    return (
      <select className="select" value={draft.get("f_" + f.key) || ""} onChange={(e) => set("f_" + f.key, e.target.value)}>
        <option value="">Any</option>
        <option value="true">Yes</option>
        <option value="false">No</option>
      </select>
    );
  }
  const isDate = f.type === "date" || f.type === "datetime";
  const val = (k: string) => {
    const v = draft.get(k) || "";
    return isDate && v.startsWith("@") ? resolveToken(v) : v;
  };
  return (
    <div className="flex items-center gap-2">
      <input
        className="input"
        type={isDate ? "date" : "number"}
        placeholder="from"
        value={val("fmin_" + f.key)}
        onChange={(e) => set("fmin_" + f.key, e.target.value)}
      />
      <span className="text-slate-400">–</span>
      <input
        className="input"
        type={isDate ? "date" : "number"}
        placeholder="to"
        value={val("fmax_" + f.key)}
        onChange={(e) => set("fmax_" + f.key, e.target.value)}
      />
    </div>
  );
}

// chipFor describes the active filter on one field, if any.
function chipFor(f: Field, q: URLSearchParams): { field: Field; text: string }[] {
  const eq = q.get("f_" + f.key);
  const any = q.get("fin_" + f.key);
  const min = q.get("fmin_" + f.key);
  const max = q.get("fmax_" + f.key);
  let text = "";
  if (eq) text = eq === "@empty" ? "(none)" : f.type === "bool" ? (eq === "true" ? "Yes" : "No") : f.type === "ref" ? "selected" : eq;
  else if (any) text = any.split(",").join(", ");
  else if (min && max) text = `${pretty(min)} – ${pretty(max)}`;
  else if (min) text = `≥ ${pretty(min)}`;
  else if (max) text = `≤ ${pretty(max)}`;
  return text ? [{ field: f, text }] : [];
}

// pretty shows relative date tokens readably (saved views use them).
function pretty(v: string): string {
  if (v === "@today") return "today";
  if (v === "@month") return "start of month";
  const m = v.match(/^@today([+-]\d+)$/);
  if (m) {
    const n = Number(m[1]);
    if (n === -1) return "yesterday";
    return n > 0 ? `today + ${n}d` : `today − ${-n}d`;
  }
  return v;
}

// resolveToken mirrors the server's date tokens for showing them in inputs.
function resolveToken(v: string): string {
  const now = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  const iso = (d: Date) => `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  if (v === "@today") return iso(now);
  if (v === "@month") return iso(new Date(now.getFullYear(), now.getMonth(), 1));
  const m = v.match(/^@today([+-]\d+)$/);
  if (m) {
    const d = new Date(now);
    d.setDate(d.getDate() + Number(m[1]));
    return iso(d);
  }
  return v;
}
