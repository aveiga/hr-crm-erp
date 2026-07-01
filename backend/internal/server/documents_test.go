package server_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGenerateOfferDocx(t *testing.T) {
	token, _ := signup(t, "Docs Co")

	// Seller legal entity.
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Urban Lines SA", "billing_address": "Rua A 1, Lisboa", "default_currency": "EUR",
	})
	leID := dataObj(t, le)["id"].(string)

	// Customer account.
	acc := do(t, "POST", "/api/v1/entities/accounts", token, map[string]any{
		"account_name": "Globex", "legal_entity_name": "Globex Holding BV", "legal_entity_address": "Dam 1, Amsterdam",
	})
	accID := dataObj(t, acc)["id"].(string)

	// Invoice.
	inv := do(t, "POST", "/api/v1/entities/invoices", token, map[string]any{
		"account_id": accID, "legal_entity_id": leID, "quote_description": "Q1 Consulting",
		"currency": "EUR", "vat_pct": 23, "net_total": 1000.0, "vat_total": 230.0, "grand_total": 1230.0,
		"invoice_date": "2026-01-15", "due_date": "2026-02-14",
	})
	if inv.Status != http.StatusCreated {
		t.Fatalf("invoice create failed: %d %s", inv.Status, inv.Raw)
	}
	invID := dataObj(t, inv)["id"].(string)

	// Two line items.
	for i, ln := range []map[string]any{
		{"invoice_id": invID, "line_no": 1, "description": "Senior Engineer", "qty": 10, "unit_price": 80.0, "line_net": 800.0},
		{"invoice_id": invID, "line_no": 2, "description": "Project Mgmt", "qty": 4, "unit_price": 50.0, "line_net": 200.0},
	} {
		r := do(t, "POST", "/api/v1/entities/invoice_lines", token, ln)
		if r.Status != http.StatusCreated {
			t.Fatalf("line %d create failed: %d %s", i, r.Status, r.Raw)
		}
	}

	// Generate the Offer as DOCX (no LibreOffice needed).
	gen := do(t, "GET", "/api/v1/documents/offer/"+invID+"?format=docx", token, nil)
	if gen.Status != http.StatusOK {
		t.Fatalf("offer generation failed: %d %s", gen.Status, gen.Raw)
	}
	if !bytes.HasPrefix(gen.Raw, []byte("PK")) {
		t.Fatalf("expected a docx (zip) payload, got %d bytes", len(gen.Raw))
	}

	xml := readDocumentXML(t, gen.Raw)
	for _, want := range []string{
		"COMMERCIAL OFFER", "Urban Lines SA", "Globex", "Q1 Consulting",
		"Senior Engineer", "Project Mgmt", "1230.00", "800.00", "200.00",
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("offer document.xml missing %q", want)
		}
	}
	// No unrendered placeholders should remain.
	if strings.Contains(xml, "{{") {
		t.Fatalf("offer still contains unrendered placeholders")
	}
}

func TestGenerateIGTRDocx(t *testing.T) {
	token, _ := signup(t, "IGTR Co")
	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{"legal_name": "Seller Ltd"})
	leID := dataObj(t, le)["id"].(string)
	acc := do(t, "POST", "/api/v1/entities/accounts", token, map[string]any{
		"account_name": "Buyer Co", "legal_entity_name": "Buyer Group", "legal_entity_address": "Street 9",
	})
	accID := dataObj(t, acc)["id"].(string)
	inv := do(t, "POST", "/api/v1/entities/invoices", token, map[string]any{
		"account_id": accID, "legal_entity_id": leID, "quote_description": "Body leasing Jan",
		"currency": "EUR", "grand_total": 5000.0,
		"service_start_date": "2026-01-01", "service_end_date": "2026-01-31",
	})
	invID := dataObj(t, inv)["id"].(string)
	do(t, "POST", "/api/v1/entities/invoice_lines", token, map[string]any{
		"invoice_id": invID, "line_no": 1, "description": "Engineer - January", "line_net": 5000.0,
	})

	gen := do(t, "GET", "/api/v1/documents/igtr/"+invID+"?format=docx", token, nil)
	if gen.Status != http.StatusOK {
		t.Fatalf("igtr generation failed: %d %s", gen.Status, gen.Raw)
	}
	xml := readDocumentXML(t, gen.Raw)
	for _, want := range []string{"INTER-GROUP TRANSACTION", "Seller Ltd", "Buyer Group", "Engineer - January", "01-01-2026"} {
		if !strings.Contains(xml, want) {
			t.Fatalf("igtr document.xml missing %q", want)
		}
	}
}

func TestGenerateRejectsUnknownKind(t *testing.T) {
	token, _ := signup(t, "Bad Kind Co")
	inv := do(t, "POST", "/api/v1/entities/invoices", token, map[string]any{"quote_description": "x"})
	invID := dataObj(t, inv)["id"].(string)
	r := do(t, "GET", "/api/v1/documents/contract/"+invID+"?format=docx", token, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown kind, got %d", r.Status)
	}
}

func readDocumentXML(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open docx zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open document.xml: %v", err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatalf("docx has no word/document.xml")
	return ""
}
