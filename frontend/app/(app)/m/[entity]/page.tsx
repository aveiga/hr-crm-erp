"use client";

import { Suspense, use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { api } from "@/lib/api";
import { useEntity, useMeta } from "@/components/MetaProvider";
import { useAuth, canWrite } from "@/lib/auth";
import { useRefMaps } from "@/lib/useRefs";
import { formatValue } from "@/lib/format";
import { Icon } from "@/components/Icon";
import { PeopleCostSummary } from "@/components/PeopleCostSummary";
import { ListFilters, FILTER_PREFIXES, isFilterParam } from "@/components/ListFilters";
import type { Field, ListResult } from "@/lib/types";

const PAGE_SIZE = 25;

export default function ListPage({ params }: { params: Promise<{ entity: string }> }) {
  const { entity } = use(params);
  // useSearchParams needs a Suspense boundary.
  return (
    <Suspense fallback={<div className="text-slate-400">Loading…</div>}>
      <EntityList entityKey={entity} />
    </Suspense>
  );
}

function EntityList({ entityKey }: { entityKey: string }) {
  const entity = useEntity(entityKey);
  const { entities } = useMeta();
  const { role } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();

  // List state lives in the URL (q, sort, dir, page and filter params) so a
  // filtered list can be bookmarked or linked to from reports.
  const query = params.toString();
  const search = params.get("q") || "";
  const page = Number(params.get("page") || 1);
  const sort = params.get("sort") || "";
  const dir = params.get("dir") || "desc";
  const filterQuery = useMemo(() => {
    const f = new URLSearchParams();
    params.forEach((v, k) => isFilterParam(k) && f.set(k, v));
    return f.toString();
  }, [params]);

  function setParams(update: (p: URLSearchParams) => void, resetPage = true) {
    const next = new URLSearchParams(query);
    update(next);
    if (resetPage) next.delete("page");
    router.replace(`${pathname}${next.toString() ? `?${next}` : ""}`);
  }

  // For a read-only child list (e.g. Contacts), find the parent entity so a row
  // click can open the record where it's actually edited.
  const parent = useMemo(() => {
    for (const e of entities) {
      const c = e.children?.find((ch) => ch.entity === entityKey);
      if (c) return { entity: e.key, fk: c.fk };
    }
    return null;
  }, [entities, entityKey]);

  const isPeople = entityKey === "people";
  const [searchInput, setSearchInput] = useState(search);
  const [result, setResult] = useState<ListResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);

  useEffect(() => setSearchInput(search), [search]);
  // Debounce the search box into the URL.
  useEffect(() => {
    if (searchInput === search) return;
    const t = setTimeout(() => setParams((p) => (searchInput ? p.set("q", searchInput) : p.delete("q"))), 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchInput]);

  // Yearly cost = hourly rate × yearly work hours × FTE% — all on the person.
  const personCost = (row: any) =>
    Number(row.hourly_rate || 0) * Number(row.yearly_hours || 0) * (Number(row.fte_pct || 0) / 100);

  // Columns: the catalog's explicit list, else the first in-list fields plus
  // the money totals (net + grand) for documents.
  const listFields = useMemo<Field[]>(() => {
    if (!entity) return [];
    if (entity.columns?.length) {
      return entity.columns.map((k) => entity.fields.find((f) => f.key === k)).filter((f): f is Field => !!f);
    }
    const totalKeys = ["net_total", "grand_total"];
    const inList = entity.fields.filter((f) => f.in_list);
    const totals = inList.filter((f) => totalKeys.includes(f.key));
    const head = inList.filter((f) => !totalKeys.includes(f.key)).slice(0, totals.length ? 5 : 7);
    return [...head, ...totals];
  }, [entity]);
  // CSV export includes every listed (non-hidden) field.
  const exportFields = useMemo<Field[]>(() => (entity ? entity.fields.filter((f) => f.in_list) : []), [entity]);
  const refTargets = useMemo(
    () => Array.from(new Set(exportFields.filter((f) => f.type === "ref" && f.ref).map((f) => f.ref!))),
    [exportFields]
  );
  const refMaps = useRefMaps(refTargets);

  const listQuery = (p: number, size: number) => {
    const qs = new URLSearchParams(filterQuery);
    qs.set("page", String(p));
    qs.set("page_size", String(size));
    qs.set("dir", dir);
    if (sort) qs.set("sort", sort);
    if (search) qs.set("q", search);
    return qs.toString();
  };

  useEffect(() => {
    if (!entity) return;
    setLoading(true);
    api
      .list(entityKey, listQuery(page, PAGE_SIZE))
      .then(setResult)
      .catch(() => setResult({ data: [], total: 0, page: 1, page_size: PAGE_SIZE }))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entityKey, entity, query]);

  if (!entity) {
    return <div className="text-slate-400">Unknown module.</div>;
  }

  const display = (f: Field, row: any) => {
    if (f.type === "ref" && f.ref) return refMaps[f.ref]?.[row[f.key]] ?? "";
    return formatValue(f, row[f.key]);
  };
  const numeric = (t: string) => ["money", "number", "percent", "int"].includes(t);

  const total = result?.total ?? 0;
  const maxPage = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const activeView = entity.views?.find((v) => sameQuery(v.query, filterQuery));

  function applyView(q: string) {
    setParams((p) => {
      for (const k of Array.from(p.keys())) if (isFilterParam(k)) p.delete(k);
      new URLSearchParams(q).forEach((v, k) => p.set(k, v));
    });
  }

  function toggleSort(f: Field) {
    setParams((p) => {
      if (sort === f.key) p.set("dir", dir === "desc" ? "asc" : "desc");
      else {
        p.set("sort", f.key);
        p.set("dir", "asc");
      }
    }, false);
  }

  // Export every row matching the current filters/search as CSV.
  async function exportCsv() {
    setExporting(true);
    try {
      const rows: any[] = [];
      for (let p = 1; p <= 50; p++) {
        const res = await api.list(entityKey, listQuery(p, 200));
        rows.push(...res.data);
        if (rows.length >= res.total || res.data.length === 0) break;
      }
      const header = exportFields.map((f) => csvCell(f.label));
      const lines = rows.map((r) => exportFields.map((f) => csvCell(display(f, r))).join(","));
      const blob = new Blob(["﻿" + [header.join(","), ...lines].join("\r\n")], { type: "text/csv;charset=utf-8" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `${entity!.plural}${activeView ? " - " + activeView.label : ""}.csv`;
      a.click();
      URL.revokeObjectURL(a.href);
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Icon name={entity.icon} className="h-6 w-6 text-brand-600" />
          <h1 className="text-2xl font-bold">{entity.plural}</h1>
          <span className="text-sm text-slate-400">({total})</span>
        </div>
        <div className="flex gap-2">
          <button type="button" className="btn-secondary" onClick={exportCsv} disabled={exporting || total === 0}>
            <Icon name="download" className="h-4 w-4" /> {exporting ? "Exporting…" : "Export CSV"}
          </button>
          {entityKey === "people" && (
            <Link href="/org" className="btn-secondary">
              <Icon name="network" className="h-4 w-4" /> Organization
            </Link>
          )}
          {canWrite(role) && !entity.read_only && (
            <Link href={`/m/${entityKey}/new`} className="btn-primary">
              <Icon name="plus" className="h-4 w-4" /> New {entity.label}
            </Link>
          )}
        </div>
      </div>

      {entity.read_only && parent && (
        <p className="rounded-md bg-slate-100 px-3 py-2 text-sm text-slate-500">
          This is a read-only directory. Click a row to open its {parent.entity.replace(/s$/, "")} to edit.
        </p>
      )}

      {isPeople && <PeopleCostSummary />}

      {!!entity.views?.length && (
        <div className="flex flex-wrap gap-2">
          <ViewChip active={!filterQuery} onClick={() => applyView("")}>
            All
          </ViewChip>
          {entity.views.map((v) => (
            <ViewChip key={v.label} active={activeView?.label === v.label} onClick={() => applyView(v.query)}>
              {v.label}
            </ViewChip>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-start gap-2">
        <input
          className="input max-w-sm"
          placeholder={`Search ${entity.plural.toLowerCase()}…`}
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
        />
        <ListFilters entity={entity} query={filterQuery} onChange={(q) => applyView(q)} />
      </div>

      <div className="card overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-left text-slate-500">
              {listFields.map((f) => (
                <th
                  key={f.key}
                  className={`cursor-pointer select-none px-4 py-2 font-medium whitespace-nowrap hover:text-slate-700 ${
                    numeric(f.type) ? "text-right" : ""
                  }`}
                  onClick={() => toggleSort(f)}
                >
                  {f.label}
                  {sort === f.key && (
                    <Icon name={dir === "asc" ? "chevron-up" : "chevron-down"} className="ml-1 inline h-3 w-3" />
                  )}
                </th>
              ))}
              {isPeople && <th className="px-4 py-2 font-medium whitespace-nowrap text-right">Yearly Cost</th>}
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={listFields.length + (isPeople ? 2 : 1)} className="px-4 py-8 text-center text-slate-400">
                  Loading…
                </td>
              </tr>
            ) : result && result.data.length > 0 ? (
              result.data.map((row) => (
                <tr
                  key={row.id}
                  className="cursor-pointer border-b border-slate-100 hover:bg-brand-50"
                  onClick={() => {
                    // Read-only child rows open their parent record for editing.
                    if (entity.read_only && parent && row[parent.fk]) {
                      router.push(`/m/${parent.entity}/${row[parent.fk]}`);
                    } else {
                      router.push(`/m/${entityKey}/${row.id}`);
                    }
                  }}
                >
                  {listFields.map((f) => (
                    <td
                      key={f.key}
                      className={`px-4 py-2 whitespace-nowrap ${numeric(f.type) ? "text-right tabular-nums" : ""}`}
                    >
                      {display(f, row)}
                    </td>
                  ))}
                  {isPeople && (
                    <td className="px-4 py-2 text-right tabular-nums font-medium">
                      {personCost(row) ? Math.round(personCost(row)).toLocaleString() : ""}
                    </td>
                  )}
                  <td className="px-4 py-2 text-right text-slate-300">
                    <Icon name="chevron-right" className="h-4 w-4 inline" />
                  </td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={listFields.length + (isPeople ? 2 : 1)} className="px-4 py-8 text-center text-slate-400">
                  {filterQuery || search ? "No matching records." : `No ${entity.plural.toLowerCase()} yet.`}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {maxPage > 1 && (
        <div className="flex items-center justify-end gap-2 text-sm">
          <button
            className="btn-secondary"
            disabled={page <= 1}
            onClick={() => setParams((p) => p.set("page", String(page - 1)), false)}
          >
            Previous
          </button>
          <span className="text-slate-500">
            Page {page} of {maxPage}
          </span>
          <button
            className="btn-secondary"
            disabled={page >= maxPage}
            onClick={() => setParams((p) => p.set("page", String(page + 1)), false)}
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}

function ViewChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-full border px-3 py-1 text-sm ${
        active ? "border-brand-600 bg-brand-50 text-brand-700" : "border-slate-200 text-slate-600 hover:bg-slate-100"
      }`}
    >
      {children}
    </button>
  );
}

// sameQuery compares two filter query strings ignoring param order.
function sameQuery(a: string, b: string): boolean {
  const norm = (q: string) => {
    const p = new URLSearchParams(q);
    return Array.from(p.entries())
      .filter(([k]) => FILTER_PREFIXES.some((x) => k.startsWith(x)))
      .map(([k, v]) => `${k}=${v}`)
      .sort()
      .join("&");
  };
  return norm(a) === norm(b);
}

function csvCell(v: string): string {
  return /[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}
