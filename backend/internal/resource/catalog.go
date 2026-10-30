package resource

// Picklists carried over from the workbook's 01_Setup sheet.
var (
	leadStatus   = []string{"New", "Contacted", "Qualified", "Unqualified", "Converted"}
	oppStage     = []string{"Prospecting", "Discovery", "Proposal", "Negotiation", "Won", "Lost"}
	activityType = []string{"Call", "Email", "Meeting", "Task"}
	activityStat = []string{"Open", "Completed", "Cancelled"}
	quoteStatus  = []string{"Draft", "Sent", "Accepted", "Rejected", "Expired"}
	// Service Agreements (entity key "invoices"). The original invoice statuses
	// are kept so existing rows stay valid; agreement lifecycle states follow.
	invoiceStatus = []string{"Draft", "Issued", "Paid", "Overdue", "Cancelled", "Sent", "Signed", "Active", "Completed"}
	billingFreq   = []string{"One-off", "Monthly", "Quarterly", "Semi-annual", "Annual", "Milestones"}
	scheduleStat  = []string{"Pending", "Partially Paid", "Paid"}
	// An agreement's payment position, derived from its schedule and payments.
	paymentStatus = []string{"Unscheduled", "Not Paid", "Partially Paid", "Paid"}
	// Own = our companies (sellers, employers); the rest are third parties.
	entityType    = []string{"Own", "Customer", "Supplier", "Partner"}
	contactRole   = []string{"Primary", "Buying", "Billing", "Legal", "Technical", "Other"}
	paymentMethod = []string{"Bank Transfer", "Card", "Cash", "Direct Debit", "IGTR"}
	uom           = []string{"EA", "HOUR", "DAY", "MONTH"}
	taxCode       = []string{"VAT23", "VAT13", "VAT6", "VAT0"}
	productType   = []string{"Product", "Service"}
	workerType    = []string{"Internal", "Contractor"}
	employStatus  = []string{"Active", "Onboarding", "Exited", "On Hold"}
	engagement    = []string{"Body Leasing", "Statement of Work", "Fixed Price"}
	apDocStatus   = []string{"Draft", "Issued", "Partially Paid", "Paid", "Cancelled"}
	accountType   = []string{"Customer", "Prospect", "Partner"}
	leadSource    = []string{"Web", "Referral", "Event", "Outbound", "Other"}
	// Azure subscription types; each has a fee % (see AzureFeeRates).
	azureSubType = []string{"TAN", "TSS"}
	// Invoices (entity sales_invoices); Paid is derived from the installment.
	salesInvStatus = []string{"Draft", "Issued", "Paid", "Cancelled"}
	// How an Azure agreement line's amount was obtained.
	azureBasis = []string{"Forecast", "Actual", "Invoiced"}
)

// Catalog returns all business entities. This is the single source of truth that
// drives the database schema, the API, and the frontend UI.
func Catalog() []Entity {
	return []Entity{
		// ---------------- Master data ----------------
		{
			Key: "legal_entities", Label: "Legal Entity", Plural: "Legal Entities",
			Group: "Master", TitleField: "legal_name", Icon: "building",
			Columns: []string{"legal_name", "entity_type", "country", "vat_id", "default_currency", "billing_email"},
			Views: []View{
				{"Our entities", "f_entity_type=Own"},
				{"Customers", "f_entity_type=Customer"},
				{"Suppliers", "f_entity_type=Supplier"},
			},
			Fields: []Field{
				req(txt("legal_name", "Legal Name")),
				withHelp(withDefault(sel("entity_type", "Entity Type", entityType...), "Customer"),
					"Own = one of our companies (selectable as seller / employer)."),
				txt("country", "Country"),
				txt("vat_id", "VAT ID"),
				txt("registration_no", "Registration #"),
				long("billing_address", "Billing Address"),
				txt("bank_iban", "Bank / IBAN"),
				txt("swift", "SWIFT"),
				txt("default_currency", "Default Currency"),
				hidden(email("email", "General Email")),
				hidden(email("billing_email", "Billing Email")),
				hidden(email("legal_email", "Legal Email")),
				hidden(txt("phone", "Phone")),
				hidden(txt("website", "Website")),
				// Who signs Offers / Service Agreements on this entity's behalf.
				hidden(txt("signatory_first_name", "Signatory First Name")),
				hidden(txt("signatory_last_name", "Signatory Last Name")),
				hidden(txt("signatory_title", "Signatory Title")),
				hidden(email("signatory_email", "Signatory Email")),
				long("notes", "Notes"),
			},
		},
		{
			Key: "locations", Label: "Location", Plural: "Locations",
			Group: "Master", TitleField: "location_name", Icon: "map-pin",
			Fields: []Field{
				req(txt("location_name", "Location Name")),
				txt("country", "Country"),
				txt("city", "City"),
				txt("time_zone", "Time Zone"),
				long("notes", "Notes"),
			},
		},
		// ---------------- HR ----------------
		{
			// A department groups one or more teams (edited inline below).
			Key: "departments", Label: "Department", Plural: "Departments",
			Group: "HR", TitleField: "name", Icon: "building-2",
			Children: []Child{{Entity: "teams", FK: "department_id", Label: "Teams"}},
			Fields: []Field{
				req(txt("name", "Department Name")),
				long("notes", "Notes"),
			},
		},
		{
			// Teams are managed as a sub-collection of their department.
			Key: "teams", Label: "Team", Plural: "Teams",
			Group: "HR", TitleField: "name", Icon: "users", Hidden: true,
			Fields: []Field{
				ref("department_id", "Department", "departments", "name"),
				req(txt("name", "Team Name")),
				long("notes", "Notes"),
			},
		},
		{
			Key: "people", Label: "Person", Plural: "People",
			Group: "HR", TitleField: "full_name", Icon: "users",
			Fields: []Field{
				req(txt("full_name", "Full Name")),
				sel("worker_type", "Worker Type", workerType...),
				email("email", "Email"),
				txt("phone", "Phone"),
				txt("role", "Role"),
				ref("department_id", "Department", "departments", "name"),
				filteredBy(lookup("team", "Team", "teams", "name", "name"), "department_id", "department_id"),
				boolf("team_leader", "Team Leader"),
				ref("location_id", "Location", "locations", "location_name"),
				filledFrom(txt("country", "Country"), "location_id", "country"),
				hidden(filledFrom(txt("city", "City"), "location_id", "city")),
				hidden(filledFrom(txt("time_zone", "Time Zone"), "location_id", "time_zone")),
				sellerRef("legal_employer_id", "Legal Employer"),
				// Contractors are engaged through a (third-party) contracting
				// company; internals have none.
				disabledWhen(ref("contracting_company_id", "Contracting Company", "legal_entities", "legal_name"), "worker_type", "Internal"),
				sel("engagement_type", "Engagement Type", engagement...),
				date("start_date", "Start Date"),
				date("end_date", "End Date"),
				sel("status", "Status", employStatus...),
				pct("fte_pct", "FTE %"),
				num("yearly_hours", "Yearly Work Hours"),
				money("hourly_rate", "Hourly Rate"),
				txt("cost_center", "Cost Center"),
				txt("manager", "Manager"),
				long("notes", "Notes"),
			},
		},
		// ---------------- CRM ----------------
		{
			Key: "accounts", Label: "Account", Plural: "Accounts",
			Group: "CRM", TitleField: "account_name", Icon: "briefcase",
			Children: []Child{{Entity: "contacts", FK: "account_id", Label: "Contacts"}},
			Fields: []Field{
				req(txt("account_name", "Account Name")),
				sel("type", "Type", accountType...),
				// The customer's legal (buying) entity. When chosen, the buyer
				// details below mirror it (read-only); without one they're typed in.
				withHelp(ref("buying_entity_id", "Buying Entity", "legal_entities", "legal_name"),
					"The customer's legal entity; fills the buyer details below."),
				filledFrom(txt("legal_entity_name", "Buying Entity Name"), "buying_entity_id", "legal_name"),
				filledFrom(long("legal_entity_address", "Buying Entity Address"), "buying_entity_id", "billing_address"),
				filledFrom(txt("vat_id", "VAT ID"), "buying_entity_id", "vat_id"),
				filledFrom(txt("country", "Country"), "buying_entity_id", "country"),
				hidden(filledFrom(txt("currency", "Currency"), "buying_entity_id", "default_currency")),
				txt("industry", "Industry"),
				// Our selling legal entity that bills this customer; the default
				// seller on the account's quotes and service agreements.
				withHelp(sellerRef("legal_entity_id", "Our Billing Legal Entity (seller)"),
					"Default seller on this account's quotes and service agreements."),
				txt("city", "City"),
				filledFrom(txt("website", "Website"), "buying_entity_id", "website"),
				filledFrom(txt("phone", "Phone"), "buying_entity_id", "phone"),
				filledFrom(email("email", "Email"), "buying_entity_id", "email"),
				hidden(filledFrom(email("billing_email", "Billing / AP Email"), "buying_entity_id", "billing_email")),
				hidden(email("buying_email", "Buying / Procurement Email")),
				hidden(filledFrom(email("legal_email", "Legal Email"), "buying_entity_id", "legal_email")),
				txt("owner", "Owner"),
				intf("payment_terms_days", "Payment Terms (days)"),
				date("created_date", "Created Date"),
				long("notes", "Notes"),
			},
		},
		{
			// Contacts are edited as a sub-collection of their Account, but are
			// also browsable here as a read-only, searchable directory.
			Key: "contacts", Label: "Contact", Plural: "Contacts",
			Group: "CRM", TitleField: "last_name", Icon: "user", ReadOnly: true,
			Fields: []Field{
				ref("account_id", "Account", "accounts", "account_name"),
				txt("first_name", "First Name"),
				req(txt("last_name", "Last Name")),
				txt("title", "Title"),
				sel("contact_role", "Contact Role", contactRole...),
				email("email", "Email"),
				txt("phone", "Phone"),
				txt("city", "City"),
				txt("country", "Country"),
				txt("owner", "Owner"),
				date("created_date", "Created Date"),
				long("notes", "Notes"),
				// The account's buying entity, kept in step on account saves so a
				// lead can offer the contacts of its company.
				ro(hidden(ref("buying_entity_id", "Buying Entity", "legal_entities", "legal_name"))),
			},
		},
		{
			Key: "leads", Label: "Lead", Plural: "Leads",
			Group: "CRM", TitleField: "company", Icon: "target",
			Fields: []Field{
				// Pick the company from Legal Entities (fills name + country,
				// read-only), or type a name for a prospect not on file yet.
				ref("legal_entity_id", "Company (Entity)", "legal_entities", "legal_name"),
				req(filledFrom(txt("company", "Company"), "legal_entity_id", "legal_name")),
				// An existing contact of the company's account; picking one fills
				// the contact details below (still editable).
				withHelp(filteredBy(ref("contact_id", "Contact", "contacts", "last_name"), "legal_entity_id", "buying_entity_id"),
					"Pick a known contact of this company to fill the details below."),
				txt("contact_first_name", "Contact First Name"),
				req(txt("contact_last_name", "Contact Last Name")),
				email("email", "Email"),
				txt("phone", "Phone"),
				sel("source", "Source", leadSource...),
				filledFrom(txt("country", "Country"), "legal_entity_id", "country"),
				sel("status", "Status", leadStatus...),
				txt("owner", "Owner"),
				date("created_date", "Created Date"),
				date("next_action_date", "Next Action Date"),
				long("notes", "Notes"),
				// Set when the lead is converted (Convert to Opportunity); links
				// back to the created Account/Opportunity and blocks re-conversion.
				ro(hidden(ref("converted_account_id", "Converted Account", "accounts", "account_name"))),
				ro(hidden(ref("converted_opportunity_id", "Converted Opportunity", "opportunities", "opportunity_name"))),
			},
		},
		{
			Key: "opportunities", Label: "Opportunity", Plural: "Opportunities",
			Group: "CRM", TitleField: "opportunity_name", Icon: "trending-up",
			Fields: []Field{
				ref("account_id", "Account", "accounts", "account_name"),
				hidden(contactRef()),
				req(txt("opportunity_name", "Opportunity Name")),
				sel("stage", "Stage", oppStage...),
				pct("probability_pct", "Probability %"),
				date("expected_close_date", "Expected Close"),
				money("amount_net", "Amount (Net)"),
				money("weighted_amount", "Weighted Amount"),
				txt("owner", "Owner"),
				date("created_date", "Created Date"),
				long("notes", "Notes"),
			},
		},
		{
			Key: "activities", Label: "Activity", Plural: "Activities",
			Group: "CRM", TitleField: "subject", Icon: "calendar",
			Fields: []Field{
				txt("related_type", "Related Type"),
				txt("related_id", "Related ID"),
				sel("activity_type", "Activity Type", activityType...),
				req(txt("subject", "Subject")),
				date("due_date", "Due Date"),
				sel("status", "Status", activityStat...),
				txt("owner", "Owner"),
				date("completed_date", "Completed Date"),
				long("notes", "Notes"),
			},
		},
		// ---------------- ERP / AR ----------------
		{
			Key: "products", Label: "Product", Plural: "Products",
			Group: "ERP", TitleField: "name", Icon: "package",
			Fields: []Field{
				req(txt("sku", "SKU")),
				req(txt("name", "Name")),
				sel("type", "Type", productType...),
				txt("category", "Category"),
				sel("uom", "UoM", uom...),
				money("sales_price", "Sales Price"),
				money("cost", "Cost"),
				sel("tax_code", "Tax Code", taxCode...),
				boolf("active", "Active"),
				long("notes", "Notes"),
			},
		},
		{
			Key: "quotes", Label: "Quote", Plural: "Quotes",
			Group: "ERP", TitleField: "description", Icon: "file-text",
			Columns:  []string{"description", "account_id", "quote_date", "status", "valid_until", "agreement_status", "net_total", "grand_total"},
			Children: []Child{{Entity: "quote_lines", FK: "quote_id", Label: "Line Items"}},
			Views: []View{
				{"Open", "fin_status=Draft,Sent"},
				{"Open, past valid date", "fin_status=Draft,Sent&fmax_valid_until=@today-1"},
				{"Accepted, no agreement yet", "f_status=Accepted&f_agreement_status=@empty"},
				{"Contracted", "fin_agreement_status=" + agreementLive},
				{"Lost", "fin_status=Rejected,Expired"},
			},
			Fields: concat(
				[]Field{
					ref("account_id", "Account", "accounts", "account_name"),
					req(txt("description", "Description")),
					date("quote_date", "Quote Date"),
					sel("status", "Status", quoteStatus...),
					date("valid_until", "Valid Until"),
					ref("opportunity_id", "Opportunity", "opportunities", "opportunity_name"),
					// Status of the latest service agreement made from this quote.
					ro(sel("agreement_status", "Agreement Status", invoiceStatus...)),
				},
				partyFields(),
				[]Field{
					hidden(date("service_start_date", "Service Start Date")),
					hidden(date("service_end_date", "Service End Date")),
				},
				pricingFields(false),
				[]Field{long("notes", "Notes")},
			),
		},
		lineEntity("quote_lines", "Quote Line", "ERP", "quote_id", "Quote", "quotes", "description"),
		{
			// Service Agreement. The key stays "invoices" (table, API routes, MCP
			// tool names and stored references depend on it); only the labels
			// changed. Besides its line items it carries a payment schedule, and
			// its payment position is derived from that schedule and the
			// registered payments (see payments.go).
			Key: "invoices", Label: "Service Agreement", Plural: "Service Agreements",
			Group: "ERP", TitleField: "quote_description", Icon: "file-pen-line",
			Columns: []string{"quote_description", "account_id", "invoice_date", "status", "payment_status", "next_due_date", "grand_total", "balance"},
			Children: []Child{
				{Entity: "invoice_lines", FK: "invoice_id", Label: "Line Items"},
				{Entity: "payment_schedule", FK: "invoice_id", Label: "Payment Schedule"},
			},
			Views: []View{
				{"Active", "fin_status=Signed,Active"},
				{"Scheduled payments outstanding", "fin_payment_status=Not Paid,Partially Paid"},
				{"Overdue", "fin_payment_status=Not Paid,Partially Paid&fmax_next_due_date=@today-1"},
				{"Due in 30 days", "fmin_next_due_date=@today&fmax_next_due_date=@today+30"},
				{"No payment schedule", "f_payment_status=Unscheduled"},
				{"Fully paid", "f_payment_status=Paid"},
				{"Ending in 90 days", "fmin_service_end_date=@today&fmax_service_end_date=@today+90"},
				{"Azure fees", "f_azure_fees=true"},
			},
			Fields: concat(
				[]Field{
					ref("account_id", "Account", "accounts", "account_name"),
					req(txt("quote_description", "Description")),
					date("invoice_date", "Agreement Date"),
					sel("status", "Status", invoiceStatus...),
					// Set by "Create Service Agreement" on a quote; read-only.
					ro(ref("quote_id", "Quote", "quotes", "description")),
					ro(ref("opportunity_id", "Opportunity", "opportunities", "opportunity_name")),
					// Agreements created before Sales Orders were retired keep
					// their link for history (the module itself is gone).
					ro(hidden(txt("sales_order_id", "Sales Order (legacy)"))),
				},
				partyFields(),
				[]Field{
					date("service_start_date", "Service Start Date"),
					date("service_end_date", "Service End Date"),
					sel("billing_frequency", "Billing Frequency", billingFreq...),
					hidden(date("due_date", "Due Date")),
					// Bills the fees on the account's Azure subscriptions per
					// quarter (lines + schedule built by the Azure module).
					hidden(boolf("azure_fees", "Azure Fees Agreement")),
				},
				pricingFields(true),
				[]Field{
					ro(sel("payment_status", "Payment Status", paymentStatus...)),
					ro(date("next_due_date", "Next Payment Due")),
					ro(hidden(money("scheduled_total", "Scheduled Total"))),
					ro(hidden(intf("installments", "Installments"))),
					ro(hidden(intf("installments_paid", "Installments Paid"))),
					long("notes", "Notes"),
				},
			),
		},
		withFields(lineEntity("invoice_lines", "Service Agreement Line", "ERP", "invoice_id", "Service Agreement", "invoices", "quote_description"),
			// Azure fee agreements: the quarter a line covers and whether its
			// amount is a forecast, actual usage or the quarter's invoice.
			hidden(date("period_start", "Period Start")),
			hidden(date("period_end", "Period End")),
			hidden(sel("basis", "Basis", azureBasis...)),
		),
		{
			// One due payment of a Service Agreement. Managed inside the
			// agreement: amounts left blank split the remaining total evenly, and
			// the schedule must add up to the agreement's grand total. Paid
			// amount and status are derived from the registered payments.
			Key: "payment_schedule", Label: "Scheduled Payment", Plural: "Payment Schedule",
			Group: "ERP", TitleField: "label", Icon: "calendar-clock", Hidden: true,
			Fields: []Field{
				ref("invoice_id", "Service Agreement", "invoices", "quote_description"),
				ro(intf("line_no", "#")),
				req(date("due_date", "Due Date")),
				money("amount", "Amount"),
				txt("description", "Description"),
				ro(money("paid_amount", "Paid")),
				ro(sel("status", "Status", scheduleStat...)),
				// "31-03-2026 · 1000.00 · Q1" — the option label when picking an
				// installment on a payment.
				ro(hidden(txt("label", "Label"))),
				// The period an installment pays for (Azure quarterly billing).
				hidden(date("period_start", "Period Start")),
				hidden(date("period_end", "Period End")),
			},
		},
		{
			// Invoice: bills a period of a service agreement (e.g. a quarter of
			// Azure fees) and settles the agreement installment for it, so its
			// paid amount and status follow that installment's payments.
			Key: "sales_invoices", Label: "Invoice", Plural: "Invoices",
			Group: "ERP", TitleField: "description", Icon: "receipt",
			Columns:  []string{"invoice_number", "description", "account_id", "invoice_date", "due_date", "status", "grand_total", "balance"},
			Children: []Child{{Entity: "sales_invoice_lines", FK: "sales_invoice_id", Label: "Line Items"}},
			Views: []View{
				{"Open", "fin_status=Draft,Issued"},
				{"Overdue", "f_status=Issued&fmax_due_date=@today-1"},
				{"Paid", "f_status=Paid"},
			},
			Fields: concat(
				[]Field{
					ref("account_id", "Account", "accounts", "account_name"),
					req(txt("description", "Description")),
					txt("invoice_number", "Invoice #"),
					date("invoice_date", "Invoice Date"),
					date("due_date", "Due Date"),
					withDefault(sel("status", "Status", salesInvStatus...), "Draft"),
					ref("agreement_id", "Service Agreement", "invoices", "quote_description"),
					withHelp(filteredBy(ref("schedule_id", "Installment", "payment_schedule", "label"), "agreement_id", "invoice_id"),
						"The agreement installment this invoice settles; its amount follows the invoice."),
					date("period_start", "Period Start"),
					date("period_end", "Period End"),
				},
				partyFields(),
				pricingFields(true),
				[]Field{long("notes", "Notes")},
			),
		},
		withFields(lineEntity("sales_invoice_lines", "Invoice Line", "ERP", "sales_invoice_id", "Invoice", "sales_invoices", "description"),
			hidden(ref("azure_subscription_id", "Azure Subscription", "azure_subscriptions", "display_name")),
			hidden(money("usage_cost", "Azure Usage")),
			hidden(pct("fee_pct", "Fee %")),
		),
		{
			Key: "payments", Label: "Payment", Plural: "Payments",
			Group: "ERP", TitleField: "reference", Icon: "credit-card",
			Columns: []string{"payment_date", "invoice_id", "schedule_id", "amount", "method", "reference"},
			Views: []View{
				{"This month", "fmin_payment_date=@month"},
				{"Last 30 days", "fmin_payment_date=@today-30"},
				{"Not linked to an installment", "f_schedule_id=@empty"},
			},
			Fields: []Field{
				ref("invoice_id", "Service Agreement", "invoices", "quote_description"),
				// Optional: the installment this payment settles. Unallocated
				// payments are applied to the earliest open installments.
				withHelp(filteredBy(ref("schedule_id", "Scheduled Payment", "payment_schedule", "label"), "invoice_id", "invoice_id"),
					"Leave the amount blank to pay the installment's outstanding amount (or the agreement balance)."),
				date("payment_date", "Payment Date"),
				sel("method", "Method", paymentMethod...),
				money("amount", "Amount"),
				txt("reference", "Reference"),
				long("notes", "Notes"),
			},
		},
		// ---------------- Azure ----------------
		{
			// An Entra (Azure AD) tenant whose subscriptions we bill fees on. The
			// app reads usage with a service principal; its client secret is
			// stored encrypted outside this table (see internal/azure).
			Key: "azure_tenants", Label: "Azure Tenant", Plural: "Azure Tenants",
			Group: "Azure", TitleField: "name", Icon: "cloud",
			Fields: []Field{
				req(txt("name", "Name")),
				withHelp(req(txt("directory_id", "Directory (Tenant) ID")), "Entra ID → Overview → Tenant ID."),
				withHelp(txt("client_id", "Client (App) ID"), "The app registration used to read Cost Management data."),
				withDefault(boolf("active", "Daily Sync"), "true"),
				ro(datetime("last_sync_at", "Last Sync")),
				ro(date("synced_through", "Usage Synced Through")),
				ro(sel("sync_status", "Sync Status", "OK", "Running", "Failed")),
				ro(hidden(long("sync_message", "Sync Message"))),
				long("notes", "Notes"),
			},
		},
		{
			// A subscription, discovered by the sync. Map it to the Account that
			// pays its fees and give it a type (fee %).
			Key: "azure_subscriptions", Label: "Azure Subscription", Plural: "Azure Subscriptions",
			Group: "Azure", TitleField: "display_name", Icon: "server",
			Columns: []string{"display_name", "subscription_id", "account_id", "subscription_type", "fee_pct", "state", "azure_tenant_id"},
			Views: []View{
				{"Unmapped", "f_account_id=@empty"},
				{"No type", "f_subscription_type=@empty"},
				{"TAN", "f_subscription_type=TAN"},
				{"TSS", "f_subscription_type=TSS"},
			},
			Fields: []Field{
				req(txt("display_name", "Name")),
				req(txt("subscription_id", "Subscription ID")),
				ref("azure_tenant_id", "Azure Tenant", "azure_tenants", "name"),
				ref("account_id", "Account", "accounts", "account_name"),
				sel("subscription_type", "Type", azureSubType...),
				withHelp(pct("fee_pct", "Fee % (override)"), "Blank = the type's rate under Azure Fee Rates (TAN 25%, TSS 27% by default)."),
				ro(txt("state", "State")),
				long("notes", "Notes"),
			},
		},
		{
			// Fee % per subscription type, effective from a date (the latest
			// rate on or before a usage month applies). Without rows the
			// built-in defaults apply: TAN 25%, TSS 27%.
			Key: "azure_fee_rates", Label: "Azure Fee Rate", Plural: "Azure Fee Rates",
			Group: "Azure", TitleField: "subscription_type", Icon: "percent",
			Fields: []Field{
				req(sel("subscription_type", "Type", azureSubType...)),
				req(pct("fee_pct", "Fee %")),
				withHelp(date("valid_from", "Valid From"), "Blank = since always."),
				long("notes", "Notes"),
			},
		},
		{
			// Daily Azure cost per subscription and service, as synced from
			// Cost Management (actual cost, billing currency). Read-only.
			Key: "azure_usage", Label: "Azure Usage", Plural: "Azure Usage",
			Group: "Azure", TitleField: "service_name", Icon: "activity", ReadOnly: true,
			Columns: []string{"usage_date", "azure_subscription_id", "service_name", "cost", "currency"},
			Views: []View{
				{"This month", "fmin_usage_date=@month"},
				{"Last 30 days", "fmin_usage_date=@today-30"},
			},
			Fields: []Field{
				ref("azure_subscription_id", "Subscription", "azure_subscriptions", "display_name"),
				date("usage_date", "Date"),
				txt("service_name", "Service"),
				money("cost", "Cost"),
				txt("currency", "Currency"),
			},
		},
		// ---------------- AP ----------------
		{
			Key: "suppliers", Label: "Supplier", Plural: "Suppliers",
			Group: "AP", TitleField: "supplier_name", Icon: "truck",
			Fields: []Field{
				req(txt("supplier_name", "Supplier Name")),
				txt("country", "Country"),
				txt("vat_id", "VAT ID"),
				email("email", "Email"),
				hidden(email("billing_email", "Billing / AR Email")),
				hidden(email("legal_email", "Legal Email")),
				txt("phone", "Phone"),
				long("billing_address", "Billing Address"),
				intf("payment_terms_days", "Payment Terms (days)"),
				txt("cc", "CC"),
				long("notes", "Notes"),
			},
		},
		{
			Key: "purchase_orders", Label: "Purchase Order", Plural: "Purchase Orders",
			Group: "AP", TitleField: "notes", Icon: "clipboard-list",
			Children: []Child{{Entity: "po_lines", FK: "po_id", Label: "Line Items"}},
			Fields: []Field{
				sellerRef("legal_entity_id", "Our Legal Entity"),
				ref("supplier_id", "Supplier", "suppliers", "supplier_name"),
				date("po_date", "PO Date"),
				sel("status", "Status", apDocStatus...),
				txt("currency", "Currency"),
				pct("vat_pct", "VAT %"),
				pct("markup_pct", "Markup %"),
				intf("payment_terms_days", "Payment Terms (days)"),
				ro(money("net_total", "Net Total")),
				ro(money("markup_total", "Markup Total")),
				ro(money("vat_total", "VAT Total")),
				ro(money("grand_total", "Grand Total")),
				date("requested_delivery", "Requested Delivery"),
				long("notes", "Notes"),
			},
		},
		lineEntity("po_lines", "PO Line", "AP", "po_id", "Purchase Order", "purchase_orders", "notes"),
		{
			Key: "vendor_bills", Label: "Vendor Bill", Plural: "Vendor Bills",
			Group: "AP", TitleField: "notes", Icon: "file-minus",
			Children: []Child{{Entity: "bill_lines", FK: "bill_id", Label: "Line Items"}},
			Fields: []Field{
				sellerRef("legal_entity_id", "Our Legal Entity"),
				ref("supplier_id", "Supplier", "suppliers", "supplier_name"),
				date("bill_date", "Bill Date"),
				date("due_date", "Due Date"),
				sel("status", "Status", apDocStatus...),
				ref("po_id", "Purchase Order", "purchase_orders", "notes"),
				txt("currency", "Currency"),
				pct("vat_pct", "VAT %"),
				pct("markup_pct", "Markup %"),
				intf("payment_terms_days", "Payment Terms (days)"),
				ro(money("net_total", "Net Total")),
				ro(money("markup_total", "Markup Total")),
				ro(money("vat_total", "VAT Total")),
				ro(money("grand_total", "Grand Total")),
				money("paid_total", "Paid Total"),
				ro(money("balance", "Balance")),
				intf("days_overdue", "Days Overdue"),
				long("notes", "Notes"),
			},
		},
		lineEntity("bill_lines", "Bill Line", "AP", "bill_id", "Vendor Bill", "vendor_bills", "notes"),
		{
			Key: "ap_payments", Label: "AP Payment", Plural: "AP Payments",
			Group: "AP", TitleField: "reference", Icon: "credit-card",
			Fields: []Field{
				ref("bill_id", "Vendor Bill", "vendor_bills", "notes"),
				date("payment_date", "Payment Date"),
				sel("method", "Method", paymentMethod...),
				money("amount", "Amount"),
				txt("reference", "Reference"),
				long("notes", "Notes"),
			},
		},
	}
}

// lineEntity builds a standard document line item entity (used by quotes,
// orders, invoices, POs, bills). Lines are managed through their parent
// document, so the entity is hidden from the main navigation. SKU is a lookup
// into the tenant's products; line_no and line_net are computed.
func lineEntity(key, label, group, parentKey, parentLabel, parentEntity, parentTitle string) Entity {
	return Entity{
		Key: key, Label: label, Plural: label + "s", Group: group,
		TitleField: "description", Icon: "list", Hidden: true,
		Fields: []Field{
			ref(parentKey, parentLabel, parentEntity, parentTitle),
			ro(intf("line_no", "Line #")),
			lookup("sku", "SKU / Service", "products", "sku", "name"),
			req(txt("description", "Description")),
			num("qty", "Qty"),
			money("unit_price", "Unit Price"),
			ro(money("line_net", "Line Net")),
		},
	}
}

// partyFields is the block shared by every sales document (quote, sales order,
// service agreement) so the three stay coherent: the customer contact, our
// selling legal entity, and read-only snapshots of both parties taken on save
// (see DocService.deriveFields). The seller defaults from the account's
// billing legal entity.
func partyFields() []Field {
	return []Field{
		hidden(contactRef()),
		withHelp(sellerRef("legal_entity_id", "Our Legal Entity (seller)"),
			"Defaults to the account's billing legal entity."),
		ro(hidden(txt("legal_entity_name", "Seller Name"))),
		ro(hidden(long("legal_entity_billing_address", "Seller Billing Address"))),
		ro(hidden(txt("buyer_name", "Buyer Name"))),
		ro(hidden(long("buyer_address", "Buyer Address"))),
		ro(hidden(txt("buyer_vat_id", "Buyer VAT ID"))),
		ro(hidden(email("buyer_billing_email", "Buyer Billing Email"))),
	}
}

// pricingFields is the money block shared by sales documents. withPayments adds
// the receivable fields (paid / balance / overdue) used by service agreements.
func pricingFields(withPayments bool) []Field {
	f := []Field{
		// Derived: the account's currency, else the seller's default currency
		// (an agreement takes its quote's).
		ro(txt("currency", "Currency")),
		pct("vat_pct", "VAT %"),
		pct("markup_pct", "Markup %"),
		intf("payment_terms_days", "Payment Terms (days)"),
		ro(money("net_total", "Net Total")),
		ro(money("markup_total", "Markup Total")),
		ro(money("vat_total", "VAT Total")),
		ro(money("grand_total", "Grand Total")),
	}
	if withPayments {
		f = append(f,
			// Sum of registered payments (kept as entered on agreements that
			// have no payments yet).
			ro(money("paid_total", "Paid Total")),
			ro(money("balance", "Balance")),
		)
	}
	return f
}

// sellerRef picks one of our own legal entities (entity_type Own).
func sellerRef(key, label string) Field {
	return optionWhere(ref(key, label, "legal_entities", "legal_name"), "entity_type", "Own")
}

// agreementLive lists the agreement statuses that count as contracted.
const agreementLive = "Signed,Active,Completed,Issued,Paid"

// contactRef picks one of the chosen account's contacts.
func contactRef() Field {
	return filteredBy(ref("contact_id", "Contact", "contacts", "last_name"), "account_id", "account_id")
}

// withFields appends fields to an entity (e.g. extra columns on a line entity).
func withFields(e Entity, fields ...Field) Entity {
	e.Fields = append(append([]Field{}, e.Fields...), fields...)
	return e
}

func concat(parts ...[]Field) []Field {
	var out []Field
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
