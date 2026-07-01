package server_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// Contacts are now managed as a child collection of an account.
func TestAccountWithContacts(t *testing.T) {
	token, _ := signup(t, "Accounts+Contacts Co")

	created := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Globex",
		"country":      "Portugal",
		"lines": []map[string]any{
			{"first_name": "Ada", "last_name": "Lovelace", "email": "ada@globex.com"},
			{"first_name": "Alan", "last_name": "Turing", "email": "alan@globex.com"},
		},
	})
	if created.Status != http.StatusCreated {
		t.Fatalf("account create failed: %d %s", created.Status, created.Raw)
	}
	id := dataObj(t, created)["id"].(string)

	got := do(t, "GET", "/api/v1/docs/accounts/"+id, token, nil)
	if got.Status != http.StatusOK {
		t.Fatalf("docGet account failed: %d %s", got.Status, got.Raw)
	}
	contacts, _ := got.Body["lines"].([]any)
	if len(contacts) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(contacts))
	}
	c0 := contacts[0].(map[string]any)
	if c0["last_name"] != "Lovelace" || c0["account_id"] != id {
		t.Fatalf("contact not linked correctly: %v", c0)
	}

	// Update: replace with a single contact.
	upd := do(t, "PUT", "/api/v1/docs/accounts/"+id, token, map[string]any{
		"account_name": "Globex",
		"lines":        []map[string]any{{"first_name": "Grace", "last_name": "Hopper"}},
	})
	if upd.Status != http.StatusOK {
		t.Fatalf("account update failed: %d %s", upd.Status, upd.Raw)
	}
	got2 := do(t, "GET", "/api/v1/docs/accounts/"+id, token, nil)
	if c, _ := got2.Body["lines"].([]any); len(c) != 1 {
		t.Fatalf("expected 1 contact after replace, got %d", len(c))
	}
}

// Contact rows missing the required last_name should be rejected.
func TestAccountContactRequiredValidation(t *testing.T) {
	token, _ := signup(t, "Contact Validation Co")
	r := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "X",
		"lines":        []map[string]any{{"first_name": "NoLastName"}},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing required contact field, got %d %s", r.Status, r.Raw)
	}
}

// Contacts are a read-only directory: searchable via the generic list, but not
// writable through the generic endpoints (they're edited via their Account).
func TestContactsReadOnlyDirectory(t *testing.T) {
	token, _ := signup(t, "Contacts Dir Co")

	// Create a contact through its account.
	acc := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{
		"account_name": "Initech",
		"lines":        []map[string]any{{"first_name": "Peter", "last_name": "Gibbons", "email": "peter@initech.com"}},
	})
	accID := dataObj(t, acc)["id"].(string)

	// It is browsable + searchable in the global contacts list.
	list := do(t, "GET", "/api/v1/entities/contacts?q=Gibbons", token, nil)
	if list.Status != http.StatusOK {
		t.Fatalf("contacts list failed: %d", list.Status)
	}
	rows, _ := list.Body["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected to find 1 contact, got %d", len(rows))
	}
	c := rows[0].(map[string]any)
	if c["account_id"] != accID {
		t.Fatalf("contact not linked to its account: %v", c["account_id"])
	}

	// Direct writes through the generic endpoints are rejected.
	wr := do(t, "POST", "/api/v1/entities/contacts", token, map[string]any{"last_name": "Nope"})
	if wr.Status != http.StatusForbidden || wr.errCode() != "read_only" {
		t.Fatalf("expected 403 read_only on create, got %d %s", wr.Status, wr.Raw)
	}
	id := c["id"].(string)
	up := do(t, "PUT", "/api/v1/entities/contacts/"+id, token, map[string]any{"last_name": "Changed"})
	if up.Status != http.StatusForbidden {
		t.Fatalf("expected 403 on update, got %d", up.Status)
	}
	del := do(t, "DELETE", "/api/v1/entities/contacts/"+id, token, nil)
	if del.Status != http.StatusForbidden {
		t.Fatalf("expected 403 on delete, got %d", del.Status)
	}
}

// Opportunities no longer expose a quote_id field; quotes keep opportunity_id.
func TestOpportunityHasNoQuoteField(t *testing.T) {
	token, _ := signup(t, "Meta Check Co")
	meta := do(t, "GET", "/api/v1/meta", token, nil)
	ents, _ := meta.Body["entities"].([]any)
	for _, e := range ents {
		m := e.(map[string]any)
		fields, _ := m["fields"].([]any)
		keys := map[string]bool{}
		for _, f := range fields {
			keys[f.(map[string]any)["key"].(string)] = true
		}
		switch m["key"] {
		case "opportunities":
			if keys["quote_id"] {
				t.Fatalf("opportunities should not have quote_id")
			}
		case "quotes":
			if !keys["opportunity_id"] {
				t.Fatalf("quotes should keep opportunity_id")
			}
		}
	}
}

// Upload a custom Offer template and generate using it.
func TestTemplateUploadAndGenerate(t *testing.T) {
	token, _ := signup(t, "Templates Co")

	// Build a minimal valid .docx with our own marker text + placeholders.
	docx := makeDocx(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
		`<w:p><w:r><w:t>CUSTOM-OFFER-MARKER {{Quote_Description}}</w:t></w:r></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>{{line.description}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`</w:body></w:document>`)

	// Upload it as an offer template (multipart).
	tplID := uploadTemplate(t, token, "offer", "My Offer", docx)

	// Create an invoice with a line to render.
	inv := do(t, "POST", "/api/v1/docs/invoices", token, map[string]any{
		"quote_description": "Special Project",
		"lines":             []map[string]any{{"description": "Custom work", "qty": 1, "unit_price": 10}},
	})
	invID := dataObj(t, inv)["id"].(string)

	// Generate with the uploaded template (DOCX, no LibreOffice needed).
	gen := do(t, "GET", "/api/v1/documents/offer/"+invID+"?format=docx&template_id="+tplID, token, nil)
	if gen.Status != http.StatusOK {
		t.Fatalf("generate with template failed: %d %s", gen.Status, gen.Raw)
	}
	xml := documentXML(t, gen.Raw)
	if !strings.Contains(xml, "CUSTOM-OFFER-MARKER") {
		t.Fatalf("uploaded template was not used")
	}
	if !strings.Contains(xml, "Special Project") || !strings.Contains(xml, "Custom work") {
		t.Fatalf("placeholders not filled in uploaded template: %s", xml)
	}

	// Listing shows the uploaded template.
	list := do(t, "GET", "/api/v1/templates?kind=offer", token, nil)
	if d, _ := list.Body["data"].([]any); len(d) != 1 {
		t.Fatalf("expected 1 offer template, got %d", len(d))
	}
}

// Word often splits placeholders across runs; normalization must still fill them.
func TestTemplateSplitPlaceholderNormalized(t *testing.T) {
	token, _ := signup(t, "Split Tpl Co")
	// {{Quote_Description}} split across two <w:t> runs, as Word would.
	docx := makeDocx(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
		`<w:p><w:r><w:t>{{Quote_</w:t></w:r><w:r><w:t>Description}}</w:t></w:r></w:p>`+
		`</w:body></w:document>`)
	tplID := uploadTemplate(t, token, "igtr", "Split", docx)
	inv := do(t, "POST", "/api/v1/docs/invoices", token, map[string]any{"quote_description": "JoinedValue"})
	invID := dataObj(t, inv)["id"].(string)
	gen := do(t, "GET", "/api/v1/documents/igtr/"+invID+"?format=docx&template_id="+tplID, token, nil)
	xml := documentXML(t, gen.Raw)
	if !strings.Contains(xml, "JoinedValue") {
		t.Fatalf("split placeholder not normalized/filled: %s", xml)
	}
}

func TestTemplateUploadRejectsNonDocx(t *testing.T) {
	token, _ := signup(t, "Bad Upload Co")
	code := uploadTemplateRaw(t, token, "offer", "bad", []byte("not a zip"))
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-docx upload, got %d", code)
	}
}

// --- helpers ---------------------------------------------------------------

func makeDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   documentXML,
	}
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadTemplate(t *testing.T, token, kind, name string, docx []byte) string {
	t.Helper()
	body, ct := multipartTemplate(t, kind, name, docx)
	req, _ := http.NewRequest("POST", testServer.URL+"/api/v1/templates", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("template upload failed: %d %s", resp.StatusCode, raw)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m["id"].(string)
}

func uploadTemplateRaw(t *testing.T, token, kind, name string, data []byte) int {
	t.Helper()
	body, ct := multipartTemplate(t, kind, name, data)
	req, _ := http.NewRequest("POST", testServer.URL+"/api/v1/templates", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func multipartTemplate(t *testing.T, kind, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("kind", kind)
	_ = mw.WriteField("name", name)
	fw, _ := mw.CreateFormFile("file", "template.docx")
	_, _ = fw.Write(data)
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func documentXML(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			defer rc.Close()
			out, _ := io.ReadAll(rc)
			return string(out)
		}
	}
	t.Fatal("no word/document.xml")
	return ""
}
