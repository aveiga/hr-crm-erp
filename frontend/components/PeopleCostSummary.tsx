"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { buildDepts, type OrgPerson } from "./OrgChart";

const fmt = (n: number) => Math.round(n || 0).toLocaleString();

// PeopleCostSummary shows yearly cost aggregated per Department and Team on the
// HR People page. Cost = hourly rate × team yearly hours × FTE%.
export function PeopleCostSummary() {
  const [depts, setDepts] = useState<any[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.list("people", "page_size=1000"), api.list("departments", "page_size=1000")])
      .then(([pp, dd]) => {
        const deptName: Record<string, string> = {};
        for (const d of dd.data) deptName[d.id] = String(d.name ?? "—");
        const resolved: OrgPerson[] = pp.data.map((p: any) => {
          const hours = Number(p.yearly_hours ?? 0);
          const rate = Number(p.hourly_rate ?? 0);
          const fte = Number(p.fte_pct ?? 0);
          return {
            department: p.department_id ? deptName[p.department_id] ?? "—" : "—",
            team: (p.team && String(p.team).trim()) || "—",
            worker_type: p.worker_type,
            yearly_cost: rate * hours * (fte / 100),
          };
        });
        setDepts(buildDepts(resolved));
      })
      .catch(() => setError("Could not load cost summary."));
  }, []);

  if (error) return null;
  if (!depts) return null;

  const grand = depts.reduce(
    (a, d) => ({ total: a.total + d.total, internal: a.internal + d.internal, contractor: a.contractor + d.contractor, cost: a.cost + d.cost }),
    { total: 0, internal: 0, contractor: 0, cost: 0 }
  );

  return (
    <details className="card p-0 overflow-hidden" open>
      <summary className="cursor-pointer bg-slate-50 px-4 py-2 text-sm font-semibold text-slate-700">
        Cost by Department &amp; Team — {fmt(grand.cost)} / yr total
      </summary>
      <div className="overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="text-left text-slate-500">
              <th className="px-4 py-2 font-medium">Department / Team</th>
              <th className="px-4 py-2 font-medium text-right">Members</th>
              <th className="px-4 py-2 font-medium text-right">Internal</th>
              <th className="px-4 py-2 font-medium text-right">Contractor</th>
              <th className="px-4 py-2 font-medium text-right">Yearly Cost</th>
            </tr>
          </thead>
          <tbody>
            {depts.map((d) => (
              <FragmentDept key={d.name} d={d} />
            ))}
            <tr className="border-t-2 border-slate-300 font-bold">
              <td className="px-4 py-2">Grand total</td>
              <td className="px-4 py-2 text-right tabular-nums">{grand.total}</td>
              <td className="px-4 py-2 text-right tabular-nums">{grand.internal}</td>
              <td className="px-4 py-2 text-right tabular-nums">{grand.contractor}</td>
              <td className="px-4 py-2 text-right tabular-nums">{fmt(grand.cost)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>
  );
}

function FragmentDept({ d }: { d: any }) {
  return (
    <>
      <tr className="border-t border-slate-200 bg-slate-50 font-semibold">
        <td className="px-4 py-2">{d.name}</td>
        <td className="px-4 py-2 text-right tabular-nums">{d.total}</td>
        <td className="px-4 py-2 text-right tabular-nums">{d.internal}</td>
        <td className="px-4 py-2 text-right tabular-nums">{d.contractor}</td>
        <td className="px-4 py-2 text-right tabular-nums">{fmt(d.cost)}</td>
      </tr>
      {d.teams.map((t: any) => (
        <tr key={t.name} className="border-t border-slate-100">
          <td className="px-4 py-2 pl-8 text-slate-600">{t.name}</td>
          <td className="px-4 py-2 text-right tabular-nums">{t.total}</td>
          <td className="px-4 py-2 text-right tabular-nums">{t.internal}</td>
          <td className="px-4 py-2 text-right tabular-nums">{t.contractor}</td>
          <td className="px-4 py-2 text-right tabular-nums">{fmt(t.cost)}</td>
        </tr>
      ))}
    </>
  );
}
