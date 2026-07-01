"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";
import { OrgChart, csvFor, type OrgPerson } from "@/components/OrgChart";
import { Icon } from "@/components/Icon";

export default function OrgPage() {
  const [people, setPeople] = useState<OrgPerson[] | null>(null);
  const [error, setError] = useState("");
  const svgRef = useRef<SVGSVGElement>(null);

  useEffect(() => {
    Promise.all([api.list("people", "page_size=1000"), api.list("departments", "page_size=1000")])
      .then(([pp, dd]) => {
        // Resolve the department reference to its name for grouping.
        const deptName: Record<string, string> = {};
        for (const d of dd.data) deptName[d.id] = String(d.name ?? "—");
        const resolved: OrgPerson[] = pp.data.map((p: any) => {
          const hourly_rate = Number(p.hourly_rate ?? 0);
          const fte_pct = Number(p.fte_pct ?? 0);
          const yearly_hours = Number(p.yearly_hours ?? 0);
          return {
            full_name: p.full_name,
            role: p.role,
            department: p.department_id ? deptName[p.department_id] ?? "—" : "—",
            team: (p.team && String(p.team).trim()) || "—",
            worker_type: p.worker_type,
            team_leader: p.team_leader,
            hourly_rate,
            fte_pct,
            yearly_hours,
            yearly_cost: hourly_rate * yearly_hours * (fte_pct / 100),
          };
        });
        setPeople(resolved);
      })
      .catch((e) =>
        setError(e instanceof ApiError && e.status === 403 ? "You don't have access to the HR section." : "Failed to load people.")
      );
  }, []);

  function triggerDownload(url: string, name: string) {
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
  }

  function exportPng() {
    const svg = svgRef.current;
    if (!svg) return;
    const xml = new XMLSerializer().serializeToString(svg);
    const src = "data:image/svg+xml;base64," + btoa(unescape(encodeURIComponent(xml)));
    const img = new Image();
    img.onload = () => {
      const scale = 2;
      const w = svg.width.baseVal.value;
      const h = svg.height.baseVal.value;
      const canvas = document.createElement("canvas");
      canvas.width = w * scale;
      canvas.height = h * scale;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.scale(scale, scale);
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(0, 0, w, h);
      ctx.drawImage(img, 0, 0);
      canvas.toBlob((b) => {
        if (b) triggerDownload(URL.createObjectURL(b), "organization.png");
      }, "image/png");
    };
    img.src = src;
  }

  function exportCsv() {
    if (!people) return;
    const blob = new Blob([csvFor(people)], { type: "text/csv;charset=utf-8" });
    triggerDownload(URL.createObjectURL(blob), "organization.csv");
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Link href="/m/people" className="text-slate-400 hover:text-slate-600">
            <Icon name="arrow-left" className="h-5 w-5" />
          </Link>
          <h1 className="text-2xl font-bold">Organization</h1>
        </div>
        <div className="flex gap-2">
          <button className="btn-secondary" onClick={exportCsv} disabled={!people}>
            <Icon name="table" className="h-4 w-4" /> Export CSV
          </button>
          <button className="btn-primary" onClick={exportPng} disabled={!people}>
            <Icon name="image" className="h-4 w-4" /> Export image
          </button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-4 text-xs text-slate-500">
        <span className="inline-flex items-center gap-1">
          <span className="inline-block h-3 w-3 rounded border border-amber-500 bg-amber-100" /> Team Leader (★)
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="font-semibold text-blue-600">INT</span> Internal
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="font-semibold text-violet-600">EXT</span> Contractor
        </span>
      </div>

      {error && <p className="text-red-600">{error}</p>}
      {!error && !people && <p className="text-slate-400">Loading…</p>}
      {people && people.length === 0 && (
        <p className="text-slate-400">No people yet. Add people (with a Department and Team) to see the chart.</p>
      )}

      {people && people.length > 0 && (
        <div className="card overflow-auto p-4">
          <OrgChart ref={svgRef} people={people} />
        </div>
      )}
    </div>
  );
}
