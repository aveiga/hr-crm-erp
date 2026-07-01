"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";
import { useMeta } from "@/components/MetaProvider";
import { Icon } from "@/components/Icon";

export default function DashboardPage() {
  const { entities } = useMeta();
  const [data, setData] = useState<any>(null);

  useEffect(() => {
    api.dashboard().then(setData).catch(() => setData({ counts: {} }));
  }, []);

  const money = (n: number) =>
    (n || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Dashboard</h1>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Kpi label="Invoiced (AR)" value={money(data?.ar?.invoiced ?? 0)} tone="brand" />
        <Kpi label="Outstanding (AR)" value={money(data?.ar?.outstanding ?? 0)} tone="amber" />
        <Kpi label="Overdue (AR)" value={money(data?.ar?.overdue ?? 0)} tone="red" />
        <Kpi label="Weighted Pipeline" value={money(data?.pipeline_weighted ?? 0)} tone="green" />
      </div>

      <h2 className="text-lg font-semibold pt-2">Records</h2>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
        {entities.filter((e) => !e.hidden).map((e) => (
          <Link key={e.key} href={`/m/${e.key}`} className="card p-4 hover:border-brand-400 transition-colors">
            <div className="flex items-center gap-2 text-slate-500">
              <Icon name={e.icon} className="h-4 w-4" />
              <span className="text-xs uppercase tracking-wide">{e.group}</span>
            </div>
            <div className="mt-2 text-2xl font-bold">{data?.counts?.[e.key] ?? 0}</div>
            <div className="text-sm text-slate-500">{e.plural}</div>
          </Link>
        ))}
      </div>
    </div>
  );
}

function Kpi({ label, value, tone }: { label: string; value: string; tone: string }) {
  const tones: globalThis.Record<string, string> = {
    brand: "text-brand-600",
    amber: "text-amber-600",
    red: "text-red-600",
    green: "text-emerald-600",
  };
  return (
    <div className="card p-5">
      <div className="text-sm text-slate-500">{label}</div>
      <div className={`mt-1 text-2xl font-bold ${tones[tone] || ""}`}>{value}</div>
    </div>
  );
}
