// Minimal typed fetch client for the Go API. The bearer token is read from
// localStorage so the whole app is a static SPA the backend can serve.
import type { AuthPayload, Entity, ListResult, Record as Row } from "./types";

declare global {
  interface Window {
    __ENV__?: { apiUrl?: string };
  }
}

// Resolved at runtime from the server-injected window.__ENV__ (see the root
// layout) so a single build runs in any environment. Falls back to the
// build-time NEXT_PUBLIC var (handy for `next dev`), then localhost.
function resolveBase(): string {
  if (typeof window !== "undefined" && window.__ENV__?.apiUrl) {
    return window.__ENV__.apiUrl;
  }
  return process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
}

const BASE = resolveBase();

const TOKEN_KEY = "hrcrm_token";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string | null) {
  if (typeof window === "undefined") return;
  if (token) window.localStorage.setItem(TOKEN_KEY, token);
  else window.localStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const data = text ? JSON.parse(text) : null;

  if (!res.ok) {
    const err = data?.error || {};
    throw new ApiError(res.status, err.code || "error", err.message || res.statusText);
  }
  return data as T;
}

export const api = {
  // Auth
  signup: (b: { email: string; password: string; full_name?: string; organization?: string }) =>
    request<AuthPayload>("POST", "/api/v1/auth/signup", b),
  login: (b: { email: string; password: string; tenant_id?: string }) =>
    request<AuthPayload>("POST", "/api/v1/auth/login", b),
  acceptInvite: (b: { token: string; full_name?: string; password?: string }) =>
    request<AuthPayload>("POST", "/api/v1/auth/accept-invite", b),
  me: () => request<{ user: any; tenant: any; role: string; memberships: any[] }>("GET", "/api/v1/auth/me"),
  switchTenant: (tenant_id: string) =>
    request<AuthPayload>("POST", "/api/v1/auth/switch-tenant", { tenant_id }),

  // Metadata + dashboard
  meta: () => request<{ entities: Entity[] }>("GET", "/api/v1/meta"),
  dashboard: () => request<any>("GET", "/api/v1/dashboard"),

  // Entities
  list: (entity: string, qs: string = "") =>
    request<ListResult>("GET", `/api/v1/entities/${entity}${qs ? "?" + qs : ""}`),
  get: (entity: string, id: string) =>
    request<{ data: Row }>("GET", `/api/v1/entities/${entity}/${id}`),
  create: (entity: string, body: Row | object) =>
    request<{ data: Row }>("POST", `/api/v1/entities/${entity}`, body),
  update: (entity: string, id: string, body: object) =>
    request<{ data: Row }>("PUT", `/api/v1/entities/${entity}/${id}`, body),
  remove: (entity: string, id: string) =>
    request<void>("DELETE", `/api/v1/entities/${entity}/${id}`),

  // Documents (header + line items): quotes, sales_orders, invoices, etc.
  docGet: (entity: string, id: string) =>
    request<{ data: Row; lines: Row[] }>("GET", `/api/v1/docs/${entity}/${id}`),
  docCreate: (entity: string, body: object) =>
    request<{ data: Row }>("POST", `/api/v1/docs/${entity}`, body),
  docUpdate: (entity: string, id: string, body: object) =>
    request<{ data: Row }>("PUT", `/api/v1/docs/${entity}/${id}`, body),
  docConvert: (entity: string, id: string) =>
    request<{ entity: string; data: Row }>("POST", `/api/v1/docs/${entity}/${id}/convert`),

  // Team
  members: () => request<{ data: any[] }>("GET", "/api/v1/team/members"),
  invites: () => request<{ data: any[] }>("GET", "/api/v1/team/invites"),
  invite: (b: { email: string; role: string }) =>
    request<any>("POST", "/api/v1/team/invites", b),

  // Permissions matrix
  permissions: () =>
    request<{ groups: string[]; members: MemberPerm[] }>("GET", "/api/v1/team/permissions"),
  setPermissions: (user_id: string, groups: string[] | null) =>
    request<{ ok: boolean }>("PUT", "/api/v1/team/permissions", { user_id, groups }),

  // Per-agent API tokens. A member manages their own; admins additionally see
  // and revoke every token in the tenant.
  tokens: () => request<{ tokens: ApiToken[] }>("GET", "/api/v1/tokens"),
  createToken: (b: { name: string; read_only?: boolean; expires_in_days?: number }) =>
    request<ApiTokenCreated>("POST", "/api/v1/tokens", b),
  revokeToken: (id: string) => request<void>("DELETE", `/api/v1/tokens/${id}`),
  teamTokens: () => request<{ tokens: TenantApiToken[] }>("GET", "/api/v1/team/tokens"),
  revokeTeamToken: (id: string) => request<void>("DELETE", `/api/v1/team/tokens/${id}`),

  // Documents
  documentUrl: (kind: "offer" | "igtr", invoiceId: string, format: "pdf" | "docx", templateId?: string) =>
    `${BASE}/api/v1/documents/${kind}/${invoiceId}?format=${format}` +
    (templateId ? `&template_id=${templateId}` : ""),

  // Uploadable Offer/IGTR templates
  templates: (kind?: "offer" | "igtr") =>
    request<{ data: TemplateMeta[] }>("GET", `/api/v1/templates${kind ? "?kind=" + kind : ""}`),
  templateFields: () => request<TemplateFieldCatalog>("GET", "/api/v1/templates/fields"),
  deleteTemplate: (id: string) => request<void>("DELETE", `/api/v1/templates/${id}`),
  uploadTemplate: async (kind: "offer" | "igtr", name: string, file: File): Promise<TemplateMeta> => {
    const fd = new FormData();
    fd.append("kind", kind);
    fd.append("name", name);
    fd.append("file", file);
    const res = await fetch(`${BASE}/api/v1/templates`, {
      method: "POST",
      headers: getToken() ? { Authorization: `Bearer ${getToken()}` } : {},
      body: fd,
    });
    const text = await res.text();
    const data = text ? JSON.parse(text) : null;
    if (!res.ok) {
      const err = data?.error || {};
      throw new ApiError(res.status, err.code || "error", err.message || res.statusText);
    }
    return data as TemplateMeta;
  },
};

export interface TemplateMeta {
  id: string;
  kind: "offer" | "igtr";
  name: string;
  filename: string;
  created_at: string;
}

export interface TemplateField {
  placeholder: string;
  name: string;
  description: string;
}

export interface TemplateFieldCatalog {
  header: TemplateField[];
  line: TemplateField[];
}

export interface MemberPerm {
  user_id: string;
  email: string;
  full_name: string;
  role: string;
  allowed_groups: string[] | null; // null => all sections
}

export interface ApiToken {
  id: string;
  name: string;
  read_only: boolean;
  created_at: string;
  last_used_at: string | null;
  expires_at: string | null;
}

// ApiTokenCreated additionally carries the plaintext token, shown exactly once.
export interface ApiTokenCreated extends ApiToken {
  token: string;
}

// TenantApiToken is the admin view: a token plus its owner.
export interface TenantApiToken extends ApiToken {
  user_id: string;
  user_email: string;
  user_name: string;
}

export { BASE };
