# CLAUDE.md — working in this repo

Context and conventions for agents working on this codebase. Task-specific
guides live in `.claude/skills/` (`add-module`, `document-templates`,
`db-migrations`, `run-local`) — read the relevant one before a change.

## What this is

A multi-tenant **HR / CRM / ERP** web app — the web successor to an Excel
workbook. Go API + Next.js (App Router) UI + Postgres + Redis. It manages
teams, leads, quotes, service agreements and payments, and generates Offer /
Service Agreement documents via headless LibreOffice. **In production:** keep
changes non-breaking (stable entity/field keys, additive migrations, existing
API shapes and MCP tool names preserved).

Stack: Go (stdlib `net/http`) · Next.js + Tailwind + TypeScript · PostgreSQL 16
· Redis 7 (optional) · LibreOffice (PDF). Devcontainer + docker-compose; CI
tests on PR and publishes images to GHCR on merge to `main`.

## Golden rules

- **The app is metadata-driven.** Every business module is defined ONCE as an
  `Entity` in `backend/internal/resource/catalog.go`. From that the engine
  generates the DB schema, the REST API, and the whole CRUD UI. Adding or
  changing a module/field is almost always a one-line catalog edit — do NOT
  hand-write per-entity SQL, endpoints, or React pages. Only genuinely bespoke
  behavior lives outside the engine (auth, permissions, documents, conversions,
  dashboard, and a few frontend special-cases — see below).
- **Tenancy is sacred.** Every entity row has `tenant_id`; every query in
  `internal/resource/repo.go` and `docservice.go` filters by it. Never add a
  query that isn't tenant-scoped.
- **Permissions are enforced server-side.** Access is checked on the request
  principal, not just hidden in the UI. Use the existing helpers.
- **Keep dependencies minimal.** Backend deps: pgx, go-redis, golang-jwt,
  google/uuid, x/crypto — that's it. No ORM/web framework. Frontend: Next,
  React, Tailwind, lucide-react. Don't add libraries without strong reason
  (e.g. the org chart export is hand-rolled SVG→canvas, no chart lib).
- **Reuse the established patterns** below instead of inventing new ones.
- **Match surrounding code.** Small, composable functions; standard `net/http`
  handlers; explicit errors.

## Backend layout (`backend/internal`)

- `config` — env config (all settings are env vars; sane local defaults).
- `db` — pgx pool + tiny embedded migration runner (`migrations/*.sql`).
- `httpx` — JSON/error helpers, middleware, and the request **Principal**
  (`UserID`, `TenantID`, `Role`, `AllowedGroups`; `CanAccessGroup`, `CanWrite`,
  `IsAdmin`).
- `auth` — users, tenants, memberships, invites, JWT, RBAC middleware, and the
  section-permissions endpoints.
- `resource` — the metadata engine:
  - `field.go` — `Field`/`Entity` types, field-type constants, and small
    constructors (`txt`, `money`, `ref`, `lookup`, `req`, `ro`, `hidden`,
    `filteredBy`, …). `Registry` (+ `Groups()`).
  - `catalog.go` — **the single source of truth**: all entities + fields.
  - `schema.go` — `EnsureSchema` creates/evolves tables (`CREATE TABLE IF NOT
    EXISTS` + `ADD COLUMN IF NOT EXISTS`), so adding a field needs no migration.
  - `repo.go` — generic tenant-scoped CRUD, value coercion, list
    filter/search/sort/paginate.
  - `handlers.go` — generic CRUD HTTP handlers, `/meta`, dashboard, and the
    document/convert handlers. `resolve()` enforces group access per request.
  - `docservice.go` — "documents" (a header + line items): transactional save,
    child replace, totals recompute, derived fields, and conversions.
- `documents` — Offer / Service Agreement (kind `igtr`) generation: `docx.go` (placeholder fill + row
  cloning + `normalizePlaceholders` for Word-split runs), `convert.go`
  (LibreOffice), `service.go` (gather data, pick template), `templates.go`
  (per-tenant uploaded templates), `handlers.go`. Default templates are
  **embedded** from `documents/templates/{offer,igtr}.xml`.
- `server` — router wiring (`server.go`) + the integration test suite.

## Metadata model (how to extend things)

An `Entity` has: `Key` (url + `ent_<key>` table), `Label`/`Plural`, `Group`
(nav section + permission unit), `TitleField`, `Icon` (lucide kebab-case), and:

- `Hidden bool` — not shown in nav (managed via a parent). Example: `teams`,
  line-item entities.
- `ReadOnly bool` — shown in nav as a **searchable read-only list**; generic
  create/update/delete return 403. Example: `contacts` (edited via its Account;
  a row click opens the parent). Editing still happens through the parent's
  document save, which bypasses the generic write guard.
- `Children []Child{Entity, FK, Label}` — makes it a **document** (header +
  sub-collection). Examples: quotes/invoices/purchase_orders/
  vendor_bills → line items; accounts → contacts; departments → teams.
  `Children[0]` is the primary collection (`lines` in payloads). Further
  entries are secondary collections sent/returned under their child entity key
  (`payment_schedule: [...]`, `children: {...}` on GET); a save only replaces a
  secondary collection when its key is present.
- **Service Agreements are the `invoices` entity** (relabelled; key, table,
  routes and MCP tools unchanged). They carry line items plus a payment
  schedule (`payment_schedule`). Sales Orders were retired (quote → agreement
  directly); `ent_sales_orders` stays in the DB, unused.

Field types: `text, longtext, int, number, money, percent, date, datetime,
bool, select, ref, lookup, email`. Notable field flags:

- `ref` — stores the target row's **id**; `RefLabel` is the display field.
- `lookup` — stores a chosen **value** (`ValueField`) from a source entity,
  displays `LabelField`. Used when you want a text value, not an id (e.g. a
  person's `team` stores the team name).
- `filteredBy(f, siblingKey, targetField)` — a **dependent** ref/lookup: options
  limited to rows where `targetField == record[siblingKey]`, disabled until the
  sibling is set. Example: People `team` filtered by `department_id`. The
  frontend clears dependents when the sibling changes.
- `ro(...)` (`ReadOnly`) — computed/auto-filled; disabled in the UI. For
  documents these are set by the server (totals, derived fields); for generic
  entities the client auto-fills and sends them.
- **Field rules** (`rules.go`, enforced server-side on every write — REST, MCP,
  conversions, doc saves — and mirrored in the record page):
  - `filledFrom(f, siblingRef, sourceField)` — while the sibling ref is set, the
    field mirrors the referenced row's field and is locked (People country ←
    Location, Account buyer details ← Buying Entity, Lead company ← Entity).
  - `disabledWhen(f, sibling, value)` — cleared/disabled while sibling == value
    (an Internal person has no contracting company).
  - `withDefault(f, v)` — applied on create when empty, pre-filled in new forms.
  - `sellerRef` = a legal-entity ref limited to `entity_type = Own`.
- **Write hooks** (`Repo.Hook`): per-entity Before/After callbacks around
  generic writes (payments keep their agreement's payment position current).
- **List metadata:** `Views` (saved filters shown as chips; also documented to
  MCP) and `Columns` (explicit list columns). List filters: `f_` (equals,
  `@empty`), `fin_` (any of), `fmin_`/`fmax_` (ranges); date tokens `@today`,
  `@today±N`, `@month`. The list page keeps them in the URL; CSV export too.

## Documents = entities with line items

Parent entities with `Children` are saved via `POST/PUT /api/v1/docs/{entity}`
(and read via `GET /api/v1/docs/{entity}/{id}` → `{data, lines, children?}`). `DocService`
(resource/docservice.go):

- Upserts the header + **replaces** child rows in one transaction
  (`replaceChildren`, generic over any child entity; computes `line_no` and
  `line_net = qty*unit_price` when those columns exist). A row sent with its
  `id` keeps it, so references to child rows survive saves (payments →
  installments, quotes → contacts); copies must drop ids.
- Recomputes money totals for line docs: `net → +markup% → +VAT% → grand`,
  `balance = grand - paid` (`ComputeTotals`). Total fields are `ro`.
- Fills **derived** fields (`deriveFields`) from the stored row: seller legal
  entity defaulted from the account's billing entity, seller/buyer snapshots
  (`partyFields()` in the catalog, shared by quotes/agreements), currency
  (agreement ← quote; else account ← its buying entity; else seller default),
  and agreement → opportunity via its quote. Add new derivations here. Note an
  account's `legal_entity_name` is the *buyer's* name.
- **Conversions** (`Convert`): lead → account + opportunity, opportunity →
  quote, quote → service agreement, legal entity → account; via
  `POST /api/v1/docs/{entity}/{id}/convert`. Read-only links the target keeps
  (agreement `quote_id`) are passed as `system` values to `save`.
- **Payments** (`payments.go`): an agreement's `payment_schedule` must add up
  to its grand total (blank amounts split the remainder). Payments (generic
  entity, optional `schedule_id`) trigger `recomputeAgreement`: installment
  paid/status, agreement `paid_total`/`balance`/`payment_status`/`next_due_date`.
  Quotes carry `agreement_status` (latest agreement's status).
- **Reports** (`reports.go`): `GET /api/v1/reports/sales` — live aggregates
  (agreements by currency/payment status, overdue & upcoming installments,
  quote conversion, payments by month/method), section-gated.
- **Duplicate** (`links.go`): `POST /api/v1/docs/{entity}/{id}/duplicate` for
  line-item documents (fresh draft, children copied).
- **Related** (`links.go`): `GET /api/v1/entities/{entity}/{id}/related` →
  upstream refs + downstream referencing records, with statuses.

- **Invoices** (`sales_invoices`, ERP): line-item documents billing a period
  of an agreement; `schedule_id` links the installment they settle, and
  `recomputeAgreement` mirrors that installment's paid amount/status onto them.
- `DocService.OnSave(entity, fn)` runs a callback after a document save (used
  by the Azure module); Repo hooks cover generic writes/deletes.

## Azure fee billing (`internal/azure`)

Entities (group `Azure`): `azure_tenants`, `azure_subscriptions` (→ account,
type TAN/TSS, `fee_pct` override), `azure_fee_rates` (per type, effective
from the month of `valid_from`; defaults TAN 25 / TSS 27 in code),
`azure_usage` (daily actual cost per subscription/service, read-only).
Client secrets live encrypted in the core table `azure_credentials`
(`SECRETS_KEY`), never in an entity. The package has: `client.go` (Entra token,
ARM subscriptions, Cost Management Query, 429 retry), `service.go` (sync +
daily scheduler via `Server.RunBackground`), `billing.go` (fee %, run-rate
forecast, quarter planning of `azure_fees` agreements, invoice creation and
true-up: an invoice's net/grand replace its quarter's line/installment),
`notes.go` (the agreement Notes block with actual usage per subscription,
between markers, rewritten on each refresh; user text outside it is kept),
`dashboard.go`, `importcsv.go` (CSV fallback: preview compares a Cost
analysis download per subscription-day with stored usage; apply adds new
days and overwrites only the changed days chosen), `handlers.go`
(`/api/v1/azure/...`). Tests use a fake Azure
server (`server/azure_test.go`); setup guide: `docs/azure-setup.md`.

Child editors on the frontend: `LineItemsEditor` (line docs — SKU lookup w/
autofill, live totals), `ScheduleEditor` (payment schedules — dates + amounts
that must match the total) and `ChildItemsEditor` (generic sub-records, e.g.
contacts).

## Tenancy & permissions

- **Roles** (membership): `owner > admin > member > viewer`. `RequireWrite`
  blocks viewers; `RequireAdmin` gates team/permissions/templates endpoints.
- **Section permissions**: `memberships.allowed_groups TEXT[]` (nil = full
  access). Owners/admins always have full access. `Principal.CanAccessGroup`
  is checked by `resolve()` (entity handlers), the doc handlers, document
  generation, `/meta` (filters entities), and the dashboard. Managed via the
  Team → Section access matrix (`GET/PUT /api/v1/team/permissions`). Never add a
  new entity/section endpoint without a group check.

## Conventions

- Errors: return `*httpx.APIError` (carries HTTP status + code); `httpx.Fail`
  maps unknown errors to 500 without leaking internals.
- Money/number/percent → `DOUBLE PRECISION` (float64) for clean JSON; dates →
  `DATE`/`TIMESTAMPTZ`, returned as RFC3339 strings.
- Value coercion (`repo.go coerce`) accepts numbers as float64/int64/string —
  keep it tolerant (conversions pass DB-typed values back in).
- Frontend is a token-based SPA; bearer token in `localStorage` (`lib/api.ts`).
  The UI is driven by `/meta` (`MetaProvider`); nav hides `hidden` entities.
- Auto-fill/read-only pattern for generic entities: client sets the value in
  `onFieldChange` (record page) and it's stored on save (e.g. a sales document's
  seller previewed from the chosen account). For documents,
  prefer server-side `deriveFields`.

## Frontend special-cases (intentional, keep them thin)

- `m/[entity]/[id]` — generic record form; branches for documents (line/child/
  schedule editors, convert + duplicate buttons), the related-records panel,
  document generation (Offer on quotes, Service Agreement on agreements), and
  dependent-field reset.
- `m/[entity]` (list) — generic, with views/filters (`ListFilters`), sortable
  columns and CSV export; special-cases People (Yearly Cost column +
  `PeopleCostSummary` + Organization button) and read-only lists (row → parent).
- `reports/` — sales reports (`/reports/sales`), every figure links into a
  filtered list.
- `org/` — HR org chart (`OrgChart`, self-contained SVG) with PNG/CSV export;
  resolves `department_id`→name and computes cost = hourly_rate × yearly_hours ×
  FTE%.
- `users/` — admin **User Manager**: members (edit identity/role/sections,
  remove, reset-password links), invites with role + sections up front and
  status/failure tracking, and the **permissions matrix**.
- `team/` — **document templates** upload/manage and API tokens.
- `azure/` — Azure dashboard (usage/fees actual + forecast, invoiced/paid/due,
  filters, what-if markup) and "New Azure agreement"; `AzurePanels` adds the
  tenant connection panel and an agreement's quarter/invoice panel to records.

## Migrations

Two layers:
1. **Core/platform** tables → SQL files in `internal/db/migrations/` (`0001`
   tenants/users/memberships/invites/documents, `0002` doc_templates, `0003`
   member permissions). Applied once each, in order, on boot. Idempotent DDL;
   no down-migrations (write a new forward file).
2. **Entity** tables (`ent_<key>`) → generated by `EnsureSchema` from the
   catalog every boot. Adding a catalog field needs no migration. `db.Migrate`
   runs before `EnsureSchema` in `main.go`.

Note: old columns are never dropped automatically. Renaming a field key leaves
the old column orphaned (data isn't migrated) — prefer stable keys.

## Verify changes (always run before finishing)

```bash
# backend
cd backend && go build ./... && go vet ./...
TEST_DATABASE_URL='postgres://hrcrm:hrcrm@localhost:55432/hrcrm_test?sslmode=disable' go test -count=1 ./...
# frontend
cd frontend && npm run typecheck && npm run build
```

Spin a throwaway test Postgres:

```bash
docker run -d --name hrcrm-test-pg -e POSTGRES_USER=hrcrm -e POSTGRES_PASSWORD=hrcrm \
  -e POSTGRES_DB=hrcrm_test -p 55432:5432 postgres:16-alpine
```

Add/adjust tests in `backend/internal/server/*_test.go` — they exercise the
real HTTP handlers against Postgres, covering auth, tenant isolation, RBAC,
section permissions, CRUD, documents/totals/conversions, templates, and HR/cost
fields. Keep them green; document new behavior with a test.

## Commit / PR

Don't commit or push unless asked. Work on a branch if the user requests a
commit. CI (`.github/workflows/ci.yml`) runs backend tests (with a Postgres
service) + frontend typecheck/build on every PR; merging to `main`
(`release.yml`) builds and pushes `-backend` and `-frontend` images to GHCR.
