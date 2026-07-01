---
name: add-module
description: Add or modify a business module (entity) in the HR/CRM/ERP app. Use when the user wants a new data type (table) with full CRUD UI + API, or wants to add/rename/remove fields on an existing module, change picklist options, or adjust which fields show in the list view. The app is metadata-driven, so this is almost always a single-file change in the Go catalog.
---

# Add or modify a module

This app is **metadata-driven**. Every business module (people, accounts,
invoices, …) is defined once as an `Entity` in
`backend/internal/resource/catalog.go`. From that definition the app
automatically generates:

- the PostgreSQL table (`ent_<key>`) and its columns,
- the REST API at `/api/v1/entities/<key>` (list/get/create/update/delete),
- the navigation entry, list table, and create/edit form in the frontend.

**You almost never write SQL, handlers, or React for a new module.**

## Steps to add a module

1. Open `backend/internal/resource/catalog.go`.
2. Add an `Entity{...}` to the slice returned by `Catalog()`. Use the field
   constructors from `field.go` (`txt`, `long`, `num`, `money`, `pct`, `intf`,
   `date`, `boolf`, `sel`, `ref`, `email`) and wrap with `req(...)` for required
   fields or `hidden(...)` to keep a field out of the list table.

   ```go
   {
       Key: "assets", Label: "Asset", Plural: "Assets",
       Group: "Master", TitleField: "name", Icon: "box",
       Fields: []Field{
           req(txt("name", "Name")),
           sel("status", "Status", "Active", "Retired"),
           ref("owner_id", "Owner", "people", "full_name"),
           money("value", "Book Value"),
           date("purchased_on", "Purchased On"),
           long("notes", "Notes"),
       },
   },
   ```

3. Choose a unique `Key` (lowercase, snake_case — it becomes the URL and table
   suffix). `Icon` is any [lucide](https://lucide.dev/icons) icon name in kebab
   case. `Group` controls sidebar grouping (existing: Master, HR, CRM, ERP, AP,
   SAR).
4. Restart the backend (`go run ./cmd/server`). On boot, `EnsureSchema` creates
   the table and adds any new columns via `ADD COLUMN IF NOT EXISTS` — existing
   data is preserved.
5. The frontend picks up the new module automatically from `/api/v1/meta`.

## Modifying an existing module

- **Add a field:** add a `Field` to the entity; the column is created on next
  boot. No migration needed.
- **Change picklist options:** edit the relevant slice at the top of
  `catalog.go` (e.g. `invoiceStatus`) or the inline `sel(...)` options.
- **Rename a field label:** change the `Label` only. Changing the `Key` renames
  the column at the DB level — the old column is NOT dropped automatically
  (data stays under the old name); prefer keeping keys stable.
- **Reference another module:** use `ref("<key>_id", "Label", "<target_entity>",
  "<title_field_on_target>")`. The form renders a dropdown; the table shows the
  target's title.

## Field types

`text, longtext, int, number, money, percent, date, datetime, bool, select,
ref, email` — see `field.go` for the SQL mapping (money/number/percent →
`DOUBLE PRECISION`, etc.).

## Verify

- `cd backend && go build ./... && go vet ./...`
- Add a test case if the module has special behavior; generic CRUD is already
  covered by `internal/server/api_test.go`. Run:
  `TEST_DATABASE_URL=... go test ./...`

## Gotchas

- Tenancy is automatic: every generated table has `tenant_id` and all queries
  are scoped by it (`repo.go`). Never add your own tenant filtering.
- Keep `Key` stable once data exists.
- The dashboard (`handlers.go Dashboard`) hard-codes a few KPIs (invoices AR,
  vendor_bills AP, opportunities pipeline). If you rename those entities/fields,
  update the dashboard too.
