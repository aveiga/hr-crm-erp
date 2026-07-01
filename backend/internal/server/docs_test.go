package server_test

import (
	"net/http"
	"testing"
)

// helper: numeric field from a data object
func numField(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("field %q not a number: %v", key, m[key])
	}
	return v
}

func TestDocumentSaveComputesTotals(t *testing.T) {
	token, _ := signup(t, "Docs Totals Co")

	body := map[string]any{
		"description": "Q1 Consulting",
		"status":      "Draft",
		"currency":    "EUR",
		"vat_pct":     23,
		"markup_pct":  10,
		"lines": []map[string]any{
			{"description": "Senior Engineer", "qty": 10, "unit_price": 80},
			{"description": "PM", "qty": 4, "unit_price": 50},
		},
	}
	created := do(t, "POST", "/api/v1/docs/quotes", token, body)
	if created.Status != http.StatusCreated {
		t.Fatalf("doc create failed: %d %s", created.Status, created.Raw)
	}
	d := dataObj(t, created)
	id := d["id"].(string)

	if got := numField(t, d, "net_total"); got != 1000 {
		t.Fatalf("net_total: want 1000, got %v", got)
	}
	if got := numField(t, d, "markup_total"); got != 100 {
		t.Fatalf("markup_total: want 100, got %v", got)
	}
	if got := numField(t, d, "vat_total"); got != 253 {
		t.Fatalf("vat_total: want 253 (23%% of 1100), got %v", got)
	}
	if got := numField(t, d, "grand_total"); got != 1353 {
		t.Fatalf("grand_total: want 1353, got %v", got)
	}

	// GET returns header + lines with computed line_no and line_net.
	got := do(t, "GET", "/api/v1/docs/quotes/"+id, token, nil)
	if got.Status != http.StatusOK {
		t.Fatalf("docGet failed: %d %s", got.Status, got.Raw)
	}
	lines, _ := got.Body["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	first := lines[0].(map[string]any)
	if first["line_no"].(float64) != 1 {
		t.Fatalf("expected line_no 1, got %v", first["line_no"])
	}
	if first["line_net"].(float64) != 800 {
		t.Fatalf("expected line_net 800, got %v", first["line_net"])
	}
}

func TestDocumentUpdateReplacesLines(t *testing.T) {
	token, _ := signup(t, "Docs Update Co")
	created := do(t, "POST", "/api/v1/docs/quotes", token, map[string]any{
		"description": "X", "lines": []map[string]any{{"description": "A", "qty": 1, "unit_price": 100}},
	})
	id := dataObj(t, created)["id"].(string)

	upd := do(t, "PUT", "/api/v1/docs/quotes/"+id, token, map[string]any{
		"description": "X",
		"lines": []map[string]any{
			{"description": "B", "qty": 2, "unit_price": 100},
			{"description": "C", "qty": 1, "unit_price": 50},
		},
	})
	if upd.Status != http.StatusOK {
		t.Fatalf("update failed: %d %s", upd.Status, upd.Raw)
	}
	if got := numField(t, dataObj(t, upd), "net_total"); got != 250 {
		t.Fatalf("net_total after update: want 250, got %v", got)
	}
	got := do(t, "GET", "/api/v1/docs/quotes/"+id, token, nil)
	lines, _ := got.Body["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines after replace, got %d", len(lines))
	}
}

func TestInvoiceLegalEntityAutofill(t *testing.T) {
	token, _ := signup(t, "Autofill Co")
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Urban Lines SA", "billing_address": "Rua A 1, Lisboa",
	})
	leID := dataObj(t, le)["id"].(string)

	inv := do(t, "POST", "/api/v1/docs/invoices", token, map[string]any{
		"legal_entity_id":   leID,
		"quote_description": "Services",
		"lines":             []map[string]any{{"description": "Work", "qty": 1, "unit_price": 500}},
	})
	if inv.Status != http.StatusCreated {
		t.Fatalf("invoice create failed: %d %s", inv.Status, inv.Raw)
	}
	d := dataObj(t, inv)
	if d["legal_entity_name"] != "Urban Lines SA" {
		t.Fatalf("legal_entity_name not auto-filled: %v", d["legal_entity_name"])
	}
	if d["legal_entity_billing_address"] != "Rua A 1, Lisboa" {
		t.Fatalf("billing address not auto-filled: %v", d["legal_entity_billing_address"])
	}
}

func TestConvertQuoteToOrderToInvoice(t *testing.T) {
	token, _ := signup(t, "Convert Co")
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Seller Ltd", "billing_address": "HQ Street 1",
	})
	leID := dataObj(t, le)["id"].(string)

	quote := do(t, "POST", "/api/v1/docs/quotes", token, map[string]any{
		"description": "Big Deal", "legal_entity_id": leID, "currency": "EUR", "vat_pct": 23, "markup_pct": 10,
		"payment_terms_days": 30,
		"lines": []map[string]any{
			{"sku": "SVC", "description": "Consulting", "qty": 10, "unit_price": 100},
		},
	})
	quoteID := dataObj(t, quote)["id"].(string)

	// Quote -> Sales Order
	toOrder := do(t, "POST", "/api/v1/docs/quotes/"+quoteID+"/convert", token, nil)
	if toOrder.Status != http.StatusCreated {
		t.Fatalf("convert to order failed: %d %s", toOrder.Status, toOrder.Raw)
	}
	if toOrder.Body["entity"] != "sales_orders" {
		t.Fatalf("expected sales_orders, got %v", toOrder.Body["entity"])
	}
	order := dataObj(t, toOrder)
	orderID := order["id"].(string)
	if order["quote_description"] != "Big Deal" {
		t.Fatalf("description not carried to order: %v", order["quote_description"])
	}
	if got := numField(t, order, "grand_total"); got != 1353 {
		t.Fatalf("order grand_total want 1353, got %v", got)
	}
	// lines copied
	og := do(t, "GET", "/api/v1/docs/sales_orders/"+orderID, token, nil)
	if ol, _ := og.Body["lines"].([]any); len(ol) != 1 {
		t.Fatalf("order should have 1 copied line, got %d", len(ol))
	}

	// Sales Order -> Invoice
	toInv := do(t, "POST", "/api/v1/docs/sales_orders/"+orderID+"/convert", token, nil)
	if toInv.Status != http.StatusCreated || toInv.Body["entity"] != "invoices" {
		t.Fatalf("convert to invoice failed: %d %s", toInv.Status, toInv.Raw)
	}
	invoice := dataObj(t, toInv)
	if invoice["legal_entity_name"] != "Seller Ltd" {
		t.Fatalf("invoice legal_entity_name not derived: %v", invoice["legal_entity_name"])
	}
	if invoice["sales_order_id"] != orderID {
		t.Fatalf("invoice should reference the order: %v", invoice["sales_order_id"])
	}
	if got := numField(t, invoice, "grand_total"); got != 1353 {
		t.Fatalf("invoice grand_total want 1353, got %v", got)
	}
}

func TestConvertLeadToOpportunity(t *testing.T) {
	token, _ := signup(t, "Lead Convert Co")
	lead := do(t, "POST", "/api/v1/entities/leads", token, map[string]any{
		"company": "Acme Corp", "contact_first_name": "Jane", "contact_last_name": "Doe", "email": "jane@acme.test",
		"country": "PT", "status": "Qualified", "owner": "Rep One",
	})
	leadID := dataObj(t, lead)["id"].(string)

	res := do(t, "POST", "/api/v1/docs/leads/"+leadID+"/convert", token, nil)
	if res.Status != http.StatusCreated || res.Body["entity"] != "opportunities" {
		t.Fatalf("convert lead failed: %d %s", res.Status, res.Raw)
	}
	opp := dataObj(t, res)
	accountID, _ := opp["account_id"].(string)
	if accountID == "" {
		t.Fatalf("opportunity should link to a created account: %v", opp)
	}

	// The account was created, carrying the lead's contact as a child row.
	accGet := do(t, "GET", "/api/v1/docs/accounts/"+accountID, token, nil)
	accData, _ := accGet.Body["data"].(map[string]any)
	if accData["account_name"] != "Acme Corp" {
		t.Fatalf("account name not set from lead: %s", accGet.Raw)
	}
	lines, _ := accGet.Body["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("account should have 1 contact from the lead, got %s", accGet.Raw)
	}
	contact, _ := lines[0].(map[string]any)
	if contact["first_name"] != "Jane" || contact["last_name"] != "Doe" {
		t.Fatalf("contact name not split onto first/last: %v", contact)
	}

	// The lead is stamped Converted, linked to what it became, and can't be
	// funneled a second time.
	leadGet := do(t, "GET", "/api/v1/entities/leads/"+leadID, token, nil)
	ld := dataObj(t, leadGet)
	if ld["status"] != "Converted" {
		t.Fatalf("lead not marked converted: %s", leadGet.Raw)
	}
	if ld["converted_opportunity_id"] != opp["id"] {
		t.Fatalf("lead not linked to its opportunity: %v", ld["converted_opportunity_id"])
	}
	again := do(t, "POST", "/api/v1/docs/leads/"+leadID+"/convert", token, nil)
	if again.Status != http.StatusConflict {
		t.Fatalf("re-converting a lead should be rejected, got %d %s", again.Status, again.Raw)
	}
}

func TestConvertLeadReusesExistingAccount(t *testing.T) {
	token, _ := signup(t, "Lead Reuse Co")
	// A pre-existing account with one contact already on file.
	acc := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Globex",
		"lines": []map[string]any{
			{"last_name": "Existing Person", "email": "existing@globex.test"},
		},
	})
	accID := dataObj(t, acc)["id"].(string)

	// A lead for the same company (different case) with a new contact.
	lead := do(t, "POST", "/api/v1/entities/leads", token, map[string]any{
		"company": "globex", "contact_first_name": "New", "contact_last_name": "Person", "email": "new@globex.test",
		"status": "Qualified",
	})
	leadID := dataObj(t, lead)["id"].(string)
	res := do(t, "POST", "/api/v1/docs/leads/"+leadID+"/convert", token, nil)
	if res.Status != http.StatusCreated {
		t.Fatalf("convert failed: %d %s", res.Status, res.Raw)
	}
	opp := dataObj(t, res)
	if opp["account_id"] != accID {
		t.Fatalf("should reuse the existing account %s, got %v", accID, opp["account_id"])
	}

	// The new contact is merged in alongside the existing one (2 total). A
	// second lead for the same person must not create a duplicate contact.
	accGet := do(t, "GET", "/api/v1/docs/accounts/"+accID, token, nil)
	if lines, _ := accGet.Body["lines"].([]any); len(lines) != 2 {
		t.Fatalf("account should have 2 merged contacts, got %s", accGet.Raw)
	}

	dup := do(t, "POST", "/api/v1/entities/leads", token, map[string]any{
		"company": "Globex", "contact_first_name": "New", "contact_last_name": "Person", "email": "new@globex.test",
		"status": "Qualified",
	})
	dupID := dataObj(t, dup)["id"].(string)
	if r := do(t, "POST", "/api/v1/docs/leads/"+dupID+"/convert", token, nil); r.Status != http.StatusCreated {
		t.Fatalf("second convert failed: %d %s", r.Status, r.Raw)
	}
	accGet2 := do(t, "GET", "/api/v1/docs/accounts/"+accID, token, nil)
	if lines, _ := accGet2.Body["lines"].([]any); len(lines) != 2 {
		t.Fatalf("duplicate contact should not be added, want 2 got %s", accGet2.Raw)
	}
}

func TestConvertOpportunityToQuoteSyncsAmount(t *testing.T) {
	token, _ := signup(t, "Opp Convert Co")
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Seller Ltd", "billing_address": "HQ Street 1",
	})
	leID := dataObj(t, le)["id"].(string)
	acc := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Big Buyer", "legal_entity_id": leID,
	})
	accID := dataObj(t, acc)["id"].(string)
	opp := do(t, "POST", "/api/v1/entities/opportunities", token, map[string]any{
		"account_id": accID, "opportunity_name": "Platform Deal",
		"stage": "Proposal", "probability_pct": 50,
	})
	oppID := dataObj(t, opp)["id"].(string)

	// Opportunity -> Quote: pre-filled with the account and its billing entity.
	res := do(t, "POST", "/api/v1/docs/opportunities/"+oppID+"/convert", token, nil)
	if res.Status != http.StatusCreated || res.Body["entity"] != "quotes" {
		t.Fatalf("convert opportunity failed: %d %s", res.Status, res.Raw)
	}
	quote := dataObj(t, res)
	quoteID := quote["id"].(string)
	if quote["account_id"] != accID {
		t.Fatalf("quote should carry the account: %v", quote["account_id"])
	}
	if quote["opportunity_id"] != oppID {
		t.Fatalf("quote should reference the opportunity: %v", quote["opportunity_id"])
	}
	if quote["legal_entity_id"] != leID {
		t.Fatalf("quote legal entity should default from the account: %v", quote["legal_entity_id"])
	}
	if quote["description"] != "Platform Deal" {
		t.Fatalf("quote description not set from opportunity: %v", quote["description"])
	}

	// Add lines to the quote; its net total flows back onto the opportunity as
	// amount (net) and probability-weighted value.
	upd := do(t, "PUT", "/api/v1/docs/quotes/"+quoteID, token, map[string]any{
		"account_id": accID, "opportunity_id": oppID, "description": "Platform Deal",
		"lines": []map[string]any{
			{"sku": "SVC", "description": "Build", "qty": 4, "unit_price": 250},
		},
	})
	if upd.Status != http.StatusOK {
		t.Fatalf("quote update failed: %d %s", upd.Status, upd.Raw)
	}

	oppGet := do(t, "GET", "/api/v1/entities/opportunities/"+oppID, token, nil)
	od := dataObj(t, oppGet)
	if got := numField(t, od, "amount_net"); got != 1000 {
		t.Fatalf("opportunity amount_net want 1000, got %v", got)
	}
	if got := numField(t, od, "weighted_amount"); got != 500 {
		t.Fatalf("opportunity weighted_amount want 500 (50%%), got %v", got)
	}
}

func TestDocEndpointsRejectNonDocument(t *testing.T) {
	token, _ := signup(t, "NonDoc Co")
	// leads has no child collection, so the document endpoints must reject it.
	r := do(t, "POST", "/api/v1/docs/leads", token, map[string]any{"company": "X"})
	if r.Status != http.StatusNotFound {
		t.Fatalf("expected 404 for non-document entity, got %d", r.Status)
	}
}
