"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useMeta } from "./MetaProvider";
import { Icon } from "./Icon";
import { useAuth, isAdmin } from "@/lib/auth";
import type { Entity } from "@/lib/types";

const GROUP_ORDER = ["Master", "HR", "CRM", "ERP", "Azure", "AP"];

export function Sidebar() {
  const { entities } = useMeta();
  const { role } = useAuth();
  const pathname = usePathname();

  const groups: globalThis.Record<string, Entity[]> = {};
  for (const e of entities) {
    if (e.hidden) continue; // line-item entities are managed via their parent
    (groups[e.group] ||= []).push(e);
  }
  const groupKeys = [
    ...GROUP_ORDER.filter((g) => groups[g]),
    ...Object.keys(groups).filter((g) => !GROUP_ORDER.includes(g)),
  ];

  return (
    <aside className="w-60 shrink-0 border-r border-slate-200 bg-white h-screen sticky top-0 overflow-y-auto">
      <div className="px-4 py-4 border-b border-slate-200">
        <div className="font-semibold text-brand-700">HR · CRM · ERP</div>
        <div className="text-xs text-slate-400">Teams · Leads · Offers · Service Agreements</div>
      </div>
      <nav className="p-2 text-sm">
        <NavLink href="/dashboard" active={pathname === "/dashboard"} icon="layout-dashboard">
          Dashboard
        </NavLink>
        {/* Sales reporting covers quotes / agreements / payments (ERP). */}
        {entities.some((e) => ["quotes", "invoices", "payments"].includes(e.key)) && (
          <NavLink href="/reports" active={pathname.startsWith("/reports")} icon="chart-column">
            Reports
          </NavLink>
        )}
        {entities.some((e) => e.key === "azure_subscriptions") && (
          <NavLink href="/azure" active={pathname.startsWith("/azure")} icon="cloud">
            Azure Dashboard
          </NavLink>
        )}

        {groupKeys.map((g) => (
          <div key={g} className="mt-3">
            <div className="px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-slate-400">
              {g}
            </div>
            {groups[g].map((e) => (
              <NavLink
                key={e.key}
                href={`/m/${e.key}`}
                active={pathname.startsWith(`/m/${e.key}`)}
                icon={e.icon}
              >
                {e.plural}
              </NavLink>
            ))}
          </div>
        ))}

        {isAdmin(role) && (
          <div className="mt-3">
            <div className="px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-slate-400">
              Settings
            </div>
            <NavLink href="/users" active={pathname.startsWith("/users")} icon="users">
              Users
            </NavLink>
            <NavLink href="/team" active={pathname.startsWith("/team")} icon="user-cog">
              Team
            </NavLink>
          </div>
        )}
      </nav>
    </aside>
  );
}

function NavLink({
  href,
  active,
  icon,
  children,
}: {
  href: string;
  active: boolean;
  icon: string;
  children: React.ReactNode;
}) {
  return (
    <Link
      href={href}
      className={`flex items-center gap-2 rounded-md px-3 py-2 ${
        active ? "bg-brand-50 text-brand-700 font-medium" : "text-slate-600 hover:bg-slate-100"
      }`}
    >
      <Icon name={icon} className="h-4 w-4" />
      <span className="truncate">{children}</span>
    </Link>
  );
}
