"use client";

import { use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, ApiError, getToken, type RelatedResult, type TemplateMeta } from "@/lib/api";
import { useEntity, useMeta } from "@/components/MetaProvider";
import { useAuth, canWrite, isAdmin } from "@/lib/auth";
import { FieldInput } from "@/components/FieldInput";
import { LineItemsEditor } from "@/components/LineItemsEditor";
import { ChildItemsEditor } from "@/components/ChildItemsEditor";
import { ScheduleEditor } from "@/components/ScheduleEditor";
import { AzureAgreementPanel, AzureTenantPanel } from "@/components/AzurePanels";
import { toInputValue } from "@/lib/format";
import { Icon } from "@/components/Icon";
import type { Field } from "@/lib/types";

// Which generated document each source entity offers: Offers from Quotes,
// the Service Agreement document from Service Agreements.
const DOC_KINDS: globalThis.Record<string, ("offer" | "igtr")[]> = {
  quotes: ["offer"],
  invoices: ["igtr"],
};

// Pipeline conversions offered on a record (server: DocService.Convert).
const CONVERT_LABEL: globalThis.Record<string, string> = {
  leads: "Convert to Opportunity",
  opportunities: "Create Quote",
  quotes: "Create Service Agreement",
  legal_entities: "Create Account",
};

export default function RecordPage({
  params,
}: {
  params: Promise<{ entity: string; id: string }>;
}) {
  const { entity: entityKey, id } = use(params);
  const entity = useEntity(entityKey);
  const { byKey } = useMeta();
  const { role } = useAuth();
  const router = useRouter();
  const isNew = id === "new";
  const isDoc = !!entity?.children?.length;
  const childSpec = entity?.children?.[0];
  const childEntity = useEntity(childSpec?.entity || "");
  const isLineDoc = !!childEntity?.fields.some((f) => f.key === "line_net");
  // Secondary child collections (e.g. a service agreement's payment schedule).
  const extraSpecs = entity?.children?.slice(1) || [];
  // Sales documents snapshot both parties (seller + buyer) on save.
  const isSalesDoc = !!entity?.fields.some((f) => f.key === "buyer_name");

  const [values, setValues] = useState<globalThis.Record<string, any>>({});
  const [childRows, setChildRows] = useState<any[]>([]);
  const [extraRows, setExtraRows] = useState<globalThis.Record<string, any[]>>({});
  const [loading, setLoading] = useState(!isNew);
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0); // bump to reload the record

  useEffect(() => {
    if (isNew && entity) {
      // New records start from the catalog defaults (e.g. Entity Type).
      const v: globalThis.Record<string, any> = {};
      for (const f of entity.fields) if (f.default) v[f.key] = f.default;
      setValues(v);
    }
    if (isNew || !entity) {
      setLoading(false);
      return;
    }
    const load = isDoc ? api.docGet(entityKey, id) : api.get(entityKey, id);
    load
      .then((res: any) => {
        const v: globalThis.Record<string, any> = {};
        for (const f of entity.fields) v[f.key] = toInputValue(f, res.data[f.key]);
        setValues(v);
        if (isDoc && res.lines) setChildRows(res.lines);
        if (res.children) setExtraRows(res.children);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Failed to load"))
      .finally(() => setLoading(false));
  }, [entityKey, id, isNew, entity, isDoc, reloadKey]);

  const editable = canWrite(role) && !entity?.read_only;

  const title = useMemo(() => {
    if (!entity) return "";
    if (isNew) return `New ${entity.label}`;
    return values[entity.title_field] || entity.label;
  }, [entity, isNew, values]);

  if (!entity) return <div className="text-slate-400">Unknown module.</div>;
  if (loading) return <div className="text-slate-400">Loading…</div>;

  // A field is locked while it mirrors a chosen ref (fill_from) or while its
  // disabling sibling has the disabling value (e.g. an internal person's
  // contracting company). The server enforces the same rules on save.
  const locked = (f: Field) =>
    (!!f.fill_from && !!values[f.fill_from]) || (!!f.disabled_when && values[f.disabled_when] === f.disabled_value);

  async function onFieldChange(key: string, v: any) {
    const fields = entity?.fields || [];
    setValues((s) => {
      const next = { ...s, [key]: v };
      for (const f of fields) {
        // Dependent pickers reset when their sibling changes (Department → Team,
        // Account → Contact, Service Agreement → Scheduled Payment).
        if (f.filter_by === key) next[f.key] = "";
        if (f.disabled_when === key && v === f.disabled_value) next[f.key] = "";
      }
      return next;
    });

    // Mirror fields from the chosen ref (e.g. Location → Country, Buying
    // Entity → buyer details). Clearing the ref leaves them editable as-is.
    const mirrors = fields.filter((f) => f.fill_from === key);
    const refField = fields.find((f) => f.key === key);
    if (mirrors.length && refField?.ref && v) {
      try {
        const src = (await api.get(refField.ref, v)).data;
        setValues((s) => {
          const next = { ...s };
          for (const f of mirrors) next[f.key] = toInputValue(f, src[f.fill_field!]);
          return next;
        });
      } catch {
        /* ignore */
      }
    }

    if (isSalesDoc) await previewParties(key, v);
    if (entityKey === "payments") await previewPayment(key, v);
    if (entityKey === "leads" && key === "contact_id" && v) await fillLeadContact(v);
  }

  // Leads: picking a contact of the company fills the contact details (still
  // editable; the server fills any left blank the same way).
  async function fillLeadContact(contactID: string) {
    try {
      const c = (await api.get("contacts", contactID)).data;
      setValues((s) => ({
        ...s,
        contact_first_name: c.first_name ?? "",
        contact_last_name: c.last_name ?? "",
        email: c.email || s.email || "",
        phone: c.phone || s.phone || "",
      }));
    } catch {
      /* ignore */
    }
  }

  // Preview the server-derived party fields of a sales document as the user
  // picks them (recomputed on save): the seller defaults from the account's
  // billing entity; buyer snapshot and currency come from the account.
  async function previewParties(key: string, v: any) {
    const hasSeller = !!entity?.fields.some((f) => f.key === "legal_entity_billing_address");
    if (key === "account_id" && v) {
      try {
        const acc = (await api.get("accounts", v)).data;
        setValues((s) => ({
          ...s,
          legal_entity_id: s.legal_entity_id || acc.legal_entity_id || "",
          buyer_name: acc.legal_entity_name || acc.account_name || "",
          buyer_address: acc.legal_entity_address ?? "",
          buyer_vat_id: acc.vat_id ?? "",
          buyer_billing_email: acc.billing_email || acc.email || "",
          currency: acc.currency || s.currency || "",
        }));
        if (hasSeller && acc.legal_entity_id) fillSeller(acc.legal_entity_id, true);
      } catch {
        /* ignore */
      }
    }
    if (hasSeller && key === "legal_entity_id" && v) fillSeller(v, false);
  }

  // fillSeller copies a legal entity's name + address into the read-only seller
  // snapshot (and its default currency when none is set yet). onlyIfUnset:
  // when defaulting from the account, don't override a seller already picked.
  async function fillSeller(leID: string, onlyIfUnset: boolean) {
    try {
      const le = (await api.get("legal_entities", leID)).data;
      setValues((s) =>
        onlyIfUnset && s.legal_entity_id && s.legal_entity_id !== leID
          ? s
          : {
              ...s,
              legal_entity_name: le.legal_name ?? "",
              legal_entity_billing_address: le.billing_address ?? "",
              currency: s.currency || le.default_currency || "",
            }
      );
    } catch {
      /* ignore */
    }
  }

  // Payments: picking an installment fills its agreement and outstanding
  // amount; picking only an agreement fills its balance. (The server applies
  // the same defaults when the amount is left blank.)
  async function previewPayment(key: string, v: any) {
    try {
      if (key === "schedule_id" && v) {
        const inst = (await api.get("payment_schedule", v)).data;
        const due = Math.max(0, Number(inst.amount || 0) - Number(inst.paid_amount || 0));
        setValues((s) => ({ ...s, invoice_id: inst.invoice_id, schedule_id: v, amount: Math.round(due * 100) / 100 }));
      } else if (key === "invoice_id" && v) {
        const sa = (await api.get("invoices", v)).data;
        setValues((s) => ({ ...s, amount: Number(sa.balance ?? sa.grand_total ?? 0) }));
      }
    } catch {
      /* ignore */
    }
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!entity) return;
    setSaving(true);
    setError("");
    const payload: globalThis.Record<string, any> = {};
    for (const f of entity.fields) {
      let v = values[f.key];
      if (v === "") v = null;
      payload[f.key] = v;
    }
    try {
      if (isDoc) {
        const body: globalThis.Record<string, any> = { ...payload, lines: childRows };
        for (const c of extraSpecs) body[c.entity] = extraRows[c.entity] || [];
        const res = isNew
          ? await api.docCreate(entityKey, body)
          : await api.docUpdate(entityKey, id, body);
        if (isNew) router.replace(`/m/${entityKey}/${res.data.id}`);
        else {
          // refresh computed totals
          setValues((s) => ({ ...s, ...numericFields(res.data) }));
          router.push(`/m/${entityKey}`);
        }
      } else if (isNew) {
        const res = await api.create(entityKey, payload);
        router.replace(`/m/${entityKey}/${res.data.id}`);
      } else {
        await api.update(entityKey, id, payload);
        router.push(`/m/${entityKey}`);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
      setSaving(false);
    }
  }

  async function run(label: string, fn: () => Promise<{ entity: string; data: any }>, failMsg: string) {
    setBusy(label);
    setError("");
    try {
      const res = await fn();
      router.push(`/m/${res.entity}/${res.data.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : failMsg);
      setBusy("");
    }
  }

  async function remove() {
    if (!confirm("Delete this record? This cannot be undone.")) return;
    try {
      await api.remove(entityKey, id);
      router.push(`/m/${entityKey}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Delete failed");
    }
  }

  const convertLabel = CONVERT_LABEL[entityKey] || "";
  // A lead converts into the funnel exactly once.
  const alreadyConverted = entityKey === "leads" && !!values.converted_opportunity_id;

  // On documents, read-only party snapshots go in their own card; on line-item
  // documents the computed money totals are shown in the line breakdown.
  const isSnapshot = (f: Field) => isDoc && f.read_only && f.type !== "money" && f.type !== "ref";
  const mainFields = entity.fields.filter((f) => !(isLineDoc && f.read_only && f.type === "money") && !isSnapshot(f));
  const snapshotFields = entity.fields.filter(isSnapshot);

  const renderField = (f: Field) => (
    <div key={f.key} className={f.type === "longtext" ? "sm:col-span-2" : ""}>
      {f.type !== "bool" && (
        <label className="label flex items-center gap-1">
          <span>
            {f.label}
            {f.required && <span className="text-red-500"> *</span>}
            {(f.read_only || (f.fill_from && values[f.fill_from])) && (
              <span className="ml-1 text-xs text-slate-400">(auto)</span>
            )}
          </span>
          {f.type === "ref" && f.ref && values[f.key] && byKey[f.ref] && (
            <Link
              href={`/m/${f.ref}/${values[f.key]}`}
              className="text-slate-400 hover:text-brand-600"
              title={`Open ${byKey[f.ref].label}`}
            >
              <Icon name="external-link" className="h-3.5 w-3.5" />
            </Link>
          )}
        </label>
      )}
      <FieldInput
        field={f}
        value={values[f.key]}
        record={values}
        disabled={!editable || f.read_only || locked(f)}
        onChange={(v) => onFieldChange(f.key, v)}
      />
      {f.fill_from && values[f.fill_from] && (
        <p className="mt-1 text-xs text-slate-400">
          From {entity.fields.find((x) => x.key === f.fill_from)?.label || "the linked record"}
        </p>
      )}
      {f.help && <p className="mt-1 text-xs text-slate-400">{f.help}</p>}
    </div>
  );

  return (
    <div className="max-w-4xl space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Link href={`/m/${entityKey}`} className="text-slate-400 hover:text-slate-600">
            <Icon name="arrow-left" className="h-5 w-5" />
          </Link>
          <h1 className="text-2xl font-bold">{title}</h1>
        </div>
        <div className="flex flex-wrap gap-2">
          {!isNew && editable && convertLabel && !alreadyConverted && (
            <button
              type="button"
              className="btn-secondary"
              onClick={() => run("convert", () => api.docConvert(entityKey, id), "Conversion failed")}
              disabled={!!busy}
            >
              <Icon name="arrow-right-left" className="h-4 w-4" />
              {busy === "convert" ? "Converting…" : convertLabel}
            </button>
          )}
          {!isNew && editable && isLineDoc && (
            <button
              type="button"
              className="btn-secondary"
              onClick={() => run("duplicate", () => api.docDuplicate(entityKey, id), "Duplicate failed")}
              disabled={!!busy}
            >
              <Icon name="copy" className="h-4 w-4" />
              {busy === "duplicate" ? "Duplicating…" : "Duplicate"}
            </button>
          )}
          {!isNew && editable && (
            <button type="button" className="btn-danger" onClick={remove}>
              <Icon name="trash-2" className="h-4 w-4" /> Delete
            </button>
          )}
        </div>
      </div>

      {!isNew && <RelatedPanel entityKey={entityKey} id={id} />}

      {!isNew && DOC_KINDS[entityKey] && (
        <DocumentActions entityKey={entityKey} id={id} kinds={DOC_KINDS[entityKey]} />
      )}

      {!isNew && entityKey === "azure_tenants" && (
        <AzureTenantPanel id={id} editable={canWrite(role)} admin={isAdmin(role)} />
      )}
      {!isNew && entityKey === "invoices" && values.azure_fees && (
        <AzureAgreementPanel id={id} editable={editable} onChanged={() => setReloadKey((k) => k + 1)} />
      )}

      <form onSubmit={save} className="space-y-4">
        <div className="card p-6">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">{mainFields.map(renderField)}</div>
        </div>

        {snapshotFields.length > 0 && (
          <div className="card p-6">
            <div className="mb-3 text-sm font-medium text-slate-600">
              Parties &amp; links <span className="text-xs font-normal text-slate-400">(filled automatically on save)</span>
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">{snapshotFields.map(renderField)}</div>
          </div>
        )}

        {isDoc && isLineDoc && (
          <LineItemsEditor
            lines={childRows}
            onChange={setChildRows}
            disabled={!editable}
            currency={values.currency}
            markupPct={Number(values.markup_pct || 0)}
            vatPct={Number(values.vat_pct || 0)}
          />
        )}

        {isDoc && !isLineDoc && childEntity && childSpec && (
          <ChildItemsEditor
            childEntity={childEntity}
            fk={childSpec.fk}
            label={childSpec.label}
            rows={childRows}
            onChange={setChildRows}
            disabled={!editable}
          />
        )}

        {extraSpecs.map((c) => {
          const ce = byKey[c.entity];
          if (!ce) return null;
          const rows = extraRows[c.entity] || [];
          const onChange = (r: any[]) => setExtraRows((s) => ({ ...s, [c.entity]: r }));
          // Payment schedules (amounts that must add up to the document total)
          // get the schedule editor; anything else the generic one.
          return ce.fields.some((f) => f.key === "paid_amount") ? (
            <ScheduleEditor
              key={c.entity}
              label={c.label}
              rows={rows}
              onChange={onChange}
              disabled={!editable}
              lines={childRows}
              markupPct={Number(values.markup_pct || 0)}
              vatPct={Number(values.vat_pct || 0)}
              startDate={values.service_start_date}
              frequency={values.billing_frequency}
              currency={values.currency}
            />
          ) : (
            <ChildItemsEditor
              key={c.entity}
              childEntity={ce}
              fk={c.fk}
              label={c.label}
              rows={rows}
              onChange={onChange}
              disabled={!editable}
            />
          );
        })}

        {error && <p className="text-sm text-red-600">{error}</p>}

        {editable && (
          <div className="flex gap-2">
            <button className="btn-primary" disabled={saving}>
              {saving ? "Saving…" : "Save"}
            </button>
            <Link href={`/m/${entityKey}`} className="btn-secondary">
              Cancel
            </Link>
          </div>
        )}
      </form>
    </div>
  );
}

// numericFields extracts the computed money fields from a saved record so the
// form can reflect server-side totals without a full reload.
function numericFields(data: any): globalThis.Record<string, any> {
  const keys = ["net_total", "markup_total", "vat_total", "grand_total", "balance"];
  const out: globalThis.Record<string, any> = {};
  for (const k of keys) if (k in data) out[k] = data[k];
  return out;
}

// RelatedPanel shows where a record sits in the chain: the records it links to
// (upstream, e.g. a service agreement's sales order and quote) and the records
// that link to it (downstream, e.g. a quote's sales orders), with statuses.
function RelatedPanel({ entityKey, id }: { entityKey: string; id: string }) {
  const { byKey } = useMeta();
  const [rel, setRel] = useState<RelatedResult | null>(null);

  useEffect(() => {
    api.related(entityKey, id).then(setRel).catch(() => setRel(null));
  }, [entityKey, id]);

  if (!rel || (rel.upstream.length === 0 && rel.downstream.length === 0)) return null;

  return (
    <div className="card p-4">
      <div className="grid gap-4 sm:grid-cols-2">
        {rel.upstream.length > 0 && (
          <div>
            <div className="mb-2 text-sm font-medium text-slate-600">Linked to</div>
            <ul className="space-y-1 text-sm">
              {rel.upstream.map((u) => (
                <li key={u.field} className="flex items-center gap-2">
                  <Icon name={byKey[u.record.entity]?.icon} className="h-4 w-4 text-slate-400" />
                  <span className="text-slate-500">{u.label}:</span>
                  <Link href={`/m/${u.record.entity}/${u.record.id}`} className="truncate text-brand-600 hover:underline">
                    {u.record.title}
                  </Link>
                  {u.record.status && <StatusBadge status={u.record.status} />}
                </li>
              ))}
            </ul>
          </div>
        )}
        {rel.downstream.length > 0 && (
          <div>
            <div className="mb-2 text-sm font-medium text-slate-600">Related records</div>
            <div className="space-y-2 text-sm">
              {rel.downstream.map((g) => (
                <div key={g.entity + g.field}>
                  <div className="flex items-center gap-2 text-slate-500">
                    <Icon name={byKey[g.entity]?.icon} className="h-4 w-4 text-slate-400" />
                    {g.label} <span className="text-xs text-slate-400">({g.total})</span>
                  </div>
                  <ul className="ml-6 space-y-0.5">
                    {g.rows.map((r) => (
                      <li key={r.id} className="flex items-center gap-2">
                        <Link href={`/m/${r.entity}/${r.id}`} className="truncate text-brand-600 hover:underline">
                          {r.title}
                        </Link>
                        {r.status && <StatusBadge status={r.status} />}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  return <span className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600">{status}</span>;
}

const KIND_LABEL = { offer: "Offer", igtr: "Service Agreement" } as const;
const KIND_ICON = { offer: "file-text", igtr: "file-pen-line" } as const;

// DocumentActions renders document generation buttons for a source record
// (Offers from quotes, Service Agreements from agreements), with a per-kind
// template selector (built-in default plus any uploaded templates).
function DocumentActions({ entityKey, id, kinds }: { entityKey: string; id: string; kinds: ("offer" | "igtr")[] }) {
  const [templates, setTemplates] = useState<TemplateMeta[]>([]);
  const [selected, setSelected] = useState<globalThis.Record<string, string>>({});

  useEffect(() => {
    api.templates().then((r) => setTemplates(r.data)).catch(() => setTemplates([]));
  }, []);

  function open(kind: "offer" | "igtr", format: "pdf" | "docx") {
    const url = api.documentUrlFor(kind, entityKey, id, format, selected[kind] || undefined);
    fetch(url, { headers: { Authorization: `Bearer ${getToken()}` } })
      .then(async (r) => {
        if (!r.ok) throw new Error(await r.text());
        const blob = await r.blob();
        const cd = r.headers.get("Content-Disposition") || "";
        const m = cd.match(/filename="?([^"]+)"?/);
        const name = m ? m[1] : `${kind}.${format}`;
        const a = document.createElement("a");
        a.href = URL.createObjectURL(blob);
        a.download = name;
        a.click();
        URL.revokeObjectURL(a.href);
      })
      .catch((e) => alert("Generation failed: " + e.message));
  }

  return (
    <div className="card p-4">
      <div className="mb-3 text-sm font-medium text-slate-600">Generate documents</div>
      <div className="grid gap-4 sm:grid-cols-2">
        {kinds.map((kind) => (
          <DocKind
            key={kind}
            title={KIND_LABEL[kind]}
            icon={KIND_ICON[kind]}
            templates={templates.filter((t) => t.kind === kind)}
            value={selected[kind] || ""}
            onSelect={(v) => setSelected((s) => ({ ...s, [kind]: v }))}
            onGenerate={(fmt) => open(kind, fmt)}
          />
        ))}
      </div>
      <p className="mt-3 text-xs text-slate-400">
        PDF requires LibreOffice on the server; DOCX works everywhere. Upload custom templates under
        Team → Document templates.
      </p>
    </div>
  );
}

function DocKind({
  title,
  icon,
  templates,
  value,
  onSelect,
  onGenerate,
}: {
  title: string;
  icon: string;
  templates: TemplateMeta[];
  value: string;
  onSelect: (v: string) => void;
  onGenerate: (fmt: "pdf" | "docx") => void;
}) {
  return (
    <div className="rounded-md border border-slate-200 p-3">
      <div className="mb-2 flex items-center gap-2 font-medium">
        <Icon name={icon} className="h-4 w-4 text-brand-600" /> {title}
      </div>
      <label className="label">Template</label>
      <select className="select mb-2" value={value} onChange={(e) => onSelect(e.target.value)}>
        <option value="">Built-in default</option>
        {templates.map((t) => (
          <option key={t.id} value={t.id}>
            {t.name}
          </option>
        ))}
      </select>
      <div className="flex gap-2">
        <button type="button" className="btn-secondary" onClick={() => onGenerate("pdf")}>
          PDF
        </button>
        <button type="button" className="btn-secondary" onClick={() => onGenerate("docx")}>
          DOCX
        </button>
      </div>
    </div>
  );
}
