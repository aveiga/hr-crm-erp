"use client";

import { useEffect, useState } from "react";
import { api } from "./api";
import { useMeta } from "@/components/MetaProvider";

export interface RefOption {
  id: string;
  label: string;
}

// useRefOptions loads selectable options (id + display label) for a referenced
// entity, using that entity's title_field from the metadata catalog. An optional
// static filter (filterField == filterValue) restricts the options server-side.
export function useRefOptions(entityKey?: string, filterField?: string, filterValue?: string): RefOption[] {
  const { byKey } = useMeta();
  const [opts, setOpts] = useState<RefOption[]>([]);

  useEffect(() => {
    if (!entityKey) return;
    const target = byKey[entityKey];
    let qs = "page_size=200";
    if (filterField && filterValue) qs += `&f_${filterField}=${encodeURIComponent(filterValue)}`;
    api
      .list(entityKey, qs)
      .then((res) => {
        const titleField = target?.title_field || "id";
        setOpts(
          res.data.map((r) => ({
            id: r.id,
            label: String(r[titleField] ?? r.id).trim() || r.id,
          }))
        );
      })
      .catch(() => setOpts([]));
  }, [entityKey, byKey, filterField, filterValue]);

  return opts;
}

// useLookupOptions loads {value,label} options for a lookup field, where the
// stored value is a chosen column (e.g. a product's SKU) rather than an id.
export function useLookupOptions(entityKey?: string, valueField?: string, labelField?: string): RefOption[] {
  const [opts, setOpts] = useState<RefOption[]>([]);
  useEffect(() => {
    if (!entityKey || !valueField) return;
    api
      .list(entityKey, "page_size=500")
      .then((res) => {
        setOpts(
          res.data
            .map((r) => {
              const value = String(r[valueField] ?? "").trim();
              const lbl = labelField ? String(r[labelField] ?? "").trim() : "";
              return { id: value, label: lbl && lbl !== value ? `${value} — ${lbl}` : value };
            })
            .filter((o) => o.id !== "")
        );
      })
      .catch(() => setOpts([]));
  }, [entityKey, valueField, labelField]);
  return opts;
}

// useRefMaps loads id->label maps for several ref targets at once (used by the
// table view to render references as names).
export function useRefMaps(targets: string[]): globalThis.Record<string, globalThis.Record<string, string>> {
  const { byKey } = useMeta();
  const [maps, setMaps] = useState<globalThis.Record<string, globalThis.Record<string, string>>>({});
  const key = targets.slice().sort().join(",");

  useEffect(() => {
    if (targets.length === 0) return;
    let cancelled = false;
    Promise.all(
      targets.map(async (t) => {
        try {
          const res = await api.list(t, "page_size=200");
          const titleField = byKey[t]?.title_field || "id";
          const m: globalThis.Record<string, string> = {};
          for (const r of res.data) m[r.id] = String(r[titleField] ?? r.id).trim() || r.id;
          return [t, m] as const;
        } catch {
          return [t, {}] as const;
        }
      })
    ).then((entries) => {
      if (cancelled) return;
      const out: globalThis.Record<string, globalThis.Record<string, string>> = {};
      for (const [t, m] of entries) out[t] = m;
      setMaps(out);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return maps;
}
