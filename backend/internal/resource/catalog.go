package resource

// Picklists carried over from the workbook's 01_Setup sheet.
var (
	leadStatus    = []string{"New", "Contacted", "Qualified", "Unqualified"}
	oppStage      = []string{"Prospecting", "Discovery", "Proposal", "Negotiation", "Won", "Lost"}
	activityType  = []string{"Call", "Email", "Meeting", "Task"}
	activityStat  = []string{"Open", "Completed", "Cancelled"}
	quoteStatus   = []string{"Draft", "Sent", "Accepted", "Rejected", "Expired"}
	orderStatus   = []string{"Open", "In Fulfillment", "Delivered", "Cancelled"}
	invoiceStatus = []string{"Draft", "Issued", "Paid", "Overdue", "Cancelled"}
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
)

// Catalog returns all business entities. This is the single source of truth that
// drives the database schema, the API, and the frontend UI.
func Catalog() []Entity {
	return []Entity{
		// ---------------- Master data ----------------
		{
			Key: "legal_entities", Label: "Legal Entity", Plural: "Legal Entities",
			Group: "Master", TitleField: "legal_name", Icon: "building",
			Fields: []Field{
				req(txt("legal_name", "Legal Name")),
				txt("country", "Country"),
				txt("vat_id", "VAT ID"),
				txt("registration_no", "Registration #"),
				long("billing_address", "Billing Address"),
				txt("bank_iban", "Bank / IBAN"),
				txt("swift", "SWIFT"),
				txt("default_currency", "Default Currency"),
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
				txt("country", "Country"),
				ref("legal_employer_id", "Legal Employer", "legal_entities", "legal_name"),
				ref("contracting_company_id", "Contracting Company", "legal_entities", "legal_name"),
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
				txt("industry", "Industry"),
				txt("vat_id", "VAT ID"),
				ref("legal_entity_id", "Bill From (Legal Entity)", "legal_entities", "legal_name"),
				txt("legal_entity_name", "Buying Entity Name"),
				long("legal_entity_address", "Buying Entity Address"),
				txt("country", "Country"),
				txt("city", "City"),
				txt("website", "Website"),
				txt("phone", "Phone"),
				email("email", "Email"),
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
				email("email", "Email"),
				txt("phone", "Phone"),
				txt("city", "City"),
				txt("country", "Country"),
				txt("owner", "Owner"),
				date("created_date", "Created Date"),
				long("notes", "Notes"),
			},
		},
		{
			Key: "leads", Label: "Lead", Plural: "Leads",
			Group: "CRM", TitleField: "company", Icon: "target",
			Fields: []Field{
				req(txt("company", "Company")),
				txt("contact_name", "Contact Name"),
				email("email", "Email"),
				txt("phone", "Phone"),
				sel("source", "Source", leadSource...),
				txt("country", "Country"),
				sel("status", "Status", leadStatus...),
				txt("owner", "Owner"),
				date("created_date", "Created Date"),
				date("next_action_date", "Next Action Date"),
				long("notes", "Notes"),
			},
		},
		{
			Key: "opportunities", Label: "Opportunity", Plural: "Opportunities",
			Group: "CRM", TitleField: "opportunity_name", Icon: "trending-up",
			Fields: []Field{
				ref("account_id", "Account", "accounts", "account_name"),
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
			Children: []Child{{Entity: "quote_lines", FK: "quote_id", Label: "Line Items"}},
			Fields: []Field{
				ref("legal_entity_id", "Legal Entity", "legal_entities", "legal_name"),
				ref("account_id", "Account", "accounts", "account_name"),
				date("quote_date", "Quote Date"),
				req(txt("description", "Description")),
				date("valid_until", "Valid Until"),
				sel("status", "Status", quoteStatus...),
				txt("currency", "Currency"),
				pct("vat_pct", "VAT %"),
				pct("markup_pct", "Markup %"),
				intf("payment_terms_days", "Payment Terms (days)"),
				ro(money("net_total", "Net Total")),
				ro(money("markup_total", "Markup Total")),
				ro(money("vat_total", "VAT Total")),
				ro(money("grand_total", "Grand Total")),
				ref("opportunity_id", "Opportunity", "opportunities", "opportunity_name"),
				long("notes", "Notes"),
			},
		},
		lineEntity("quote_lines", "Quote Line", "ERP", "quote_id", "Quote", "quotes", "description"),
		{
			Key: "sales_orders", Label: "Sales Order", Plural: "Sales Orders",
			Group: "ERP", TitleField: "quote_description", Icon: "shopping-cart",
			Children: []Child{{Entity: "order_lines", FK: "sales_order_id", Label: "Line Items"}},
			Fields: []Field{
				ref("legal_entity_id", "Legal Entity", "legal_entities", "legal_name"),
				ref("account_id", "Account", "accounts", "account_name"),
				date("order_date", "Order Date"),
				sel("status", "Status", orderStatus...),
				ref("quote_id", "Quote", "quotes", "description"),
				req(txt("quote_description", "Description")),
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
		lineEntity("order_lines", "Order Line", "ERP", "sales_order_id", "Sales Order", "sales_orders", "quote_description"),
		{
			Key: "invoices", Label: "Invoice", Plural: "Invoices",
			Group: "ERP", TitleField: "quote_description", Icon: "receipt",
			Children: []Child{{Entity: "invoice_lines", FK: "invoice_id", Label: "Line Items"}},
			Fields: []Field{
				ref("account_id", "Account", "accounts", "account_name"),
				ref("legal_entity_id", "Legal Entity", "legal_entities", "legal_name"),
				ro(txt("legal_entity_name", "Legal Entity Name")),
				ro(long("legal_entity_billing_address", "Legal Entity Billing Address")),
				date("invoice_date", "Invoice Date"),
				date("due_date", "Due Date"),
				sel("status", "Status", invoiceStatus...),
				ref("sales_order_id", "Sales Order", "sales_orders", "quote_description"),
				req(txt("quote_description", "Description")),
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
				date("service_start_date", "Service Start Date"),
				date("service_end_date", "Service End Date"),
				long("notes", "Notes"),
			},
		},
		lineEntity("invoice_lines", "Invoice Line", "ERP", "invoice_id", "Invoice", "invoices", "quote_description"),
		{
			Key: "payments", Label: "Payment", Plural: "Payments",
			Group: "ERP", TitleField: "reference", Icon: "credit-card",
			Fields: []Field{
				ref("invoice_id", "Invoice", "invoices", "quote_description"),
				date("payment_date", "Payment Date"),
				sel("method", "Method", paymentMethod...),
				money("amount", "Amount"),
				txt("reference", "Reference"),
				long("notes", "Notes"),
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
				ref("legal_entity_id", "Legal Entity", "legal_entities", "legal_name"),
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
				ref("legal_entity_id", "Legal Entity", "legal_entities", "legal_name"),
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
		// ---------------- SAR tracking ----------------
		{
			Key: "sar_tracking", Label: "SAR Report", Plural: "SAR Tracking",
			Group: "SAR", TitleField: "period", Icon: "check-square",
			Fields: []Field{
				// Only contractors are tracked here (internals are excluded).
				optionWhere(ref("person_id", "Person", "people", "full_name"), "worker_type", "Contractor"),
				// Auto-filled from the chosen person's contracting company (a legal
				// entity); read-only in the UI.
				ro(ref("contracting_company_id", "Contracting Company", "legal_entities", "legal_name")),
				txt("period", "Period"),
				num("days_reported", "Days Reported"),
				// Auto-filled from the chosen person's hourly rate; read-only in the UI.
				ro(money("rate", "Rate")),
				// Auto-filled = days_reported * 8 * rate; read-only in the UI.
				ro(money("amount", "Amount")),
				boolf("sar_received", "SAR Received"),
				boolf("internal_validation", "Internal Validation"),
				boolf("approved", "Approved"),
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
