# Azure fee billing — setup

The app reads each subscription's **actual daily cost** from Azure Cost
Management, bills a fee on it (TAN 25%, TSS 27% by default), and plans/invoices
it quarterly through Service Agreements. This guide connects one Azure tenant;
repeat it for each additional tenant.

## 1. Create a read-only service principal (once per Azure tenant)

The app signs in with the OAuth client-credentials flow, so it needs an app
registration with a client secret and the **Cost Management Reader** role. Give
it the role on the tenant's **root management group**: every subscription,
including ones created later, inherits it.

```bash
az login --tenant <TENANT_ID>

# Root management group id = the tenant id.
ROOT_MG=/providers/Microsoft.Management/managementGroups/<TENANT_ID>

az ad sp create-for-rbac \
  --name hrcrm-cost-reader \
  --role "Cost Management Reader" \
  --scopes "$ROOT_MG" \
  --years 2
```

The command prints `appId` (Client ID), `password` (client secret) and
`tenant` (Directory ID). The secret is shown **once**, so copy it now.

Notes:

- Assigning a role at the root management group needs `Owner` or
  `User Access Administrator` there. A Global Administrator can grant that to
  themselves under **Entra ID → Properties → Access management for Azure
  resources**. If you'd rather not, assign the role per subscription instead:
  ```bash
  APP_ID=<appId>
  for SUB in $(az account list --query "[?tenantId=='<TENANT_ID>'].id" -o tsv); do
    az role assignment create --assignee "$APP_ID" --role "Cost Management Reader" --scope "/subscriptions/$SUB"
  done
  ```
  (With per-subscription assignments, new subscriptions must be added by hand.)
- `Cost Management Reader` includes `Microsoft.Resources/subscriptions/read`,
  so the app can also discover the subscriptions. It cannot change anything.
- Rotate the secret before it expires:
  `az ad app credential reset --id <appId> --years 2 --append`, then paste the
  new value into the app (step 2).
- In the portal: **Entra ID → App registrations → New registration**, then
  **Certificates & secrets → New client secret**. Then go to **Management
  groups → Tenant Root Group → Access control (IAM) → Add role assignment →
  Cost Management Reader** and pick the app.

## 2. Connect it in the app

1. **Azure → Azure Tenants → New**. Enter a name, the **Directory (Tenant) ID**
   and the **Client (App) ID**. Leave **Daily Sync** on, then save.
2. On the record, admins paste the **client secret** (and optionally its
   expiry date) and click **Save secret**. The secret is stored encrypted
   (AES-GCM with `SECRETS_KEY`) and is never shown again.
3. Click **Test connection**. It should list the visible subscriptions.
4. Back-fill history: set **Back-fill from** to e.g. 12 months ago and click
   **Sync now**. The sync runs in the background; the record shows its
   status, *Last Sync* and *Usage Synced Through*.

After that, a daily job re-syncs from the first day of the previous month to
today, which picks up Microsoft's late cost adjustments. Set
`AZURE_SYNC_EVERY` to change the interval (`0` = manual only).

## Fallback: import a CSV from the portal

If the service principal isn't set up yet (or you only have portal access),
load usage from a Cost analysis download instead:

1. **Cost Management → Cost analysis**. Pick the widest scope you can see and
   the **Daily costs** view. Set *Actual cost*, a custom date range, granularity
   **Daily**, and group by **Subscription** (adding *Service name* is
   optional). Then **Download → CSV**.
2. In the app: **Azure Dashboard → Import CSV**. Pick the Azure tenant (only
   a name and Directory ID are needed, no secret), choose the file and click
   **Check file**.
3. Review the preview:
   - **New days** are always added.
   - **Unchanged** days are skipped.
   - **Changes** are days already in the app whose total differs. Tick the
     ones you want to overwrite; unticked ones keep their stored value.
4. Click **Import**. Subscriptions not in the app yet are created, unmapped.
   The tenant's *Usage Synced Through* advances to the file's last day
   (yesterday at most).

The file is compared per subscription and day, and days in the app that
aren't in the file are never deleted. A later API sync of the same dates
simply replaces those days. A monthly-granularity file is accepted with a
warning, but daily data is needed for accurate forecasts and partial quarters.

## 3. Map subscriptions

The sync creates every subscription it finds, unmapped. Open **Azure → Azure
Subscriptions** (use the *Unmapped* / *No type* views) and set on each:

- **Account**: the customer who pays its fees. One account can have many
  subscriptions.
- **Type**: `TAN` or `TSS`.
- **Fee % (override)**: only when this subscription has a special rate.

Rates live in **Azure → Azure Fee Rates**: one row per type, with an optional
*Valid From*. A rate applies from the month of its *Valid From*. Usage before
that keeps the earlier rate. With no rows, TAN = 25% and TSS = 27%.

## 4. Agreements and quarterly invoices

- **Azure Dashboard → New Azure agreement**: pick the account and period. You
  get a Draft Service Agreement with one line and one installment per calendar
  quarter. Quarters that have already passed are priced from **real usage**,
  which covers billing a period that was free. Later quarters use the
  **forecast**: each subscription's 90-day daily average × days × fee %.
  Markup % and VAT % work as on any agreement. You can still edit everything.
  The agreement's **Notes** list the actual usage and fee of each mapped
  subscription, in total and per quarter, through the last synced day. That
  block is rewritten on every refresh or invoice. Text you write above or
  below it is kept.
- On the agreement, the **Azure fees by quarter** panel shows each quarter's
  basis (Forecast / Actual / Invoiced). **Create invoice** bills a quarter with
  one line per subscription (usage × fee %). It refuses quarters whose usage
  isn't fully synced yet unless you confirm.
- **The invoice replaces the forecast.** The quarter's line and installment
  take the invoice's amounts and follow later edits. Cancelling or deleting the
  invoice re-prices the quarter from usage. Payments are registered against
  the installment as usual, and paying it marks the invoice Paid.
- **Refresh forecast** re-prices all quarters that aren't invoiced yet. This
  replaces manual edits to those lines.

## Things to know

- **Latency**: Cost Management data is typically 8–24 h behind, so the last
  synced day is treated as incomplete ("synced through" = the day before).
- **History**: the Query API goes back about 13 months. For anything older, set
  up a Cost Management *export* and ask for an import path.
- **Currency**: pay-as-you-go costs come in the subscription's billing
  currency. Invoices and agreements take the account's currency. If they
  differ, the invoice is created with a warning; no FX conversion is done.
- **Throttling**: Cost Management rate-limits queries. The sync queries one
  month per subscription per call and backs off on HTTP 429.
- **Another tenant**: add another Azure Tenant record with its own service
  principal. Subscriptions keep a link to the tenant they came from, and the
  dashboard can filter by it.
