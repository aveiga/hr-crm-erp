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
  read_only?: boolean;
  in_list?: boolean;
  help?: string;
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
