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
  // `children` holds secondary child collections keyed by child entity (e.g. a
  // service agreement's invoice_schedule); send them back under the same key.
  docGet: (entity: string, id: string) =>
    request<{ data: Row; lines: Row[]; children?: { [entity: string]: Row[] } }>(
      "GET",
      `/api/v1/docs/${entity}/${id}`
    ),
  docCreate: (entity: string, body: object) =>
    request<{ data: Row }>("POST", `/api/v1/docs/${entity}`, body),
  docUpdate: (entity: string, id: string, body: object) =>
    request<{ data: Row }>("PUT", `/api/v1/docs/${entity}/${id}`, body),
  docConvert: (entity: string, id: string) =>
    request<{ entity: string; data: Row }>("POST", `/api/v1/docs/${entity}/${id}/convert`),
  docDuplicate: (entity: string, id: string) =>
    request<{ entity: string; data: Row }>("POST", `/api/v1/docs/${entity}/${id}/duplicate`),
  related: (entity: string, id: string) =>
    request<RelatedResult>("GET", `/api/v1/entities/${entity}/${id}/related`),
  salesReport: () => request<SalesReport>("GET", "/api/v1/reports/sales"),

  // Team members and invites are managed through `userAdmin` (User Manager).

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
  // Generate from a source document (quotes → Offer, invoices → Service Agreement).
  documentUrlFor: (
    kind: "offer" | "igtr",
    entity: string,
    id: string,
    format: "pdf" | "docx",
    templateId?: string
  ) =>
    `${BASE}/api/v1/documents/${kind}/${entity}/${id}?format=${format}` +
    (templateId ? `&template_id=${templateId}` : ""),

  // Uploadable Offer / Service Agreement (kind "igtr") templates
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
  group?: string; // record a namespaced field reads from, e.g. "Account"
}

export interface TemplateFieldCatalog {
  header: TemplateField[];
  line: TemplateField[];
  schedule?: TemplateField[];
}

type Num = number;
export interface InstallmentRow {
  id: string;
  agreement_id: string;
  agreement: string;
  account: string | null;
  currency: string;
  due_date: string;
  description: string | null;
  amount: Num;
  paid: Num;
  outstanding: Num;
}
// Each section is present only when the caller can access its module.
export interface SalesReport {
  agreements?: {
    by_currency: { currency: string; count: Num; contracted: Num; paid: Num; outstanding: Num }[];
    by_payment_status: { status: string; count: Num; outstanding: Num; overdue: Num }[];
    by_status: { status: string; count: Num; contracted: Num }[];
  };
  overdue_installments?: InstallmentRow[];
  upcoming_installments?: InstallmentRow[];
  quotes?: {
    by_status: { status: string; count: Num; net: Num; grand: Num }[];
    conversion: { quotes: Num; accepted: Num; contracted: Num; accepted_not_contracted: Num; open_expired: Num };
    by_month: { month: string; count: Num; net: Num }[];
  };
  payments?: {
    by_month: { month: string; count: Num; amount: Num }[];
    by_method: { method: string; count: Num; amount: Num }[];
    unallocated: { count: Num; amount: Num };
  };
}

export interface RelatedRecord {
  entity: string;
  id: string;
  title: string;
  status?: string;
}

export interface RelatedResult {
  upstream: { field: string; label: string; record: RelatedRecord }[];
  downstream: { entity: string; field: string; label: string; total: number; rows: RelatedRecord[] }[];
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

// --- Azure (usage sync, fee billing, dashboard) ------------------------------
export const azure = {
  dashboard: (qs: string) => request<AzureDashboard>("GET", `/api/v1/azure/dashboard?${qs}`),
  credential: (id: string) => request<AzureCredential>("GET", `/api/v1/azure/tenants/${id}/credential`),
  setCredential: (id: string, b: { client_secret: string; expires_on?: string }) =>
    request<AzureCredential>("PUT", `/api/v1/azure/tenants/${id}/credential`, b),
  test: (id: string) =>
    request<{ subscriptions: { subscriptionId: string; displayName: string; state: string }[] }>(
      "POST",
      `/api/v1/azure/tenants/${id}/test`
    ),
  sync: (id: string, b: { from?: string; to?: string }) =>
    request<{ status: string; from: string; to: string }>("POST", `/api/v1/azure/tenants/${id}/sync`, b),
  createAgreement: (b: {
    account_id: string;
    period_start: string;
    period_end: string;
    description?: string;
    markup_pct?: number;
    vat_pct?: number;
  }) => request<{ entity: string; data: Row }>("POST", "/api/v1/azure/agreements", b),
  quarters: (id: string) =>
    request<{ quarters: AzureQuarter[]; synced_through: string }>("GET", `/api/v1/azure/agreements/${id}/quarters`),
  refresh: (id: string) => request<{ entity: string; data: Row }>("POST", `/api/v1/azure/agreements/${id}/refresh`),
  invoice: (id: string, period_start: string, force = false) =>
    request<{ entity: string; data: Row; warnings: string[] | null }>(
      "POST",
      `/api/v1/azure/agreements/${id}/invoices`,
      { period_start, force }
    ),
};

// importCsv uploads a Cost Management CSV: mode "preview" compares it with the
// stored usage; "apply" writes it (overwrite = change keys to replace).
export async function azureImportCsv(
  file: File,
  fields: { mode: "preview" | "apply"; azure_tenant_id?: string; overwrite?: string[]; overwrite_all?: boolean }
): Promise<AzureImportResult> {
  const fd = new FormData();
  fd.append("file", file);
  fd.append("mode", fields.mode);
  if (fields.azure_tenant_id) fd.append("azure_tenant_id", fields.azure_tenant_id);
  if (fields.overwrite) fd.append("overwrite", JSON.stringify(fields.overwrite));
  if (fields.overwrite_all) fd.append("overwrite_all", "true");
  const res = await fetch(`${BASE}/api/v1/azure/import`, {
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
  return data as AzureImportResult;
}

export interface AzureImportChange {
  key: string;
  subscription: string;
  date: string;
  existing: number;
  existing_currency: string;
  incoming: number;
  currency: string;
}

export interface AzureImportResult {
  rows: number;
  from: string;
  to: string;
  subscriptions: { key: string; name: string; subscription_id: string; row_id?: string; days: number; total: number; currency: string }[];
  new_days: number;
  unchanged_days: number;
  changes: AzureImportChange[];
  warnings?: string[];
  // apply only
  added_days?: number;
  overwritten_days?: number;
  kept_days?: number;
  new_subscriptions?: number;
  synced_through?: string;
}

export interface AzureCredential {
  has_secret: boolean;
  expires_on?: string;
  updated_at?: string;
}

export interface AzureQuarter {
  label: string;
  period_start: string;
  period_end: string;
  amount: number;
  basis: string;
  usage_complete: boolean;
  ended: boolean;
  installment?: { id: string; due_date: string; amount: number; paid_amount: number; status: string };
  invoice?: { ID: string; Status: string; Net: number; Grand: number; InvoiceNumber: string };
}

export interface AzureAmounts {
  usage: number;
  fee: number;
  forecast_usage: number;
  forecast_fee: number;
  fee_with_markup: number;
}

export interface AzureDashboard {
  from: string;
  to: string;
  synced_through: string;
  markup_pct: number;
  totals: ({ currency: string } & AzureAmounts)[];
  by_month: ({ month: string; currency: string } & AzureAmounts)[];
  by_subscription: ({
    id: string;
    subscription_id: string;
    name: string;
    account_id: string;
    account: string;
    type: string;
    fee_pct: number | null;
    currency: string;
    daily_run_rate: number;
  } & AzureAmounts)[];
  by_account: ({ account_id: string; account: string; currency: string; subscriptions: number } & AzureAmounts)[];
  invoiced: { currency: string; count: number; net: number; invoiced: number; paid: number; due: number; overdue: number; drafts: number }[] | null;
  agreement_forecast: { currency: string; basis: string; quarters: number; net: number; with_markup: number }[] | null;
  warnings?: string[];
}

// --- User manager (admin) ---------------------------------------------------
// Kept separate from `api` so the original team calls keep their shapes.
import type { TeamUser, TeamInvite, PasswordResetLink } from "./types";

export const userAdmin = {
  users: () => request<{ groups: string[]; data: TeamUser[] }>("GET", "/api/v1/team/users"),
  // Partial update; omit allowed_groups to leave access unchanged, null => full.
  updateUser: (
    id: string,
    b: { full_name?: string; email?: string; role?: string; allowed_groups?: string[] | null }
  ) => request<{ data: TeamUser }>("PUT", `/api/v1/team/users/${id}`, b),
  removeUser: (id: string) => request<void>("DELETE", `/api/v1/team/users/${id}`),
  passwordReset: (id: string) =>
    request<PasswordResetLink>("POST", `/api/v1/team/users/${id}/password-reset`),

  invites: () => request<{ data: TeamInvite[] | null }>("GET", "/api/v1/team/invites"),
  invite: (b: { email: string; role: string; allowed_groups: string[] | null }) =>
    request<TeamInvite>("POST", "/api/v1/team/invites", b),
  updateInvite: (id: string, b: { role: string; allowed_groups: string[] | null }) =>
    request<TeamInvite>("PUT", `/api/v1/team/invites/${id}`, b),
  revokeInvite: (id: string) => request<TeamInvite>("POST", `/api/v1/team/invites/${id}/revoke`),
  resendInvite: (id: string) => request<TeamInvite>("POST", `/api/v1/team/invites/${id}/resend`),

  // Public: redeem a reset link.
  resetPassword: (b: { token: string; password: string }) =>
    request<{ ok: boolean }>("POST", "/api/v1/auth/reset-password", b),
};
