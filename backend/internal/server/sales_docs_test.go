package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// salesFixture creates our seller legal entity, a customer legal entity, an
// account whose buying entity is that customer (so the buyer details mirror
// it), and one contact on the account.
func salesFixture(t *testing.T, token string) (leID, accID, contactID string) {
	t.Helper()
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Seller SA", "entity_type": "Own", "billing_address": "Rua A 1, Lisboa", "default_currency": "EUR",
		"signatory_first_name": "Ana", "signatory_last_name": "Silva", "signatory_title": "CEO",
	})
	leID = dataObj(t, le)["id"].(string)

	buyer := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Globex Holding BV", "billing_address": "Dam 1, Amsterdam", "vat_id": "NL123",
		"country": "NL", "default_currency": "USD", "billing_email": "ap@globex.test", "legal_email": "legal@globex.test",
	})
	if dataObj(t, buyer)["entity_type"] != "Customer" {
		t.Fatalf("new legal entities should default to Customer: %v", dataObj(t, buyer)["entity_type"])
	}
	buyerID := dataObj(t, buyer)["id"].(string)

	acc := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Globex", "legal_entity_id": leID, "buying_entity_id": buyerID,
		"legal_entity_name": "typed name is ignored while an entity is linked",
		"lines":             []map[string]any{{"first_name": "Jo", "last_name": "Buyer", "email": "jo@globex.test", "contact_role": "Buying"}},
	})
	if acc.Status != http.StatusCreated {
		t.Fatalf("account create: %d %s", acc.Status, acc.Raw)
	}
	a := dataObj(t, acc)
	accID = a["id"].(string)
	for k, want := range map[string]any{
		"legal_entity_name": "Globex Holding BV", "legal_entity_address": "Dam 1, Amsterdam", "vat_id": "NL123",
		"country": "NL", "currency": "USD", "billing_email": "ap@globex.test", "legal_email": "legal@globex.test",
	} {
		if a[k] != want {
			t.Fatalf("account %s = %v, want %v (mirrored from buying entity)", k, a[k], want)
		}
	}

	contacts := do(t, "GET", "/api/v1/entities/contacts?f_account_id="+accID, token, nil)
	contactID = contacts.Body["data"].([]any)[0].(map[string]any)["id"].(string)
	return leID, accID, contactID
}

// Re-saving an account keeps contact ids (quotes reference them), and an
// account without a buying entity keeps its typed buyer details.
func TestAccountContactsKeepIDsAndTypedBuyer(t *testing.T) {
	token, _ := signup(t, "Acct Co")
	_, accID, contactID := salesFixture(t, token)
	g := do(t, "GET", "/api/v1/docs/accounts/"+accID, token, nil)
	body := dataObj(t, g)
	body["lines"] = g.Body["lines"]
	do(t, "PUT", "/api/v1/docs/accounts/"+accID, token, body)
	c := do(t, "GET", "/api/v1/entities/contacts/"+contactID, token, nil)
	if c.Status != http.StatusOK {
		t.Fatalf("contact id changed on account save: %d", c.Status)
	}

	typed := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Typed", "legal_entity_name": "Typed Buyer Ltd", "country": "PT",
	})
	if dataObj(t, typed)["legal_entity_name"] != "Typed Buyer Ltd" || dataObj(t, typed)["country"] != "PT" {
		t.Fatalf("typed buyer details lost: %v", dataObj(t, typed))
	}
}

// Quote → Service Agreement: the seller defaults from the account, currency
// from the account, links are kept and navigable both ways.
func TestSalesDocumentPartiesAndChain(t *testing.T) {
	token, _ := signup(t, "Chain Co")
	leID, accID, contactID := salesFixture(t, token)

	opp := do(t, "POST", "/api/v1/entities/opportunities", token, map[string]any{
		"account_id": accID, "opportunity_name": "Globex deal", "probability_pct": 50,
	})
	oppID := dataObj(t, opp)["id"].(string)

	q := do(t, "POST", "/api/v1/docs/quotes", token, map[string]any{
		"account_id": accID, "contact_id": contactID, "opportunity_id": oppID, "description": "Consulting",
		"currency":           "GBP", // read-only: derived from the account
		"service_start_date": "2026-01-01", "service_end_date": "2026-12-31",
		"lines": []map[string]any{{"description": "Dev", "qty": 10, "unit_price": 100}},
	})
	if q.Status != http.StatusCreated {
		t.Fatalf("quote: %d %s", q.Status, q.Raw)
	}
	quote := dataObj(t, q)
	for k, want := range map[string]any{
		"legal_entity_id": leID, "legal_entity_name": "Seller SA", "currency": "USD",
		"buyer_name": "Globex Holding BV", "buyer_vat_id": "NL123", "buyer_billing_email": "ap@globex.test",
	} {
		if quote[k] != want {
			t.Fatalf("quote %s = %v, want %v", k, quote[k], want)
		}
	}
	quoteID := quote["id"].(string)

	sa := do(t, "POST", "/api/v1/docs/quotes/"+quoteID+"/convert", token, nil)
	agreement := dataObj(t, sa)
	if sa.Body["entity"] != "invoices" || agreement["quote_id"] != quoteID || agreement["opportunity_id"] != oppID ||
		agreement["contact_id"] != contactID || agreement["currency"] != "USD" {
		t.Fatalf("agreement links not carried: %v", agreement)
	}
	agreementID := agreement["id"].(string)

	// quote_id / opportunity_id are read-only: an update can't repoint them.
	agreement["quote_id"], agreement["opportunity_id"] = nil, nil
	agreement["lines"] = []map[string]any{{"description": "Dev", "qty": 10, "unit_price": 100}}
	up := dataObj(t, do(t, "PUT", "/api/v1/docs/invoices/"+agreementID, token, agreement))
	if up["quote_id"] != quoteID || up["opportunity_id"] != oppID {
		t.Fatalf("read-only links changed by update: %v", up)
	}

	rel := do(t, "GET", "/api/v1/entities/invoices/"+agreementID+"/related", token, nil)
	upFields := map[string]bool{}
	for _, l := range rel.Body["upstream"].([]any) {
		upFields[l.(map[string]any)["field"].(string)] = true
	}
	for _, f := range []string{"quote_id", "opportunity_id", "account_id", "legal_entity_id"} {
		if !upFields[f] {
			t.Fatalf("agreement upstream missing %s: %s", f, rel.Raw)
		}
	}
	down := do(t, "GET", "/api/v1/entities/quotes/"+quoteID+"/related", token, nil)
	found := false
	for _, g := range down.Body["downstream"].([]any) {
		gm := g.(map[string]any)
		if gm["entity"] == "invoices" && gm["rows"].([]any)[0].(map[string]any)["status"] == "Draft" {
			found = true
		}
	}
	if !found {
		t.Fatalf("quote downstream should list the agreement with its status: %s", down.Raw)
	}
}

func agreement(t *testing.T, token, accID string, total float64, schedule []map[string]any) string {
	t.Helper()
	body := map[string]any{
		"account_id": accID, "quote_description": "Retainer", "vat_pct": 0,
		"lines": []map[string]any{{"description": "Fee", "qty": 1, "unit_price": total}},
	}
	if schedule != nil {
		body["payment_schedule"] = schedule
	}
	r := do(t, "POST", "/api/v1/docs/invoices", token, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("agreement create: %d %s", r.Status, r.Raw)
	}
	return dataObj(t, r)["id"].(string)
}

func getAgreement(t *testing.T, token, id string) (map[string]any, []map[string]any) {
	t.Helper()
	g := do(t, "GET", "/api/v1/docs/invoices/"+id, token, nil)
	var rows []map[string]any
	if ch, ok := g.Body["children"].(map[string]any); ok {
		for _, r := range ch["payment_schedule"].([]any) {
			rows = append(rows, r.(map[string]any))
		}
	}
	return dataObj(t, g), rows
}

// The payment schedule splits blank amounts evenly, must add up to the
// agreement total, and drives the payment position as payments are registered.
func TestPaymentScheduleAndPayments(t *testing.T) {
	token, _ := signup(t, "Pay Co")
	_, accID, _ := salesFixture(t, token)

	// Explicit amounts that don't add up are rejected.
	bad := do(t, "POST", "/api/v1/docs/invoices", token, map[string]any{
		"account_id": accID, "quote_description": "Bad", "vat_pct": 0,
		"lines":            []map[string]any{{"description": "Fee", "qty": 1, "unit_price": 1000}},
		"payment_schedule": []map[string]any{{"due_date": "2026-01-31", "amount": 100}},
	})
	if bad.Status != http.StatusBadRequest || bad.errCode() != "schedule_mismatch" {
		t.Fatalf("mismatched schedule should be 400 schedule_mismatch, got %d %s", bad.Status, bad.Raw)
	}

	id := agreement(t, token, accID, 1000, []map[string]any{
		{"due_date": "2020-01-31", "description": "M1"},
		{"due_date": "2020-02-28", "description": "M2"},
		{"due_date": "2099-03-31", "description": "M3"},
	})
	sa, rows := getAgreement(t, token, id)
	if len(rows) != 3 || numField(t, rows[0], "amount") != 333.33 || numField(t, rows[2], "amount") != 333.34 {
		t.Fatalf("even split wrong: %v", rows)
	}
	// Installments are picked by their label (date · amount · description).
	if rows[0]["label"] != "31-01-2020 · 333.33 · M1" {
		t.Fatalf("installment label not stored: %v", rows[0]["label"])
	}
	if sa["payment_status"] != "Not Paid" || !strings.HasPrefix(sa["next_due_date"].(string), "2020-01-31") {
		t.Fatalf("initial position wrong: %v %v", sa["payment_status"], sa["next_due_date"])
	}

	// Pay installment 1 with a blank amount → its outstanding amount.
	p1 := do(t, "POST", "/api/v1/entities/payments", token, map[string]any{
		"schedule_id": rows[0]["id"], "payment_date": "2020-02-01", "reference": "P1",
	})
	if p1.Status != http.StatusCreated || numField(t, dataObj(t, p1), "amount") != 333.33 || dataObj(t, p1)["invoice_id"] != id {
		t.Fatalf("installment payment not defaulted: %d %s", p1.Status, p1.Raw)
	}
	sa, rows = getAgreement(t, token, id)
	if rows[0]["status"] != "Paid" || sa["payment_status"] != "Partially Paid" || numField(t, sa, "balance") != 666.67 ||
		!strings.HasPrefix(sa["next_due_date"].(string), "2020-02-28") {
		t.Fatalf("after P1: %v / %v", sa, rows[0])
	}

	// Overdue view: next due date in the past and not fully paid.
	ov := do(t, "GET", "/api/v1/entities/invoices?fin_payment_status=Not%20Paid,Partially%20Paid&fmax_next_due_date=@today-1", token, nil)
	if numField(t, ov.Body, "total") != 1 {
		t.Fatalf("overdue filter should match the agreement: %s", ov.Raw)
	}

	// Re-saving the agreement (ids sent back) keeps the payment linked.
	body := sa
	body["lines"] = []map[string]any{{"description": "Fee", "qty": 1, "unit_price": 1000}}
	body["payment_schedule"] = rows
	if r := do(t, "PUT", "/api/v1/docs/invoices/"+id, token, body); r.Status != http.StatusOK {
		t.Fatalf("resave: %d %s", r.Status, r.Raw)
	}
	_, rows = getAgreement(t, token, id)
	if rows[0]["status"] != "Paid" {
		t.Fatalf("installment lost its payment after re-save: %v", rows[0])
	}

	// An unallocated payment without an amount settles the balance.
	p2 := do(t, "POST", "/api/v1/entities/payments", token, map[string]any{"invoice_id": id, "payment_date": "2020-03-01"})
	if numField(t, dataObj(t, p2), "amount") != 666.67 {
		t.Fatalf("unallocated payment should default to the balance: %s", p2.Raw)
	}
	sa, rows = getAgreement(t, token, id)
	if sa["payment_status"] != "Paid" || numField(t, sa, "balance") != 0 || sa["next_due_date"] != nil || numField(t, sa, "installments_paid") != 3 {
		t.Fatalf("after P2 should be fully paid: %v", sa)
	}

	// Deleting a payment reopens the schedule.
	do(t, "DELETE", "/api/v1/entities/payments/"+dataObj(t, p2)["id"].(string), token, nil)
	sa, _ = getAgreement(t, token, id)
	if sa["payment_status"] != "Partially Paid" || numField(t, sa, "paid_total") != 333.33 {
		t.Fatalf("after deleting P2: %v", sa)
	}

	// An installment from another agreement is rejected.
	other := agreement(t, token, accID, 50, []map[string]any{{"due_date": "2026-01-01"}})
	if r := do(t, "POST", "/api/v1/entities/payments", token, map[string]any{"invoice_id": other, "schedule_id": rows[1]["id"]}); r.Status != http.StatusBadRequest {
		t.Fatalf("cross-agreement installment should be 400, got %d", r.Status)
	}

	// Saving without the schedule key keeps it; an explicit empty array clears it.
	delete(body, "payment_schedule")
	do(t, "PUT", "/api/v1/docs/invoices/"+id, token, body)
	if _, rows := getAgreement(t, token, id); len(rows) != 3 {
		t.Fatalf("schedule lost on save without key: %d", len(rows))
	}
	body["payment_schedule"] = []any{}
	do(t, "PUT", "/api/v1/docs/invoices/"+id, token, body)
	if sa, rows := getAgreement(t, token, id); len(rows) != 0 || sa["payment_status"] != "Unscheduled" {
		t.Fatalf("empty schedule should clear and mark Unscheduled: %v %d", sa["payment_status"], len(rows))
	}
}

// People: location fills country/city/time zone; internal workers have no
// contracting company.
func TestPeopleLocationAndWorkerTypeRules(t *testing.T) {
	token, _ := signup(t, "People Rules Co")
	loc := do(t, "POST", "/api/v1/entities/locations", token, map[string]any{
		"location_name": "Lisbon HQ", "country": "Portugal", "city": "Lisbon", "time_zone": "Europe/Lisbon",
	})
	locID := dataObj(t, loc)["id"].(string)
	co := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{"legal_name": "Contractor Co", "entity_type": "Supplier"})
	coID := dataObj(t, co)["id"].(string)

	p := do(t, "POST", "/api/v1/entities/people", token, map[string]any{
		"full_name": "Rita", "worker_type": "Contractor", "location_id": locID, "country": "Spain", "contracting_company_id": coID,
	})
	person := dataObj(t, p)
	if person["country"] != "Portugal" || person["city"] != "Lisbon" || person["time_zone"] != "Europe/Lisbon" || person["contracting_company_id"] != coID {
		t.Fatalf("location fill / contractor wrong: %v", person)
	}
	up := do(t, "PUT", "/api/v1/entities/people/"+person["id"].(string), token, map[string]any{"worker_type": "Internal"})
	if dataObj(t, up)["contracting_company_id"] != nil {
		t.Fatalf("internal person should have no contracting company: %v", dataObj(t, up)["contracting_company_id"])
	}
}

// Accounts can be created from a legal entity; leads pick their company from
// legal entities and convert onto the account that uses it.
func TestLegalEntityToAccountAndLead(t *testing.T) {
	token, _ := signup(t, "Entity Co")
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Initech GmbH", "country": "DE", "default_currency": "EUR", "email": "info@initech.test",
	})
	leID := dataObj(t, le)["id"].(string)

	lead := do(t, "POST", "/api/v1/entities/leads", token, map[string]any{
		"legal_entity_id": leID, "contact_last_name": "Lumbergh",
	})
	if lead.Status != http.StatusCreated || dataObj(t, lead)["company"] != "Initech GmbH" || dataObj(t, lead)["country"] != "DE" {
		t.Fatalf("lead company not filled from entity: %d %s", lead.Status, lead.Raw)
	}

	conv := do(t, "POST", "/api/v1/docs/legal_entities/"+leID+"/convert", token, nil)
	if conv.Status != http.StatusCreated || conv.Body["entity"] != "accounts" {
		t.Fatalf("entity → account: %d %s", conv.Status, conv.Raw)
	}
	acc := dataObj(t, conv)
	if acc["account_name"] != "Initech GmbH" || acc["buying_entity_id"] != leID || acc["currency"] != "EUR" || acc["email"] != "info@initech.test" {
		t.Fatalf("account not built from entity: %v", acc)
	}
	if again := do(t, "POST", "/api/v1/docs/legal_entities/"+leID+"/convert", token, nil); again.Status != http.StatusConflict {
		t.Fatalf("second entity → account should be 409, got %d", again.Status)
	}

	// The lead converts onto that account (matched by entity, not name).
	lc := do(t, "POST", "/api/v1/docs/leads/"+dataObj(t, lead)["id"].(string)+"/convert", token, nil)
	if dataObj(t, lc)["account_id"] != acc["id"] {
		t.Fatalf("lead should convert onto the entity's account: %s", lc.Raw)
	}
}

// Saved views are part of the metadata and their filters work.
func TestViewsAndFilters(t *testing.T) {
	token, _ := signup(t, "Views Co")
	_, accID, _ := salesFixture(t, token)
	agreement(t, token, accID, 100, nil)
	agreement(t, token, accID, 200, []map[string]any{{"due_date": "2099-01-01"}})

	meta := do(t, "GET", "/api/v1/meta", token, nil)
	var views []any
	for _, e := range meta.Body["entities"].([]any) {
		if m := e.(map[string]any); m["key"] == "invoices" {
			views, _ = m["views"].([]any)
		}
	}
	if len(views) == 0 {
		t.Fatalf("service agreements should expose saved views")
	}

	count := func(q string) float64 {
		r := do(t, "GET", "/api/v1/entities/invoices?"+q, token, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("filter %s: %d %s", q, r.Status, r.Raw)
		}
		return numField(t, r.Body, "total")
	}
	if n := count("f_payment_status=Unscheduled"); n != 1 {
		t.Fatalf("unscheduled = %v", n)
	}
	if n := count("fmin_grand_total=150"); n != 1 {
		t.Fatalf("min total = %v", n)
	}
	if n := count("fmin_next_due_date=@today&fmax_next_due_date=@today+36500"); n != 1 {
		t.Fatalf("due range = %v", n)
	}
	if n := count("f_next_due_date=@empty"); n != 1 {
		t.Fatalf("empty next due = %v", n)
	}
	if n := count("fmin_grand_total=notanumber"); n != 2 {
		t.Fatalf("bad bound should be ignored, got %v", n)
	}
}

// The sales report aggregates agreements, installments, quotes and payments.
func TestSalesReport(t *testing.T) {
	token, _ := signup(t, "Report Co")
	_, accID, _ := salesFixture(t, token)
	id := agreement(t, token, accID, 300, []map[string]any{{"due_date": "2020-01-01"}, {"due_date": time.Now().AddDate(0, 0, 10).Format("2006-01-02")}})
	do(t, "POST", "/api/v1/entities/payments", token, map[string]any{"invoice_id": id, "amount": 50, "payment_date": time.Now().Format("2006-01-02"), "method": "Card"})

	r := do(t, "GET", "/api/v1/reports/sales", token, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("report: %d %s", r.Status, r.Raw)
	}
	overdue := r.Body["overdue_installments"].([]any)
	upcoming := r.Body["upcoming_installments"].([]any)
	if len(overdue) != 1 || numField(t, overdue[0].(map[string]any), "outstanding") != 100 || len(upcoming) != 1 {
		t.Fatalf("installments wrong: overdue=%v upcoming=%v", overdue, upcoming)
	}
	pay := r.Body["payments"].(map[string]any)
	if numField(t, pay["unallocated"].(map[string]any), "amount") != 50 {
		t.Fatalf("unallocated payments wrong: %v", pay)
	}
	for _, k := range []string{"agreements", "quotes"} {
		if r.Body[k] == nil {
			t.Fatalf("report missing %s", k)
		}
	}
}

// Duplicating copies header, lines and schedule into a fresh draft.
func TestDuplicateDocuments(t *testing.T) {
	token, _ := signup(t, "Dup Co")
	_, accID, _ := salesFixture(t, token)

	q := do(t, "POST", "/api/v1/docs/quotes", token, map[string]any{
		"account_id": accID, "description": "Offer A", "status": "Accepted",
		"lines": []map[string]any{{"description": "Dev", "qty": 2, "unit_price": 50}},
	})
	qid := dataObj(t, q)["id"].(string)
	d := do(t, "POST", "/api/v1/docs/quotes/"+qid+"/duplicate", token, nil)
	cp := dataObj(t, d)
	if d.Status != http.StatusCreated || cp["id"] == qid || cp["description"] != "Offer A (copy)" || cp["status"] != "Draft" || numField(t, cp, "net_total") != 100 {
		t.Fatalf("bad duplicate: %d %v", d.Status, cp)
	}
	// The original keeps its lines (copies get new ids).
	if l := do(t, "GET", "/api/v1/docs/quotes/"+qid, token, nil).Body["lines"].([]any); len(l) != 1 {
		t.Fatalf("original lost its lines")
	}

	id := agreement(t, token, accID, 100, []map[string]any{{"due_date": "2026-01-01", "description": "Once"}})
	do(t, "POST", "/api/v1/entities/payments", token, map[string]any{"invoice_id": id, "amount": 100})
	d2 := dataObj(t, do(t, "POST", "/api/v1/docs/invoices/"+id+"/duplicate", token, nil))
	sa, rows := getAgreement(t, token, d2["id"].(string))
	if sa["status"] != "Draft" || numField(t, sa, "paid_total") != 0 || len(rows) != 1 || rows[0]["status"] != "Pending" {
		t.Fatalf("agreement copy should start unpaid: %v %v", sa, rows)
	}

	if r := do(t, "POST", "/api/v1/docs/accounts/"+accID+"/duplicate", token, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("account duplicate should be 400, got %d", r.Status)
	}
}

// Offers generate from a quote; any field of the reachable records is a
// {{prefix.field}} placeholder, and {{schedule.*}} rows repeat on agreements.
func TestGenerateFromQuoteAndNamespacedFields(t *testing.T) {
	token, _ := signup(t, "Tmpl Co")
	_, accID, contactID := salesFixture(t, token)

	q := do(t, "POST", "/api/v1/docs/quotes", token, map[string]any{
		"account_id": accID, "contact_id": contactID, "description": "Cloud migration",
		"lines": []map[string]any{{"description": "Architect", "qty": 3, "unit_price": 200}},
	})
	qid := dataObj(t, q)["id"].(string)

	offer := makeDocx(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
		`<w:p><w:r><w:t>{{quote.description}}|{{account.legal_email}}|{{contact.email}}|{{legal_entity.signatory_title}}|{{Legal_Entity_First_Name}}|{{quote.account_id}}|{{Quote_Description}}</w:t></w:r></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>{{line.description}}={{line.line_net}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`</w:body></w:document>`)
	tpl := uploadTemplate(t, token, "offer", "Namespaced", offer)
	gen := do(t, "GET", "/api/v1/documents/offer/quotes/"+qid+"?format=docx&template_id="+tpl, token, nil)
	if gen.Status != http.StatusOK {
		t.Fatalf("generate from quote: %d %s", gen.Status, gen.Raw)
	}
	xml := documentXML(t, gen.Raw)
	if !strings.Contains(xml, "Cloud migration|legal@globex.test|jo@globex.test|CEO|Ana|Globex|Cloud migration") || !strings.Contains(xml, "Architect=600.00") {
		t.Fatalf("namespaced fields not filled: %s", xml)
	}

	said := agreement(t, token, accID, 1000, []map[string]any{
		{"due_date": "2026-03-31", "description": "Q1", "amount": 250},
		{"due_date": "2026-06-30", "description": "Q2"},
	})
	sched := makeDocx(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>{{schedule.description}}@{{schedule.due_date}}:{{schedule.amount}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`</w:body></w:document>`)
	tpl2 := uploadTemplate(t, token, "igtr", "Schedule", sched)
	xml2 := documentXML(t, do(t, "GET", "/api/v1/documents/igtr/invoices/"+said+"?format=docx&template_id="+tpl2, token, nil).Raw)
	for _, want := range []string{"Q1@31-03-2026:250.00", "Q2@30-06-2026:750.00"} {
		if !strings.Contains(xml2, want) {
			t.Fatalf("schedule row %q missing: %s", want, xml2)
		}
	}

	if r := do(t, "GET", "/api/v1/documents/offer/accounts/"+accID+"?format=docx", token, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("generate from account should be 400, got %d", r.Status)
	}
	cat := do(t, "GET", "/api/v1/templates/fields", token, nil)
	if !strings.Contains(string(cat.Raw), "{{account.billing_email}}") || !strings.Contains(string(cat.Raw), "{{schedule.due_date}}") ||
		strings.Contains(string(cat.Raw), "sales_order.") {
		t.Fatalf("field catalog wrong (namespaced/schedule present, no sales orders)")
	}
}
