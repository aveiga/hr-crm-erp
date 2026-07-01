"use client";

import { use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { useEntity, useMeta } from "@/components/MetaProvider";
import { useAuth, canWrite } from "@/lib/auth";
import { useRefMaps } from "@/lib/useRefs";
import { formatValue } from "@/lib/format";
import { Icon } from "@/components/Icon";
import { PeopleCostSummary } from "@/components/PeopleCostSummary";
import type { Field, ListResult } from "@/lib/types";

export default function ListPage({ params }: { params: Promise<{ entity: string }> }) {
  const { entity: entityKey } = use(params);
  const entity = useEntity(entityKey);
  const { entities } = useMeta();
  const { role } = useAuth();
  const router = useRouter();

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
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [result, setResult] = useState<ListResult | null>(null);
  const [loading, setLoading] = useState(true);

  // Yearly cost = hourly rate × yearly work hours × FTE% — all on the person.
  const personCost = (row: any) =>
    Number(row.hourly_rate || 0) * Number(row.yearly_hours || 0) * (Number(row.fte_pct || 0) / 100);

  // Show the most relevant columns, and always surface the money totals
  // (net + grand) for document entities so each row shows its value.
  const listFields = useMemo<Field[]>(() => {
    if (!entity) return [];
    const totalKeys = ["net_total", "grand_total"];
    const inList = entity.fields.filter((f) => f.in_list);
    const totals = inList.filter((f) => totalKeys.includes(f.key));
    const head = inList.filter((f) => !totalKeys.includes(f.key)).slice(0, totals.length ? 5 : 7);
    return [...head, ...totals];
  }, [entity]);
  const refTargets = useMemo(
    () => Array.from(new Set(listFields.filter((f) => f.type === "ref" && f.ref).map((f) => f.ref!))),
    [listFields]
  );
  const refMaps = useRefMaps(refTargets);

  useEffect(() => {
    if (!entity) return;
    setLoading(true);
    const qs = new URLSearchParams({ page: String(page), page_size: "25", dir: "desc" });
    if (search) qs.set("q", search);
    api
      .list(entityKey, qs.toString())
      .then(setResult)
      .catch(() => setResult({ data: [], total: 0, page: 1, page_size: 25 }))
      .finally(() => setLoading(false));
  }, [entityKey, entity, page, search]);

  if (!entity) {
    return <div className="text-slate-400">Unknown module.</div>;
  }

  const display = (f: Field, row: any) => {
    if (f.type === "ref" && f.ref) return refMaps[f.ref]?.[row[f.key]] ?? "";
    return formatValue(f, row[f.key]);
  };
  const numeric = (t: string) => ["money", "number", "percent", "int"].includes(t);

  const total = result?.total ?? 0;
  const pageSize = result?.page_size ?? 25;
  const maxPage = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Icon name={entity.icon} className="h-6 w-6 text-brand-600" />
          <h1 className="text-2xl font-bold">{entity.plural}</h1>
          <span className="text-sm text-slate-400">({total})</span>
        </div>
        <div className="flex gap-2">
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

      <input
        className="input max-w-sm"
        placeholder={`Search ${entity.plural.toLowerCase()}…`}
        value={search}
        onChange={(e) => {
          setPage(1);
          setSearch(e.target.value);
        }}
      />

      <div className="card overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-left text-slate-500">
              {listFields.map((f) => (
                <th
                  key={f.key}
                  className={`px-4 py-2 font-medium whitespace-nowrap ${numeric(f.type) ? "text-right" : ""}`}
                >
                  {f.label}
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
                  No {entity.plural.toLowerCase()} yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {maxPage > 1 && (
        <div className="flex items-center justify-end gap-2 text-sm">
          <button className="btn-secondary" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
            Previous
          </button>
          <span className="text-slate-500">
            Page {page} of {maxPage}
          </span>
          <button className="btn-secondary" disabled={page >= maxPage} onClick={() => setPage((p) => p + 1)}>
            Next
          </button>
        </div>
      )}
    </div>
  );
}
