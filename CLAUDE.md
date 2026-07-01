# CLAUDE.md — working in this repo

Context and conventions for agents working on this codebase. Task-specific
guides live in `.claude/skills/` (`add-module`, `document-templates`,
`db-migrations`, `run-local`) — read the relevant one before a change.

## What this is

A multi-tenant **HR / CRM / ERP** web app — the web successor to an Excel
workbook. Go API + Next.js (App Router) UI + Postgres + Redis. It manages
teams, leads, offers and invoices (IGTRs), and generates Offer/IGTR documents
via headless LibreOffice.

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
- `documents` — Offer/IGTR generation: `docx.go` (placeholder fill + row
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
  sub-collection). Examples: quotes/sales_orders/invoices/purchase_orders/
  vendor_bills → line items; accounts → contacts; departments → teams.

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

## Documents = entities with line items

Parent entities with `Children` are saved via `POST/PUT /api/v1/docs/{entity}`
(and read via `GET /api/v1/docs/{entity}/{id}` → `{data, lines}`). `DocService`
(resource/docservice.go):

- Upserts the header + **replaces** child rows in one transaction
  (`replaceChildren`, generic over any child entity; computes `line_no` and
  `line_net = qty*unit_price` when those columns exist).
- Recomputes money totals for line docs: `net → +markup% → +VAT% → grand`,
  `balance = grand - paid` (`ComputeTotals`). Total fields are `ro`.
- Fills **derived** fields (`deriveFields`) — e.g. an invoice's
  `legal_entity_name`/`legal_entity_billing_address` from the chosen legal
  entity. Add new derivations here.
- **Conversions** (`Convert`): quote → sales order → invoice, copying header +
  lines, via `POST /api/v1/docs/{entity}/{id}/convert`.

Two child editors on the frontend: `LineItemsEditor` (line docs — SKU lookup w/
autofill, live totals) vs `ChildItemsEditor` (generic sub-records, e.g.
contacts). The record page picks based on whether the child has a `line_net`.

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
  `onFieldChange` (record page) and it's stored on save (e.g. SAR contracting
  company from the chosen person; invoice legal-entity fields). For documents,
  prefer server-side `deriveFields`.

## Frontend special-cases (intentional, keep them thin)

- `m/[entity]/[id]` — generic record form; branches for documents (line/child
  editors, convert buttons), invoices (document generation panel with template
  picker), and dependent-field reset.
- `m/[entity]` (list) — generic; special-cases People (Yearly Cost column +
  `PeopleCostSummary` + Organization button) and read-only lists (row → parent).
- `org/` — HR org chart (`OrgChart`, self-contained SVG) with PNG/CSV export;
  resolves `department_id`→name and computes cost = hourly_rate × yearly_hours ×
  FTE%.
- `team/` — members, invites, **permissions matrix**, and **document templates**
  upload/manage (admin only).

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
