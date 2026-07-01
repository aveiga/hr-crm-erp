"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from "react";
import { api, setToken } from "./api";
import type { AuthPayload, Membership, Tenant, User } from "./types";

interface AuthState {
  user: User | null;
  tenant: Tenant | null;
  role: string;
  memberships: Membership[];
  loading: boolean;
  apply: (p: AuthPayload) => void;
  logout: () => void;
  switchTenant: (id: string) => Promise<void>;
  refresh: () => Promise<void>;
}

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [tenant, setTenant] = useState<Tenant | null>(null);
  const [role, setRole] = useState("");
  const [memberships, setMemberships] = useState<Membership[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const me = await api.me();
      setUser(me.user);
      setTenant(me.tenant);
      setRole(me.role);
      setMemberships(me.memberships || []);
    } catch {
      setUser(null);
      setTenant(null);
      setRole("");
      setToken(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const apply = useCallback((p: AuthPayload) => {
    setToken(p.token);
    setUser(p.user);
    setTenant(p.tenant);
    setRole(p.role);
    if (p.memberships) setMemberships(p.memberships);
    setLoading(false);
  }, []);

  const logout = useCallback(() => {
    setToken(null);
    setUser(null);
    setTenant(null);
    setRole("");
    setMemberships([]);
  }, []);

  const switchTenant = useCallback(async (id: string) => {
    const p = await api.switchTenant(id);
    setToken(p.token);
    await (async () => {
      const me = await api.me();
      setUser(me.user);
      setTenant(me.tenant);
      setRole(me.role);
      setMemberships(me.memberships || []);
    })();
  }, []);

  return (
    <Ctx.Provider
      value={{ user, tenant, role, memberships, loading, apply, logout, switchTenant, refresh }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

export function canWrite(role: string) {
  return role === "owner" || role === "admin" || role === "member";
}
export function isAdmin(role: string) {
  return role === "owner" || role === "admin";
}
