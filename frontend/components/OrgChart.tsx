"use client";

import { forwardRef, useMemo } from "react";

export interface OrgPerson {
  full_name?: string;
  role?: string;
  department?: string;
  team?: string;
  worker_type?: string;
  team_leader?: boolean;
  // Cost inputs (resolved by the caller from the person + their team config).
  hourly_rate?: number;
  fte_pct?: number;
  yearly_hours?: number;
  yearly_cost?: number;
}

interface Counts {
  total: number;
  internal: number;
  contractor: number;
  cost: number;
}
interface Team extends Counts {
  name: string;
  members: OrgPerson[];
}
interface Dept extends Counts {
  name: string;
  teams: Team[];
}

// Layout constants (px). Everything is deterministic so the SVG rasterizes
// identically when exported to PNG.
const COLS = 4;
const TW = 240; // team box width
const GAP = 16;
const MH = 34; // member card height
const TEAM_HEAD = 58; // team header (name + counts + yearly cost)
const TEAM_PADB = 10;
const DEPT_HEAD = 44;
const DEPT_PAD = 16;
const DEPT_GAP = 24;
const PAD = 24;
const TITLE_H = 56;
const SUM_LINE = 22;

function countOf(members: OrgPerson[]): Counts {
  let internal = 0,
    contractor = 0,
    cost = 0;
  for (const m of members) {
    if (m.worker_type === "Internal") internal++;
    else if (m.worker_type === "Contractor") contractor++;
    cost += m.yearly_cost || 0;
  }
  return { total: members.length, internal, contractor, cost };
}

function fmtCost(n: number) {
  return Math.round(n || 0).toLocaleString();
}

export function buildDepts(people: OrgPerson[]): Dept[] {
  const dmap = new Map<string, Map<string, OrgPerson[]>>();
  for (const p of people) {
    const d = (p.department || "—").trim() || "—";
    const t = (p.team || "—").trim() || "—";
    if (!dmap.has(d)) dmap.set(d, new Map());
    const tm = dmap.get(d)!;
    if (!tm.has(t)) tm.set(t, []);
    tm.get(t)!.push(p);
  }
  const depts: Dept[] = [];
  for (const dName of [...dmap.keys()].sort()) {
    const tm = dmap.get(dName)!;
    const teams: Team[] = [];
    const deptMembers: OrgPerson[] = [];
    for (const tName of [...tm.keys()].sort()) {
      const members = tm.get(tName)!.slice().sort((a, b) => {
        // Leaders first, then by name.
        if (!!a.team_leader !== !!b.team_leader) return a.team_leader ? -1 : 1;
        return (a.full_name || "").localeCompare(b.full_name || "");
      });
      deptMembers.push(...members);
      teams.push({ name: tName, members, ...countOf(members) });
    }
    depts.push({ name: dName, teams, ...countOf(deptMembers) });
  }
  return depts;
}

function trunc(s: string, n: number) {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
function label(c: Counts) {
  return `${c.total} total · ${c.internal} internal · ${c.contractor} contractor`;
}

// csvFor returns the underlying data as CSV (one row per person).
export function csvFor(people: OrgPerson[]): string {
  const esc = (v: string) => `"${(v || "").replace(/"/g, '""')}"`;
  const rows = [
    ["Department", "Team", "Name", "Role", "Worker Type", "Team Leader", "Hourly Rate", "FTE %", "Yearly Hours", "Yearly Cost"],
  ];
  const depts = buildDepts(people);
  for (const d of depts)
    for (const t of d.teams)
      for (const m of t.members)
        rows.push([
          d.name,
          t.name,
          m.full_name || "",
          m.role || "",
          m.worker_type || "",
          m.team_leader ? "Yes" : "No",
          String(m.hourly_rate ?? ""),
          String(m.fte_pct ?? ""),
          String(m.yearly_hours ?? ""),
          String(Math.round(m.yearly_cost || 0)),
        ]);
  // Summary section: totals per department and team, then grand total.
  rows.push([]);
  rows.push(["Summary", "", "", "", "", "", "", "Total", "Members int/ext", "Yearly Cost"]);
  for (const d of depts) {
    rows.push([`Department: ${d.name}`, "", "", "", "", "", "", String(d.total), `${d.internal}/${d.contractor}`, String(Math.round(d.cost))]);
    for (const t of d.teams)
      rows.push([`  Team: ${t.name}`, "", "", "", "", "", "", String(t.total), `${t.internal}/${t.contractor}`, String(Math.round(t.cost))]);
  }
  const g = countOf(people);
  rows.push(["GRAND TOTAL", "", "", "", "", "", "", String(g.total), `${g.internal}/${g.contractor}`, String(Math.round(g.cost))]);
  return rows.map((r) => r.map(esc).join(",")).join("\n");
}

// OrgChart renders the whole organization as a single self-contained SVG so it
// can be exported to PNG without any external dependency.
export const OrgChart = forwardRef<SVGSVGElement, { people: OrgPerson[] }>(function OrgChart(
  { people },
  ref
) {
  const { width, height, els } = useMemo(() => layout(people), [people]);
  return (
    <svg
      ref={ref}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      xmlns="http://www.w3.org/2000/svg"
      style={{ fontFamily: "Arial, Helvetica, sans-serif", background: "#ffffff" }}
    >
      <rect x={0} y={0} width={width} height={height} fill="#ffffff" />
      {els}
    </svg>
  );
});

function layout(people: OrgPerson[]) {
  const depts = buildDepts(people);
  const els: React.ReactNode[] = [];
  let key = 0;
  const k = () => `n${key++}`;

  const grand = countOf(people);

  // Title.
  els.push(
    <text key={k()} x={PAD} y={34} fontSize={24} fontWeight={700} fill="#0f172a">
      Organization Chart
    </text>
  );
  els.push(
    <text key={k()} x={PAD} y={50} fontSize={12} fill="#64748b">
      {`${label(grand)} · total ${fmtCost(grand.cost)} / yr`}
    </text>
  );

  let maxW = 600;
  let y = PAD + TITLE_H;

  for (const d of depts) {
    const cols = Math.max(1, Math.min(d.teams.length, COLS));
    const rows = Math.ceil(d.teams.length / cols);
    const teamH = (t: Team) => TEAM_HEAD + t.members.length * MH + TEAM_PADB;
    const rowHeights: number[] = [];
    for (let r = 0; r < rows; r++) {
      let h = 0;
      for (let c = 0; c < cols; c++) {
        const t = d.teams[r * cols + c];
        if (t) h = Math.max(h, teamH(t));
      }
      rowHeights.push(h);
    }
    const contentH = rowHeights.reduce((a, b) => a + b, 0) + (rows - 1) * GAP;
    const contentW = cols * TW + (cols - 1) * GAP;
    const deptW = contentW + DEPT_PAD * 2;
    const deptH = DEPT_HEAD + DEPT_PAD + contentH + DEPT_PAD;
    const deptX = PAD;
    const deptY = y;
    maxW = Math.max(maxW, deptX + deptW);

    // Department box + header.
    els.push(
      <rect key={k()} x={deptX} y={deptY} width={deptW} height={deptH} rx={10} fill="#ffffff" stroke="#cbd5e1" />
    );
    els.push(<rect key={k()} x={deptX} y={deptY} width={deptW} height={DEPT_HEAD} rx={10} fill="#264a9e" />);
    els.push(<rect key={k()} x={deptX} y={deptY + DEPT_HEAD - 12} width={deptW} height={12} fill="#264a9e" />);
    els.push(
      <text key={k()} x={deptX + 14} y={deptY + 27} fontSize={16} fontWeight={700} fill="#ffffff">
        {trunc(d.name, 40)}
      </text>
    );
    els.push(
      <text key={k()} x={deptX + deptW - 14} y={deptY + 19} fontSize={12} fill="#c7d2fe" textAnchor="end">
        {label(d)}
      </text>
    );
    els.push(
      <text key={k()} x={deptX + deptW - 14} y={deptY + 34} fontSize={12} fontWeight={700} fill="#a7f3d0" textAnchor="end">
        {`${fmtCost(d.cost)} / yr`}
      </text>
    );

    // Teams.
    const areaX = deptX + DEPT_PAD;
    const areaY = deptY + DEPT_HEAD + DEPT_PAD;
    d.teams.forEach((t, i) => {
      const c = i % cols;
      const r = Math.floor(i / cols);
      const tx = areaX + c * (TW + GAP);
      const ty = areaY + rowHeights.slice(0, r).reduce((a, b) => a + b, 0) + r * GAP;
      const th = teamH(t);

      els.push(<rect key={k()} x={tx} y={ty} width={TW} height={th} rx={8} fill="#ffffff" stroke="#cbd5e1" />);
      els.push(<rect key={k()} x={tx} y={ty} width={TW} height={TEAM_HEAD} rx={8} fill="#eef4ff" />);
      els.push(<rect key={k()} x={tx} y={ty + TEAM_HEAD - 10} width={TW} height={10} fill="#eef4ff" />);
      els.push(
        <text key={k()} x={tx + 12} y={ty + 19} fontSize={14} fontWeight={700} fill="#264a9e">
          {trunc(t.name, 26)}
        </text>
      );
      els.push(
        <text key={k()} x={tx + 12} y={ty + 35} fontSize={10.5} fill="#64748b">
          {`${t.total} · ${t.internal} int · ${t.contractor} ext`}
        </text>
      );
      els.push(
        <text key={k()} x={tx + 12} y={ty + 50} fontSize={11} fontWeight={700} fill="#0f766e">
          {`${fmtCost(t.cost)} / yr`}
        </text>
      );

      // Members.
      t.members.forEach((m, mi) => {
        const my = ty + TEAM_HEAD + mi * MH;
        const leader = !!m.team_leader;
        if (leader) {
          els.push(<rect key={k()} x={tx + 6} y={my + 3} width={TW - 12} height={MH - 4} rx={6} fill="#fef3c7" stroke="#f59e0b" />);
        }
        const nameX = tx + 14;
        els.push(
          <text key={k()} x={nameX} y={my + 16} fontSize={12.5} fontWeight={leader ? 700 : 500} fill={leader ? "#92400e" : "#0f172a"}>
            {(leader ? "★ " : "") + trunc(m.full_name || "—", leader ? 22 : 26)}
          </text>
        );
        if (m.role) {
          els.push(
            <text key={k()} x={nameX} y={my + 28} fontSize={10} fill={leader ? "#b45309" : "#64748b"}>
              {trunc(m.role, 30)}
            </text>
          );
        }
        // Worker-type tag.
        const tag = m.worker_type === "Internal" ? "INT" : m.worker_type === "Contractor" ? "EXT" : "";
        if (tag) {
          els.push(
            <text key={k()} x={tx + TW - 12} y={my + 16} fontSize={9.5} fontWeight={700} textAnchor="end" fill={tag === "INT" ? "#2563eb" : "#7c3aed"}>
              {tag}
            </text>
          );
        }
      });
    });

    y = deptY + deptH + DEPT_GAP;
  }

  // Summary block.
  const summaryX = PAD;
  const summaryY = y;
  const summaryLines = 1 + depts.length + 1;
  const summaryH = 16 + summaryLines * SUM_LINE + 16;
  els.push(<rect key={k()} x={summaryX} y={summaryY} width={maxW - PAD + 0} height={summaryH} rx={10} fill="#f8fafc" stroke="#cbd5e1" />);
  els.push(
    <text key={k()} x={summaryX + 14} y={summaryY + 24} fontSize={14} fontWeight={700} fill="#0f172a">
      Summary
    </text>
  );
  let sy = summaryY + 24 + SUM_LINE;
  for (const d of depts) {
    els.push(
      <text key={k()} x={summaryX + 14} y={sy} fontSize={12} fill="#334155">
        {`${d.name}: ${d.total} members (${d.internal} internal, ${d.contractor} contractors) — ${d.teams.length} team(s) — ${fmtCost(d.cost)} / yr`}
      </text>
    );
    sy += SUM_LINE;
  }
  els.push(
    <text key={k()} x={summaryX + 14} y={sy} fontSize={13} fontWeight={700} fill="#0f172a">
      {`GRAND TOTAL: ${grand.total} people — ${grand.internal} internal, ${grand.contractor} contractors — ${fmtCost(grand.cost)} / yr`}
    </text>
  );

  const width = maxW + PAD;
  const height = summaryY + summaryH + PAD;
  return { width, height, els };
}
