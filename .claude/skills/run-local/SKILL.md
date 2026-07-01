---
name: run-local
description: Run, test, or debug the HR/CRM/ERP app locally (backend Go API + Next.js frontend + Postgres + Redis). Use when the user wants to start the app, run the test suite, reset the database, or troubleshoot why the stack won't boot.
---

# Run the app locally

## Fastest: devcontainer

Open the repo in VS Code → "Reopen in Container". Postgres and Redis start
automatically; `post-create.sh` installs LibreOffice and warms caches. Then:

```bash
cd backend  && go run ./cmd/server      # API on :8080
cd frontend && npm run dev              # UI on :3000
```

## Without a devcontainer

Bring up the data services (or the whole stack) with Docker:

```bash
# whole stack (images built from source):
docker compose up --build

# OR just the databases, then run the apps on the host:
docker compose up -d db redis
cd backend  && DATABASE_URL='postgres://hrcrm:hrcrm@localhost:5432/hrcrm?sslmode=disable' \
               REDIS_URL='redis://localhost:6379/0' go run ./cmd/server
cd frontend && NEXT_PUBLIC_API_URL=http://localhost:8080 npm run dev
```

First boot runs migrations and generates all entity tables automatically
(`MIGRATE_ON_BOOT=true`).

## Run the backend tests

Tests need a Postgres. Spin one up and point `TEST_DATABASE_URL` at it:

```bash
docker run -d --name pg-test -e POSTGRES_USER=hrcrm -e POSTGRES_PASSWORD=hrcrm \
  -e POSTGRES_DB=hrcrm_test -p 55432:5432 postgres:16-alpine
cd backend
TEST_DATABASE_URL='postgres://hrcrm:hrcrm@localhost:55432/hrcrm_test?sslmode=disable' \
  go test -count=1 ./...
```

The suite covers auth, tenant isolation, generic CRUD, RBAC, invites, dashboard,
and document generation. The DOCX document tests run without LibreOffice; PDF
conversion is exercised by the smoke flow only.

## Frontend checks

```bash
cd frontend
npm run typecheck
npm run build
```

## Config reference

All backend settings are env vars (see `backend/internal/config/config.go` and
`.env.example`): `DATABASE_URL`, `REDIS_URL` (empty disables caching),
`JWT_SECRET`, `JWT_TTL`, `CORS_ORIGIN`, `SOFFICE_BIN`, `TEMPLATES_DIR`,
`BCRYPT_COST`, `MIGRATE_ON_BOOT`. In production `JWT_SECRET` must be set or the
server refuses to start.

## Troubleshooting

- **`database not reachable`**: Postgres isn't up yet; `db.Connect` retries for
  ~30s. Check `docker compose ps`.
- **`/healthz` returns `cache_down`**: `REDIS_URL` points at an unreachable
  Redis. Set it to a valid URL or leave it empty to disable caching.
- **PDF returns 503 `no_libreoffice`**: `soffice` isn't on PATH; use
  `?format=docx` or install LibreOffice.
- **CORS errors in the browser**: set `CORS_ORIGIN` to the frontend origin.
