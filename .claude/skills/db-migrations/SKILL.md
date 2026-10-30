---
name: db-migrations
description: Work with the database schema and migrations for the HR/CRM/ERP app. Use when the user wants to add a hand-written SQL migration (for core/platform tables like users, tenants, documents), understand how entity tables are generated, add an index, or reset the schema.
---

# Database & migrations

The schema has two layers:

## 1. Core platform tables — hand-written SQL migrations

Tables for the platform itself (`tenants`, `users`, `memberships`, `invites`,
`documents`, `schema_migrations`) live in
`backend/internal/db/migrations/*.sql`. They are embedded with `go:embed` and
applied once each, in filename order, by `db.Migrate` on boot.

To add one:

1. Create `backend/internal/db/migrations/0002_<name>.sql` (zero-padded number
   keeps ordering). Write idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER
   TABLE ... ADD COLUMN IF NOT EXISTS`).
2. Restart the backend. The runner records the version in `schema_migrations`;
   it never re-runs an applied file.

There is no "down" migration by design (keeps the runner tiny). To change an
applied migration, write a new forward migration.

## 2. Business entity tables — generated from metadata

Tables named `ent_<key>` (e.g. `ent_invoices`) are **not** in the SQL files.
They are created/evolved by `resource.EnsureSchema` (`internal/resource/
schema.go`) from the catalog in `catalog.go`, every boot:

- `CREATE TABLE IF NOT EXISTS ent_<key> (id, tenant_id, created_at, updated_at)`
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS <field> <type>` per catalog field
- `CREATE INDEX IF NOT EXISTS idx_<key>_tenant ON ent_<key>(tenant_id)`

So **adding a field never needs a SQL migration** — just edit `catalog.go`
(see the `add-module` skill). Column types map from field types in `field.go`.

### Adding a custom index on an entity table

If you need an index beyond the automatic `tenant_id` one (e.g. on
`(tenant_id, status)` for invoices), add a hand-written migration in layer 1
that does `CREATE INDEX IF NOT EXISTS ... ON ent_invoices (...)`. The entity
table is guaranteed to exist because migrations and `EnsureSchema` both run at
boot — but ordering matters: `db.Migrate` runs **before** `EnsureSchema` in
`main.go`. If your index depends on a generated column, either move the index
into `EnsureSchema` or guard with `IF EXISTS`/create the column there first.

**Data backfills that touch entity columns** go in
`internal/db/datamigrations/NNNN_name.sql` instead: `db.MigrateData` applies
them once each *after* `EnsureSchema` (from `server.New`), so newly added
catalog columns exist. Guard references to retired entity tables with
`to_regclass('ent_x') IS NOT NULL` (fresh databases never create them).

## Reset the database

```bash
docker compose down -v        # drops the pgdata volume
docker compose up -d db       # fresh; app recreates everything on next boot
```

## Notes

- Tenancy is row-level via `tenant_id`; there is no Postgres RLS in this build
  (app-enforced, per project decision). Every entity query in `repo.go` filters
  by `tenant_id` — keep it that way.
- UUID primary keys use `gen_random_uuid()` (pgcrypto, enabled in `0001_core`).
