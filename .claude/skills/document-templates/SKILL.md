---
name: document-templates
description: Edit, add, or debug the Offer and IGTR document templates (Word/PDF generation). Use when the user wants to change how generated Offers or Invoices/IGTRs look, add a new merge field, fix a placeholder that isn't filling, swap in their own finished Word .docx, or add a new document type.
---

# Document templates (Offer / IGTR)

Generated documents reproduce the original Excel + Word-VBA flow, but headless
on Linux via LibreOffice. Code lives in `backend/internal/documents/`.

## How it works

1. `service.go` loads invoice data (invoice + lines + account + legal entity),
   builds a `values` map (scalars) and a `lines` slice, all tenant-scoped.
2. `docx.go` fills a template:
   - **Scalars:** `{{Field}}` is replaced everywhere.
   - **Line items:** the single table row `<w:tr>` containing any
     `{{line.<col>}}` token is duplicated once per line; tokens are replaced per
     line. Available line columns: `line_no, sku, description, qty, unit_price,
     line_net`.
   - Any leftover `{{...}}` is blanked so it never shows in the output.
3. `convert.go` runs `soffice --headless --convert-to pdf` to produce the PDF.

## Template forms (three options, priority order)

0. **Uploaded per-tenant templates (UI)** — admins upload `.docx` files under
   **Team → Document templates** (`POST /api/v1/templates`, stored in the
   `doc_templates` table). At generation, the user selects one via
   `?template_id=<id>`. Word-split placeholders are auto-normalized
   (`normalizePlaceholders` in `docx.go`), so ordinary Word docs work.

The default templates are **embedded in the binary** from
`backend/internal/documents/templates/{offer,igtr}.xml` (via `go:embed`), so
generation works from any working directory with no setup. Resolution order in
`service.go loadTemplate`: a `<kind>.docx` then `<kind>.xml` in `TEMPLATES_DIR`
(if set), otherwise the embedded `<kind>.xml`.

- **`<kind>.xml`** (shipped default): the raw `word/document.xml` body. Easy to
  edit as text; the app wraps it into a valid `.docx`. Edit
  `backend/internal/documents/templates/offer.xml` /
  `.../igtr.xml` to change the default layout (rebuild to re-embed).
- **`<kind>.docx`** (drop-in override): set `TEMPLATES_DIR=/path` and put a
  finished Word document at `/path/igtr.docx`. Use the same `{{Field}}` /
  `{{line.col}}` placeholders. Author placeholders so each sits in a single run
  (type them in one go; don't let Word split them), and put the line
  placeholders in exactly one table row. In Docker, mount the dir and set
  `TEMPLATES_DIR`.

## Available scalar fields

`Invoice_ID, Legal_Entity, Account_ID, Account_Name, Buying_Entity_Name,
Buying_Entity_Address, Legal_Entity_Name, Legal_Entity_Address,
Legal_Entity_First_Name, Legal_Entity_Last_Name, Legal_Entity_Title,
Invoice_Date, Due_Date, Service_Start_Date, Service_End_Date, Currency,
Quote_Description, Net_Total, VAT_Total, Grand_Total, VAT_Pct`

(These match the field names from the original `83_/84_Print_*_Data` sheets, so
your existing SharePoint templates work after renaming nothing.)

To add a new scalar, add it to the `values` map in `Service.buildValues`
(`service.go`) and reference `{{Your_Field}}` in the template.

## Add a new document kind

1. Create `templates/<kind>.xml` (or `.docx`).
2. Allow the kind in `handlers.go Generate` (the `kind != "offer" && kind !=
   "igtr"` check) and any UI buttons in
   `frontend/app/(app)/m/[entity]/[id]/page.tsx` (`DocumentActions`).

## Verify

- DOCX path needs no LibreOffice and is unit-tested:
  `go test ./internal/documents/...` and the integration tests
  `TestGenerateOfferDocx` / `TestGenerateIGTRDocx`.
- For PDF, you need `soffice` on PATH (present in the devcontainer and the
  backend image). Smoke test:
  `GET /api/v1/documents/igtr/<invoice_id>?format=pdf`.

## Gotchas

- LibreOffice must be installed for `?format=pdf`; the API returns 503 with a
  clear message if it's missing — `?format=docx` always works.
- XML-escape is automatic for substituted values; don't pre-escape.
- Dates are reformatted to `dd-mm-yyyy` (matching the old VBA) by `fmtDate`.
