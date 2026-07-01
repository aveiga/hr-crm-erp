"use client";

import { use, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, ApiError, getToken, type TemplateMeta } from "@/lib/api";
import { useEntity } from "@/components/MetaProvider";
import { useAuth, canWrite } from "@/lib/auth";
import { FieldInput } from "@/components/FieldInput";
import { LineItemsEditor } from "@/components/LineItemsEditor";
import { ChildItemsEditor } from "@/components/ChildItemsEditor";
import { toInputValue } from "@/lib/format";
import { Icon } from "@/components/Icon";

// SAR amount is days * 8 (hours/day) * rate. Blank until days are entered.
function sarAmount(days: any, rate: any): number | "" {
  if (days === "" || days == null) return "";
  return Number(days || 0) * 8 * Number(rate || 0);
}

export default function RecordPage({
  params,
}: {
  params: Promise<{ entity: string; id: string }>;
}) {
  const { entity: entityKey, id } = use(params);
  const entity = useEntity(entityKey);
  const { role } = useAuth();
  const router = useRouter();
  const isNew = id === "new";
  const isDoc = !!entity?.children?.length;
  const childSpec = entity?.children?.[0];
  const childEntity = useEntity(childSpec?.entity || "");
  const isLineDoc = !!childEntity?.fields.some((f) => f.key === "line_net");

  const [values, setValues] = useState<globalThis.Record<string, any>>({});
  const [childRows, setChildRows] = useState<any[]>([]);
  const [loading, setLoading] = useState(!isNew);
  const [saving, setSaving] = useState(false);
  const [converting, setConverting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
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
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Failed to load"))
      .finally(() => setLoading(false));
  }, [entityKey, id, isNew, entity, isDoc]);

  const editable = canWrite(role) && !entity?.read_only;

  const title = useMemo(() => {
    if (!entity) return "";
    if (isNew) return `New ${entity.label}`;
    return values[entity.title_field] || entity.label;
  }, [entity, isNew, values]);

  if (!entity) return <div className="text-slate-400">Unknown module.</div>;
  if (loading) return <div className="text-slate-400">Loading…</div>;

  // Auto-fill the legal entity name + address on an invoice when the entity is
  // chosen (these are read-only, server-confirmed on save).
  async function onFieldChange(key: string, v: any) {
    setValues((s) => {
      const next = { ...s, [key]: v };
      // When a field that other fields depend on changes, clear the dependents
      // (e.g. changing Department resets the Team choice).
      for (const f of entity?.fields || []) {
        if (f.filter_by === key) next[f.key] = "";
      }
      return next;
    });
    if (entityKey === "invoices" && key === "legal_entity_id" && v) {
      try {
        const le = await api.get("legal_entities", v);
        setValues((s) => ({
          ...s,
          legal_entity_name: le.data.legal_name ?? "",
          legal_entity_billing_address: le.data.billing_address ?? "",
        }));
      } catch {
        /* ignore */
      }
    }
    // SAR: auto-fill the (read-only) contracting company + rate from the person,
    // and recompute the (read-only) amount = days * 8 * rate.
    if (entityKey === "sar_tracking" && key === "person_id") {
      if (!v) {
        setValues((s) => ({ ...s, contracting_company_id: "", rate: "", amount: sarAmount(s.days_reported, "") }));
      } else {
        try {
          const person = await api.get("people", v);
          const rate = person.data.hourly_rate ?? "";
          setValues((s) => ({
            ...s,
            contracting_company_id: person.data.contracting_company_id ?? "",
            rate,
            amount: sarAmount(s.days_reported, rate),
          }));
        } catch {
          /* ignore */
        }
      }
    }
    if (entityKey === "sar_tracking" && key === "days_reported") {
      setValues((s) => ({ ...s, amount: sarAmount(v, s.rate) }));
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
        const body = { ...payload, lines: childRows };
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

  async function convert() {
    setConverting(true);
    setError("");
    try {
      const res = await api.docConvert(entityKey, id);
      router.push(`/m/${res.entity}/${res.data.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Conversion failed");
      setConverting(false);
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

  const convertLabel =
    entityKey === "quotes" ? "Convert to Sales Order" : entityKey === "sales_orders" ? "Convert to Invoice" : "";

  return (
    <div className="max-w-4xl space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Link href={`/m/${entityKey}`} className="text-slate-400 hover:text-slate-600">
            <Icon name="arrow-left" className="h-5 w-5" />
          </Link>
          <h1 className="text-2xl font-bold">{title}</h1>
        </div>
        <div className="flex gap-2">
          {!isNew && editable && convertLabel && (
            <button type="button" className="btn-secondary" onClick={convert} disabled={converting}>
              <Icon name="arrow-right-left" className="h-4 w-4" />
              {converting ? "Converting…" : convertLabel}
            </button>
          )}
          {!isNew && editable && (
            <button type="button" className="btn-danger" onClick={remove}>
              <Icon name="trash-2" className="h-4 w-4" /> Delete
            </button>
          )}
        </div>
      </div>

      {entityKey === "invoices" && !isNew && <DocumentActions invoiceId={id} />}

      <form onSubmit={save} className="space-y-4">
        <div className="card p-6">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {entity.fields
              // On line-item documents, the computed money totals are shown in
              // the line breakdown below, so hide them from the header form.
              .filter((f) => !(isLineDoc && f.read_only && f.type === "money"))
              .map((f) => (
                <div key={f.key} className={f.type === "longtext" ? "sm:col-span-2" : ""}>
                  {f.type !== "bool" && (
                    <label className="label">
                      {f.label}
                      {f.required && <span className="text-red-500"> *</span>}
                      {f.read_only && <span className="ml-1 text-xs text-slate-400">(auto)</span>}
                    </label>
                  )}
                  <FieldInput
                    field={f}
                    value={values[f.key]}
                    record={values}
                    disabled={!editable || f.read_only}
                    onChange={(v) => onFieldChange(f.key, v)}
                  />
                  {f.help && <p className="mt-1 text-xs text-slate-400">{f.help}</p>}
                </div>
              ))}
          </div>
        </div>

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

// DocumentActions renders Offer/IGTR generation buttons for an invoice, with a
// per-kind template selector (built-in default plus any uploaded templates).
function DocumentActions({ invoiceId }: { invoiceId: string }) {
  const [templates, setTemplates] = useState<TemplateMeta[]>([]);
  const [offerTpl, setOfferTpl] = useState("");
  const [igtrTpl, setIgtrTpl] = useState("");

  useEffect(() => {
    api.templates().then((r) => setTemplates(r.data)).catch(() => setTemplates([]));
  }, []);

  const offerTemplates = templates.filter((t) => t.kind === "offer");
  const igtrTemplates = templates.filter((t) => t.kind === "igtr");

  function open(kind: "offer" | "igtr", format: "pdf" | "docx") {
    const templateId = kind === "offer" ? offerTpl : igtrTpl;
    const url = api.documentUrl(kind, invoiceId, format, templateId || undefined);
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
        <DocKind
          title="Offer"
          icon="file-text"
          templates={offerTemplates}
          value={offerTpl}
          onSelect={setOfferTpl}
          onGenerate={(fmt) => open("offer", fmt)}
        />
        <DocKind
          title="IGTR"
          icon="receipt"
          templates={igtrTemplates}
          value={igtrTpl}
          onSelect={setIgtrTpl}
          onGenerate={(fmt) => open("igtr", fmt)}
        />
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
        <button className="btn-secondary" onClick={() => onGenerate("pdf")}>
          PDF
        </button>
        <button className="btn-secondary" onClick={() => onGenerate("docx")}>
          DOCX
        </button>
      </div>
    </div>
  );
}
