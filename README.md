# HR · CRM · ERP

A multi-tenant web app to manage **teams, leads, offers and invoices (IGTRs)** —
the web successor to the `Excel_CRM_ERP_HR_OS` workbook. It covers HR, CRM, the
full sales (AR) and purchasing (AP) cycle, and contractor SAR tracking, and it
generates **Offer** and **IGTR** documents (DOCX + PDF) from your data.

Open-source, self-hostable, and built to be sold as a service: proper user
management, organizations (tenants) with email invites, and strict per-tenant
data segregation.

## Stack

| Layer     | Tech                                                            |
| --------- | -------------------------------------------------------------- |
| Frontend  | Next.js (App Router) + Tailwind CSS + TypeScript               |
| Backend   | Go (stdlib `net/http`), minimal deps: pgx, go-redis, jwt, uuid |
| Database  | PostgreSQL 16                                                  |
| Cache     | Redis 7 (optional)                                             |
| Documents | LibreOffice (headless) DOCX → PDF                              |
| Dev       | Devcontainer (app + Postgres + Redis)                          |
| CI/CD     | GitHub Actions → public images on GHCR + Docker Hub            |

## Design highlights

- **Metadata-driven engine.** All 23 business modules are defined once in
  `backend/internal/resource/catalog.go`. From that, the app generates the DB
  schema, the REST API, and the entire CRUD UI. Adding a module or field is a
  one-file change — no SQL, no handlers, no React. See the `add-module` skill.
- **Multi-tenancy.** Every row carries a `tenant_id`; all queries are scoped to
  the caller's active organization in the data layer. Self-service signup
  creates a new org (you become Owner); invite teammates by email with roles
  (owner / admin / member / viewer).
- **Stateless auth.** Email + password (bcrypt) → JWT carrying the active tenant
  and role. Switching org re-issues a token. Membership is re-checked on every
  request, so revoked access is immediate.
- **Per-user section permissions.** Admins use a matrix (Team → Section access)
  to grant/revoke each member access to sections (Master, HR, CRM, ERP, AP,
  SAR). Enforced server-side on every entity/document/dashboard call and applied
  to `/meta`, so restricted sections disappear from the UI. Owners/admins always
  have full access.
- **Documents.** Reproduces the original Excel/Word-VBA Offer & IGTR flows
  headless on Linux: fills `.docx` templates (scalar `{{Field}}` + repeating
  `{{line.col}}` table rows) and converts to PDF via LibreOffice.

## Modules

**HR org & costs:** Departments are configured under HR, each owning its Teams
(edited inline as a sub-collection). A Person picks a Department, then a Team
(the Team dropdown is filtered to that department and disabled until a
department is chosen), and carries their own FTE %, Yearly Work Hours and Hourly
Rate. Yearly cost = hourly rate × yearly hours × FTE%, shown per person (People
table column), per Team and per Department (a cost summary on the People page).
The People page → **Organization** renders a Department → Team visualization
with team leaders highlighted, internal/contractor counts and yearly cost per
team/department/overall, exportable as a **PNG image** (for slides) and **CSV**
(the underlying data + summary). No external chart dependency — it's a
self-contained SVG rasterized in the browser.

`Master` Legal Entities, Locations · `HR` People · `CRM` Accounts, Contacts,
Leads, Opportunities, Activities · `ERP/AR` Products, Quotes, Quote Lines, Sales
Orders, Order Lines, Invoices, Invoice Lines, Payments · `AP` Suppliers, Purchase
Orders, PO Lines, Vendor Bills, Bill Lines, AP Payments · `SAR` SAR Tracking.

## Quick start

### Devcontainer (recommended)

Open in VS Code → **Reopen in Container**. Postgres + Redis start automatically
and LibreOffice is installed. Then:

```bash
cd backend  && go run ./cmd/server     # API → http://localhost:8080
cd frontend && npm run dev             # UI  → http://localhost:3000
```

Sign up at <http://localhost:3000/signup> — you get a fresh organization.

### Docker Compose (whole stack)

```bash
cp .env.example .env        # set JWT_SECRET for anything non-local
docker compose up --build
```

Frontend on `:3000`, API on `:8080`, Postgres on `:5432`, Redis on `:6379`.

### Run on the host (databases in Docker)

```bash
docker compose up -d db redis
cd backend  && go run ./cmd/server
cd frontend && npm run dev
```

The backend runs migrations and creates all entity tables automatically on first
boot.

## Testing

```bash
docker run -d --name pg-test -e POSTGRES_USER=hrcrm -e POSTGRES_PASSWORD=hrcrm \
  -e POSTGRES_DB=hrcrm_test -p 55432:5432 postgres:16-alpine
cd backend
TEST_DATABASE_URL='postgres://hrcrm:hrcrm@localhost:55432/hrcrm_test?sslmode=disable' \
  go test -count=1 ./...
```

Covers auth, **tenant isolation**, generic CRUD + validation, RBAC, invites,
dashboard, and Offer/IGTR document generation. Frontend: `npm run typecheck &&
npm run build`.

## API overview

All under `/api/v1`. Auth via `Authorization: Bearer <token>`.

| Method                | Path                                  | Purpose                         |
| --------------------- | ------------------------------------- | ------------------------------- |
| POST                  | `/auth/signup`                        | Create user + organization      |
| POST                  | `/auth/login`                         | Get a token                     |
| GET                   | `/auth/me`                            | Current user, org, memberships  |
| POST                  | `/auth/switch-tenant`                 | Re-issue token for another org  |
| POST                  | `/auth/accept-invite`                 | Join an org via invite token    |
| GET/POST              | `/team/members`, `/team/invites`      | Team admin (admin only)         |
| GET/POST/DELETE       | `/tokens`, `/tokens/{id}`             | Manage your own API tokens      |
| GET/DELETE            | `/team/tokens`, `/team/tokens/{id}`   | All org API tokens (admin only) |
| POST                  | `/mcp`                                | MCP endpoint for AI agents      |
| GET                   | `/meta`                               | Entity catalog (drives the UI)  |
| GET                   | `/dashboard`                          | KPIs + record counts            |
| GET/POST              | `/entities/{entity}`                  | List / create                   |
| GET/PUT/DELETE        | `/entities/{entity}/{id}`             | Read / update / delete          |
| GET                   | `/documents/{offer\|igtr}/{invoice_id}?format=pdf\|docx` | Generate a document |

List query params: `page`, `page_size`, `q` (search), `sort`, `dir=desc`,
`f_<field>=value` (equality filter).

## MCP server (AI agents)

The app exposes a **Model Context Protocol** endpoint so agents (Claude Code,
GitHub Copilot CLI, etc.) can drive it directly. Like the REST API and UI, the
tool set is **generated from the module catalog** — every module gets
`list_/get_/create_/update_/delete_<module>` tools, documents (quotes, invoices,
…) get `get_doc_/save_doc_/convert_<module>`, plus `describe_schema` and
`dashboard`. Calls run through the same engine, so **tenancy and section
permissions are enforced** exactly as elsewhere, and a read-only token can read
but not write.

- **Endpoint:** `POST /api/v1/mcp` (Streamable HTTP transport).
- **Auth:** an API token, sent as `Authorization: Bearer <token>`.

### 1. Create an API token

In the UI: **Team → API tokens → Create token** (name it, optionally tick
**Read-only** / set an expiry). The token is shown **once** — copy it. It starts
with `hrcrm_pat_`. Or via the API:

```bash
curl -sX POST http://localhost:8080/api/v1/tokens \
  -H "Authorization: Bearer <your-login-jwt>" -H 'Content-Type: application/json' \
  -d '{"name":"claude-code","read_only":true}' | jq -r .token
```

A token inherits your role and section access, works until revoked (Team → API
tokens, admins see the whole org), and — unlike a login JWT — doesn't expire on
the short login cadence. Tip: for an agent, create a dedicated member (e.g. a
`viewer`, or a `member` restricted to some sections) and mint the token from it.

### 2a. Claude Code

```bash
claude mcp add --transport http hr-crm-erp http://localhost:8080/api/v1/mcp \
  --header "Authorization: Bearer hrcrm_pat_..."
```

Or commit a project-scoped `.mcp.json` (keep the secret in an env var):

```json
{
  "mcpServers": {
    "hr-crm-erp": {
      "type": "http",
      "url": "http://localhost:8080/api/v1/mcp",
      "headers": { "Authorization": "Bearer ${HRCRM_TOKEN}" }
    }
  }
}
```

### 2b. GitHub Copilot CLI

Run `/mcp add`, choose the **remote / HTTP** server type, enter the URL
`http://localhost:8080/api/v1/mcp`, and set the header
`{"Authorization": "Bearer hrcrm_pat_..."}` (stored in `~/.copilot/mcp-config.json`).

> If Copilot tries OAuth discovery and fails with `MCPOAuthError`, run
> `/mcp enable` for the server — this server authenticates by header, not OAuth.

Then ask the agent to `describe_schema` first, and it can list/search/create
across your data. Use `https://` in production (bearer tokens are plaintext over
HTTP).

## Documents

There are three ways to supply Offer/IGTR templates, in priority order:

1. **Upload via the UI** (recommended): admins upload `.docx` templates under
   **Team → Document templates**. When generating from an invoice, pick a
   template (or "Built-in default") per document type. Templates are stored
   per-tenant.
2. **`TEMPLATES_DIR` override**: point it at a directory containing
   `offer.docx`/`igtr.docx` (or `.xml`).
3. **Built-in defaults**: embedded in the binary from
   `backend/internal/documents/templates/{offer,igtr}.xml`.

Use `{{Field}}` placeholders for header values and a table row containing
`{{line.description}}`, `{{line.qty}}`, `{{line.unit_price}}`, `{{line.line_net}}`
for line items (field names match the workbook's `83_/84_Print_*_Data` sheets).
Uploaded Word docs work even if Word split a placeholder across runs — the
server normalizes them. PDF needs LibreOffice (bundled in the backend image and
devcontainer); DOCX works anywhere. See the `document-templates` skill.

## Deployment & images

On merge to `main`, GitHub Actions (`.github/workflows/release.yml`) builds and
pushes `-backend` and `-frontend` images to **both** GHCR and Docker Hub:

- `ghcr.io/<owner>/<repo>-{backend,frontend}`
- `docker.io/<dockerhub-user>/<repo>-{backend,frontend}`

tagged `latest`, the short SHA, and semver on `v*` tags. Docker Hub publishing
needs two repo secrets: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` (a Read &
Write access token). **After the first push, mark both GHCR packages Public**
(package → Settings → Change visibility); this is a one-time GitHub setting.

The frontend image is **portable**: the browser API base URL is resolved at
runtime — set the `API_URL` env var on the frontend container to your backend's
public URL (injected into the page by `frontend/app/layout.tsx`), so the same
image runs in any environment without a rebuild.

Required production env:

- **Backend**: `DATABASE_URL`, `JWT_SECRET` (mandatory), `CORS_ORIGIN` (must
  match the frontend's public origin), `REDIS_URL` (optional).
- **Frontend**: `API_URL` (the backend's public URL).

See `.env.example`.

## AI skills for maintenance

This repo ships Claude Code skills in `.claude/skills/` to speed up future work:

- **add-module** — add/modify a business module (entity) end-to-end.
- **document-templates** — edit/add Offer & IGTR templates and merge fields.
- **db-migrations** — schema layers and how to add migrations/indexes.
- **run-local** — run, test, reset, and troubleshoot the stack.

## Repository layout

```
backend/         Go API (cmd/server, internal/{config,db,httpx,cache,auth,resource,documents,mcpsrv,server})
frontend/        Next.js + Tailwind app (app/, components/, lib/)
templates/       Offer & IGTR document templates (editable XML bodies)
.devcontainer/   Dev environment (app + Postgres + Redis + LibreOffice)
.github/workflows ci.yml (tests) · release.yml (GHCR images)
docker-compose.yml  Local production-like stack
```

## License

MIT — see `LICENSE`.
