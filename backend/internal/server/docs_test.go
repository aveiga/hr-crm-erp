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

func TestDocEndpointsRejectNonDocument(t *testing.T) {
	token, _ := signup(t, "NonDoc Co")
	// leads has no child collection, so the document endpoints must reject it.
	r := do(t, "POST", "/api/v1/docs/leads", token, map[string]any{"company": "X"})
	if r.Status != http.StatusNotFound {
		t.Fatalf("expected 404 for non-document entity, got %d", r.Status)
	}
}
