"use client";

import { FieldInput } from "./FieldInput";
import { Icon } from "./Icon";
import type { Entity, Field } from "@/lib/types";

// ChildItemsEditor edits a generic sub-collection (e.g. an account's contacts)
// as repeating blocks of fields. Line-item documents use the specialised
// LineItemsEditor instead.
export function ChildItemsEditor({
  childEntity,
  fk,
  label,
  rows,
  onChange,
  disabled,
}: {
  childEntity: Entity;
  fk: string;
  label: string;
  rows: any[];
  onChange: (rows: any[]) => void;
  disabled?: boolean;
}) {
  const fields: Field[] = childEntity.fields.filter(
    (f) => f.key !== fk && !f.read_only && f.key !== "line_no" && f.key !== "line_net"
  );

  function update(i: number, key: string, v: any) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, [key]: v } : r)));
  }
  function addRow() {
    onChange([...rows, {}]);
  }
  function removeRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i));
  }

  return (
    <div className="card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-semibold">{label}</h3>
        {!disabled && (
          <button type="button" className="btn-secondary" onClick={addRow}>
            <Icon name="plus" className="h-4 w-4" /> Add
          </button>
        )}
      </div>

      {rows.length === 0 && (
        <p className="text-sm text-slate-400">No {label.toLowerCase()} yet.</p>
      )}

      <div className="space-y-4">
        {rows.map((row, i) => (
          <div key={i} className="rounded-md border border-slate-200 p-3">
            <div className="mb-2 flex justify-end">
              {!disabled && (
                <button
                  type="button"
                  className="text-slate-400 hover:text-red-600"
                  onClick={() => removeRow(i)}
                  title="Remove"
                >
                  <Icon name="x" className="h-4 w-4" />
                </button>
              )}
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {fields.map((f) => (
                <div key={f.key} className={f.type === "longtext" ? "sm:col-span-2 lg:col-span-3" : ""}>
                  {f.type !== "bool" && (
                    <label className="label">
                      {f.label}
                      {f.required && <span className="text-red-500"> *</span>}
                    </label>
                  )}
                  <FieldInput
                    field={f}
                    value={row[f.key]}
                    record={row}
                    disabled={disabled}
                    onChange={(v) => update(i, f.key, v)}
                  />
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
