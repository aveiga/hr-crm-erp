"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import Link from "next/link";
import {
  api,
  ApiError,
  type TemplateMeta,
  type ApiToken,
  type ApiTokenCreated,
  type TenantApiToken,
  type TemplateField,
  type TemplateFieldCatalog,
} from "@/lib/api";
import { useAuth, isAdmin } from "@/lib/auth";
import { formatDate } from "@/lib/format";
import { Icon } from "@/components/Icon";

export default function TeamPage() {
  const { role, user } = useAuth();
  const admin = isAdmin(role);

  return (
    <div className="max-w-3xl space-y-8">
      <h1 className="text-2xl font-bold">Team</h1>

      {admin && (
        <>
          <section className="card flex flex-wrap items-center justify-between gap-3 p-4">
            <div>
              <h2 className="text-lg font-semibold">Users</h2>
              <p className="text-sm text-slate-500">
                Members, invites, roles, section access and password resets now live in the User
                Manager.
              </p>
            </div>
            <Link href="/users" className="btn-primary">
              <Icon name="users" className="h-4 w-4" /> Manage users
            </Link>
          </section>

          <DocumentTemplatesSection />
        </>
      )}

      <ApiTokensSection admin={admin} currentUserId={user?.id} />
    </div>
  );
}

// DocumentTemplatesSection lets admins upload and manage the Word templates
// used to generate Offers and Service Agreements (template kind "igtr").
function DocumentTemplatesSection() {
  const [templates, setTemplates] = useState<TemplateMeta[]>([]);
  const [kind, setKind] = useState<"offer" | "igtr">("offer");
  const [name, setName] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [showFields, setShowFields] = useState(false);

  function load() {
    api.templates().then((r) => setTemplates(r.data)).catch(() => setTemplates([]));
  }
  useEffect(load, []);

  async function upload(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (!file) {
      setError("Choose a .docx file");
      return;
    }
    setBusy(true);
    try {
      await api.uploadTemplate(kind, name || file.name, file);
      setName("");
      setFile(null);
      (document.getElementById("tpl-file") as HTMLInputElement | null)?.value &&
        ((document.getElementById("tpl-file") as HTMLInputElement).value = "");
      load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Upload failed");
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string) {
    if (!confirm("Delete this template?")) return;
    await api.deleteTemplate(id).catch(() => {});
    load();
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Document templates</h2>
        <button type="button" className="btn-secondary" onClick={() => setShowFields(true)}>
          <Icon name="list" className="h-4 w-4" /> Available fields
        </button>
      </div>
      {showFields && <TemplateFieldsModal onClose={() => setShowFields(false)} />}
      <p className="text-sm text-slate-500">
        Upload Word <code>.docx</code> templates for Offers (generated from Quotes) and Service
        Agreements. Any field of the document, its quote / sales order / agreement, account, contact
        and our legal entity is available as e.g. <code>{"{{account.billing_email}}"}</code>. A table
        row containing <code>{"{{line.description}}"}</code>, <code>{"{{line.line_net}}"}</code>… repeats
        per line item, and one containing <code>{"{{schedule.invoice_date}}"}</code>,{" "}
        <code>{"{{schedule.amount}}"}</code>… repeats per invoicing-schedule installment. See
        “Available fields”. When generating, pick a template from the dropdown (or the built-in default).
      </p>

      <form onSubmit={upload} className="card flex flex-wrap items-end gap-3 p-4">
        <div>
          <label className="label">Type</label>
          <select className="select" value={kind} onChange={(e) => setKind(e.target.value as "offer" | "igtr")}>
            <option value="offer">Offer</option>
            <option value="igtr">Service Agreement</option>
          </select>
        </div>
        <div className="flex-1 min-w-[180px]">
          <label className="label">Name</label>
          <input className="input" placeholder="e.g. Standard Offer 2026" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div>
          <label className="label">File (.docx)</label>
          <input
            id="tpl-file"
            className="block text-sm"
            type="file"
            accept=".docx,application/vnd.openxmlformats-officedocument.wordprocessingml.document"
            onChange={(e) => setFile(e.target.files?.[0] || null)}
          />
        </div>
        <button className="btn-primary" disabled={busy}>
          <Icon name="upload" className="h-4 w-4" /> {busy ? "Uploading…" : "Upload"}
        </button>
      </form>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <div className="card overflow-hidden">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="bg-slate-50 text-left text-slate-500">
              <th className="px-4 py-2 font-medium">Type</th>
              <th className="px-4 py-2 font-medium">Name</th>
              <th className="px-4 py-2 font-medium">File</th>
              <th className="px-4 py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {templates.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-6 text-center text-slate-400">
                  No templates uploaded — the built-in defaults are used.
                </td>
              </tr>
            )}
            {templates.map((t) => (
              <tr key={t.id} className="border-t border-slate-100">
                <td className="px-4 py-2">{t.kind === "igtr" ? "Service Agreement" : "Offer"}</td>
                <td className="px-4 py-2">{t.name}</td>
                <td className="px-4 py-2 text-slate-500">{t.filename}</td>
                <td className="px-4 py-2 text-right">
                  <button className="text-red-600 hover:underline" onClick={() => remove(t.id)}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// TemplateFieldsModal lists every placeholder a template author can reference,
// fetched from the backend so it can't drift from the renderer. Clicking a
// placeholder copies it.
function TemplateFieldsModal({ onClose }: { onClose: () => void }) {
  const [cat, setCat] = useState<TemplateFieldCatalog | null>(null);
  const [query, setQuery] = useState("");
  const matches = (f: TemplateField) => {
    const q = query.trim().toLowerCase();
    return !q || f.placeholder.toLowerCase().includes(q) || f.description.toLowerCase().includes(q);
  };
  const [copied, setCopied] = useState("");
  // Render into a portal (below) so the overlay isn't affected by the parent
  // section's flex/space-y flow; mounted guards against SSR (no document).
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  useEffect(() => {
    api.templateFields().then(setCat).catch(() => setCat({ header: [], line: [] }));
  }, []);

  async function copy(ph: string) {
    await navigator.clipboard?.writeText(ph).catch(() => {});
    setCopied(ph);
    setTimeout(() => setCopied((c) => (c === ph ? "" : c)), 1200);
  }

  function fieldTable(fields: TemplateField[]) {
    return (
      <div className="card overflow-hidden">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="bg-slate-50 text-left text-slate-500">
              <th className="px-4 py-2 font-medium">Placeholder</th>
              <th className="px-4 py-2 font-medium">Description</th>
            </tr>
          </thead>
          <tbody>
            {fields.map((f) => (
              <tr key={f.name} className="border-t border-slate-100 align-top">
                <td className="px-4 py-2">
                  <button
                    type="button"
                    title="Click to copy"
                    className="rounded bg-slate-100 px-2 py-1 font-mono text-xs hover:bg-slate-200"
                    onClick={() => copy(f.placeholder)}
                  >
                    {copied === f.placeholder ? "Copied!" : f.placeholder}
                  </button>
                </td>
                <td className="px-4 py-2 text-slate-600">{f.description}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  if (!mounted) return null;
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
    >
      <div
        className="max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-lg bg-white p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-lg font-semibold">Template fields</h3>
          <button type="button" className="text-slate-500 hover:text-slate-800" onClick={onClose}>
            <Icon name="x" className="h-5 w-5" />
          </button>
        </div>
        <p className="mb-4 text-sm text-slate-500">
          Reference these in your <code>.docx</code>. Header fields are scalar{" "}
          <code>{"{{Field}}"}</code> tokens placed anywhere (fields of a record the document doesn&apos;t
          link to render blank); line and schedule fields each go in a single table row that is
          cloned per row. Click a placeholder to copy it.
        </p>
        <input
          className="input mb-4"
          placeholder="Search fields…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />

        {!cat ? (
          <p className="text-sm text-slate-400">Loading…</p>
        ) : (
          <div className="space-y-6">
            {[
              ...groupFields(cat.header),
              ["Line item fields (repeating row)", cat.line] as const,
              ["Invoicing schedule fields (repeating row, Service Agreements)", cat.schedule || []] as const,
            ]
              .map(([title, fields]) => [title, fields.filter(matches)] as const)
              .filter(([, fields]) => fields.length > 0)
              .map(([title, fields]) => (
                <div key={title} className="space-y-2">
                  <h4 className="text-sm font-semibold text-slate-700">{title}</h4>
                  {fieldTable(fields)}
                </div>
              ))}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

// groupFields splits header fields into the original flat placeholders and one
// section per record namespace (Quote, Account, …), keeping catalog order.
function groupFields(fields: TemplateField[]): (readonly [string, TemplateField[]])[] {
  const out = new Map<string, TemplateField[]>();
  for (const f of fields) {
    const g = f.group ? `${f.group} fields` : "General fields";
    if (!out.has(g)) out.set(g, []);
    out.get(g)!.push(f);
  }
  return Array.from(out.entries());
}

// ApiTokensSection lets a member mint per-agent API tokens (shown exactly once)
// for connecting agents like Claude Code / Copilot CLI to the MCP endpoint, and
// revoke their own. Admins see and can revoke every token in the organization.
function ApiTokensSection({ admin, currentUserId }: { admin: boolean; currentUserId?: string }) {
  const [rows, setRows] = useState<(ApiToken | TenantApiToken)[]>([]);
  const [name, setName] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [expires, setExpires] = useState("");
  const [created, setCreated] = useState<ApiTokenCreated | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function load() {
    (admin ? api.teamTokens() : api.tokens())
      .then((r) => setRows(r.tokens || []))
      .catch(() => setRows([]));
  }
  useEffect(load, [admin]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (!name.trim()) {
      setError("Give the token a name");
      return;
    }
    setBusy(true);
    try {
      const t = await api.createToken({
        name: name.trim(),
        read_only: readOnly,
        expires_in_days: expires ? Number(expires) : undefined,
      });
      setCreated(t);
      setCopied(false);
      setName("");
      setReadOnly(false);
      setExpires("");
      load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create token");
    } finally {
      setBusy(false);
    }
  }

  async function revoke(id: string) {
    if (!confirm("Revoke this token? Any agent using it loses access immediately.")) return;
    await (admin ? api.revokeTeamToken(id) : api.revokeToken(id)).catch(() => {});
    load();
  }

  async function copy() {
    if (!created) return;
    await navigator.clipboard?.writeText(created.token).catch(() => {});
    setCopied(true);
  }

  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold">API tokens</h2>
      <p className="text-sm text-slate-500">
        Long-lived credentials for agents (e.g. Claude Code or Copilot CLI) connecting to the MCP
        endpoint — pass one as a <code>Bearer</code> token. A read-only token can read data but not
        modify it. Tokens inherit your current role and section access and can be revoked at any
        time.
        {admin && " As an admin you can see and revoke every token in the organization."}
      </p>

      {created && (
        <div className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm">
          <div className="mb-2 font-medium text-amber-900">
            Copy your new token now — it won't be shown again.
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 overflow-x-auto rounded bg-white px-2 py-1 text-xs">
              {created.token}
            </code>
            <button type="button" className="btn-secondary" onClick={copy}>
              <Icon name={copied ? "check" : "copy"} className="h-4 w-4" /> {copied ? "Copied" : "Copy"}
            </button>
            <button
              type="button"
              className="text-slate-500 hover:underline"
              onClick={() => setCreated(null)}
            >
              Dismiss
            </button>
          </div>
        </div>
      )}

      <form onSubmit={create} className="card flex flex-wrap items-end gap-3 p-4">
        <div className="flex-1 min-w-[180px]">
          <label className="label">Name</label>
          <input
            className="input"
            placeholder="e.g. claude-code"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div>
          <label className="label">Expires (days)</label>
          <input
            className="input w-32"
            type="number"
            min={1}
            placeholder="Never"
            value={expires}
            onChange={(e) => setExpires(e.target.value)}
          />
        </div>
        <label className="flex items-center gap-2 pb-2 text-sm">
          <input
            type="checkbox"
            className="checkbox"
            checked={readOnly}
            onChange={(e) => setReadOnly(e.target.checked)}
          />
          Read-only
        </label>
        <button className="btn-primary" disabled={busy}>
          <Icon name="key" className="h-4 w-4" /> {busy ? "Creating…" : "Create token"}
        </button>
      </form>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <div className="card overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="bg-slate-50 text-left text-slate-500">
              <th className="px-4 py-2 font-medium">Name</th>
              {admin && <th className="px-4 py-2 font-medium">Owner</th>}
              <th className="px-4 py-2 font-medium">Access</th>
              <th className="px-4 py-2 font-medium">Created</th>
              <th className="px-4 py-2 font-medium">Last used</th>
              <th className="px-4 py-2 font-medium">Expires</th>
              <th className="px-4 py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={admin ? 7 : 6} className="px-4 py-6 text-center text-slate-400">
                  No active tokens.
                </td>
              </tr>
            )}
            {rows.map((t) => {
              const owner = t as TenantApiToken;
              return (
                <tr key={t.id} className="border-t border-slate-100">
                  <td className="px-4 py-2">{t.name}</td>
                  {admin && (
                    <td className="px-4 py-2">
                      {owner.user_name || owner.user_email}
                      {owner.user_id === currentUserId && (
                        <span className="ml-1 text-xs text-slate-400">(you)</span>
                      )}
                    </td>
                  )}
                  <td className="px-4 py-2">{t.read_only ? "Read-only" : "Full"}</td>
                  <td className="px-4 py-2 text-slate-500">{formatDate(t.created_at)}</td>
                  <td className="px-4 py-2 text-slate-500">
                    {t.last_used_at ? formatDate(t.last_used_at) : "Never"}
                  </td>
                  <td className="px-4 py-2 text-slate-500">
                    {t.expires_at ? formatDate(t.expires_at) : "Never"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button className="text-red-600 hover:underline" onClick={() => revoke(t.id)}>
                      Revoke
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
