"use client";

import { useAuth } from "@/lib/auth";

export function Topbar() {
  const { user, tenant, role, memberships, switchTenant, logout } = useAuth();

  return (
    <header className="flex items-center justify-between border-b border-slate-200 bg-white px-6 py-3">
      <div className="flex items-center gap-2">
        {memberships.length > 1 ? (
          <select
            className="select w-auto"
            value={tenant?.id || ""}
            onChange={(e) => switchTenant(e.target.value)}
          >
            {memberships.map((m) => (
              <option key={m.tenant_id} value={m.tenant_id}>
                {m.tenant_name}
              </option>
            ))}
          </select>
        ) : (
          <div className="font-medium">{tenant?.name}</div>
        )}
      </div>
      <div className="flex items-center gap-4">
        <div className="text-right">
          <div className="text-sm font-medium">{user?.full_name || user?.email}</div>
          <div className="text-xs capitalize text-slate-400">{role}</div>
        </div>
        <button className="btn-secondary" onClick={logout}>
          Sign out
        </button>
      </div>
    </header>
  );
}
