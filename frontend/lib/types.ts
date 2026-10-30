export type FieldType =
  | "text"
  | "longtext"
  | "int"
  | "number"
  | "money"
  | "percent"
  | "date"
  | "datetime"
  | "bool"
  | "select"
  | "ref"
  | "lookup"
  | "email";

export interface Field {
  key: string;
  label: string;
  type: FieldType;
  required?: boolean;
  options?: string[];
  ref?: string; // ref/lookup target entity key
  ref_label?: string;
  value_field?: string; // lookup: stored field on target
  label_field?: string; // lookup: displayed field on target
  filter_by?: string; // sibling field key whose value filters options
  filter_field?: string; // target field matched against the sibling value
  option_filter_field?: string; // target field matched against a constant
  option_filter_value?: string; // constant value for the static option filter
  fill_from?: string; // sibling ref; while set, this field mirrors the ref'd row's fill_field (read-only)
  fill_field?: string;
  disabled_when?: string; // sibling field; when it equals disabled_value this field is disabled + cleared
  disabled_value?: string;
  default?: string; // pre-filled on new records
  read_only?: boolean;
  in_list?: boolean;
  help?: string;
}

// A saved list filter (query uses the list endpoint's filter params).
export interface View {
  label: string;
  query: string;
}

export interface Child {
  entity: string;
  fk: string;
  label: string;
}

export interface Entity {
  key: string;
  label: string;
  plural: string;
  group: string;
  title_field: string;
  icon: string;
  hidden?: boolean;
  read_only?: boolean;
  children?: Child[];
  views?: View[];
  columns?: string[]; // list view columns, in order (else first in-list fields + totals)
  fields: Field[];
}

export interface Membership {
  tenant_id: string;
  tenant_name: string;
  tenant_slug: string;
  role: string;
}

export interface User {
  id: string;
  email: string;
  full_name: string;
}

export interface Tenant {
  id: string;
  name: string;
  slug: string;
}

export interface AuthPayload {
  token: string;
  user: User;
  tenant: Tenant;
  role: string;
  memberships?: Membership[];
}

export type Record = { [k: string]: any; id: string };

export interface ListResult {
  data: Record[];
  total: number;
  page: number;
  page_size: number;
}

// --- User manager (/users) --------------------------------------------------

export type Role = "owner" | "admin" | "member" | "viewer";

// TeamUser is a member row from GET /api/v1/team/users.
export interface TeamUser {
  user_id: string;
  email: string;
  full_name: string;
  role: Role;
  allowed_groups: string[] | null; // null => all sections
  joined_at: string;
  last_login_at: string | null;
  // false when the user also belongs to another organization: their email,
  // name and password can then only be changed by themselves.
  identity_editable: boolean;
}

export type InviteStatus = "pending" | "accepted" | "expired" | "revoked";

export interface TeamInvite {
  id: string;
  tenant_id: string;
  email: string;
  role: Role;
  token: string;
  accepted: boolean;
  allowed_groups: string[] | null;
  status: InviteStatus;
  created_at: string;
  expires_at: string | null; // null => never expires
  accepted_at: string | null;
  revoked_at: string | null;
  last_error: string | null;
  failed_attempts: number;
  last_attempt_at: string | null;
}

export interface PasswordResetLink {
  user_id: string;
  token: string;
  path: string; // e.g. /reset-password?token=…
  expires_at: string;
}
