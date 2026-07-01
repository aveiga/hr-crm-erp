"use client";

import { useEffect, useState } from "react";
import { useRefOptions, useLookupOptions } from "@/lib/useRefs";
import { api } from "@/lib/api";
import type { Field } from "@/lib/types";
import type { RefOption } from "@/lib/useRefs";

export function FieldInput({
  field,
  value,
  onChange,
  disabled,
  record,
}: {
  field: Field;
  value: any;
  onChange: (v: any) => void;
  disabled?: boolean;
  record?: Record<string, any>;
}) {
  const ro = disabled || field.read_only;
  switch (field.type) {
    case "longtext":
      return (
        <textarea
          className="input min-h-[80px]"
          disabled={ro}
          value={value ?? ""}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case "bool":
      return (
        <label className="inline-flex items-center gap-2">
          <input
            type="checkbox"
            className="checkbox"
            disabled={ro}
            checked={Boolean(value)}
            onChange={(e) => onChange(e.target.checked)}
          />
          <span className="text-sm text-slate-600">{field.label}</span>
        </label>
      );
    case "int":
    case "number":
    case "money":
    case "percent":
      return (
        <input
          className="input"
          type="number"
          step={field.type === "int" ? "1" : "any"}
          disabled={ro}
          value={value ?? ""}
          onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
        />
      );
    case "date":
      return (
        <input className="input" type="date" disabled={ro} value={value ?? ""} onChange={(e) => onChange(e.target.value)} />
      );
    case "select":
      return (
        <select className="select" disabled={ro} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
          <option value="">—</option>
          {(field.options || []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      );
    case "ref":
      return <RefInput field={field} value={value} onChange={onChange} disabled={ro} />;
    case "lookup":
      if (field.filter_by) {
        return <FilteredLookupInput field={field} value={value} onChange={onChange} disabled={ro} record={record} />;
      }
      return <LookupInput field={field} value={value} onChange={onChange} disabled={ro} />;
    case "email":
      return (
        <input className="input" type="email" disabled={ro} value={value ?? ""} onChange={(e) => onChange(e.target.value)} />
      );
    default:
      return (
        <input className="input" disabled={ro} value={value ?? ""} onChange={(e) => onChange(e.target.value)} />
      );
  }
}

function RefInput({ field, value, onChange, disabled }: { field: Field; value: any; onChange: (v: any) => void; disabled?: boolean }) {
  const opts = useRefOptions(field.ref, field.option_filter_field, field.option_filter_value);
  return (
    <select className="select" disabled={disabled} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">—</option>
      {opts.map((o) => (
        <option key={o.id} value={o.id}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

function LookupInput({ field, value, onChange, disabled }: { field: Field; value: any; onChange: (v: any) => void; disabled?: boolean }) {
  const opts = useLookupOptions(field.ref, field.value_field, field.label_field);
  return (
    <select className="select" disabled={disabled} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">—</option>
      {opts.map((o) => (
        <option key={o.id} value={o.id}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

// FilteredLookupInput is a dependent dropdown: its options come from the source
// entity restricted to rows whose filter_field equals the record's filter_by
// sibling value (e.g. Team options for the chosen Department). It's disabled
// until that sibling is set.
function FilteredLookupInput({
  field,
  value,
  onChange,
  disabled,
  record,
}: {
  field: Field;
  value: any;
  onChange: (v: any) => void;
  disabled?: boolean;
  record?: Record<string, any>;
}) {
  const filterVal = field.filter_by ? record?.[field.filter_by] : "";
  const [opts, setOpts] = useState<RefOption[]>([]);

  useEffect(() => {
    if (!field.ref || !field.value_field || !filterVal) {
      setOpts([]);
      return;
    }
    const qs = `f_${field.filter_field}=${encodeURIComponent(filterVal)}&page_size=500`;
    api
      .list(field.ref, qs)
      .then((res) => {
        setOpts(
          res.data
            .map((r) => {
              const v = String(r[field.value_field!] ?? "").trim();
              const lbl = field.label_field ? String(r[field.label_field] ?? "").trim() : "";
              return { id: v, label: lbl && lbl !== v ? `${v} — ${lbl}` : v };
            })
            .filter((o) => o.id !== "")
        );
      })
      .catch(() => setOpts([]));
  }, [field.ref, field.value_field, field.label_field, field.filter_field, filterVal]);

  if (!filterVal) {
    return (
      <select className="select" disabled value="">
        <option value="">Select department first</option>
      </select>
    );
  }
  return (
    <select className="select" disabled={disabled} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">—</option>
      {/* keep a stored value that is no longer a listed option selectable */}
      {value && !opts.some((o) => o.id === value) && <option value={value}>{value}</option>}
      {opts.map((o) => (
        <option key={o.id} value={o.id}>
          {o.label}
        </option>
      ))}
    </select>
  );
}
