import type { Field } from "./types";

// formatValue renders a stored value for display in tables and detail views.
export function formatValue(field: Field, value: any): string {
  if (value === null || value === undefined || value === "") return "";
  switch (field.type) {
    case "money":
      return Number(value).toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      });
    case "percent":
      return `${Number(value)}%`;
    case "number":
    case "int":
      return String(value);
    case "bool":
      return value ? "Yes" : "No";
    case "date":
    case "datetime":
      return formatDate(value);
    default:
      return String(value);
  }
}

export function formatDate(value: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (isNaN(d.getTime())) return value;
  return d.toISOString().slice(0, 10);
}

// toInputValue converts a stored value to what an <input> expects.
export function toInputValue(field: Field, value: any): any {
  if (value === null || value === undefined) return field.type === "bool" ? false : "";
  if (field.type === "date" || field.type === "datetime") return formatDate(value);
  if (field.type === "bool") return Boolean(value);
  return value;
}
