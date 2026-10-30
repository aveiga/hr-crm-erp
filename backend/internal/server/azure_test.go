package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeAzure serves the Entra token endpoint, the ARM subscription list and the
// Cost Management query API. Subscription sub-a costs 10 EUR/day, sub-b
// 4 EUR/day, every day of any queried window.
const fakeSecret = "s3cret"

var fakeDaily = map[string]float64{"sub-a": 10, "sub-b": 4}

func fakeAzureHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{tenant}/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("client_secret") != fakeSecret || r.PostForm.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided.\r\nTrace ID: x"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3599}`))
	})
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"InvalidAuthenticationToken","message":"bad token"}}`))
			return false
		}
		return true
	}
	mux.HandleFunc("GET /subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		_, _ = w.Write([]byte(`{"value":[
			{"subscriptionId":"sub-a","displayName":"Prod A","state":"Enabled","tenantId":"dir-1"},
			{"subscriptionId":"sub-b","displayName":"Dev B","state":"Enabled","tenantId":"dir-1"}]}`))
	})
	mux.HandleFunc("POST /subscriptions/{sub}/providers/Microsoft.CostManagement/query", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var body struct {
			TimePeriod struct{ From, To string } `json:"timePeriod"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		from, _ := time.Parse("2006-01-02", body.TimePeriod.From[:10])
		to, _ := time.Parse("2006-01-02", body.TimePeriod.To[:10])
		var rows [][]any
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			date, _ := json.Marshal(d.Year()*10000 + int(d.Month())*100 + d.Day())
			rows = append(rows, []any{fakeDaily[r.PathValue("sub")], json.RawMessage(date), "Virtual Machines", "EUR"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"properties": map[string]any{
			"columns": []map[string]string{{"name": "Cost", "type": "Number"}, {"name": "UsageDate", "type": "Number"},
				{"name": "ServiceName", "type": "String"}, {"name": "Currency", "type": "String"}},
			"rows": rows,
		}})
	})
	return mux
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.006 }

// Azure fee billing end to end: connect a tenant, sync subscriptions and
// usage, map subscriptions to an account with types/rates, build a quarterly
// agreement (actual past quarters, forecast future ones), invoice a quarter
// (true-up), pay it, cancel it, and read the dashboard.
func TestAzureFeeBilling(t *testing.T) {
	token, _ := signup(t, "Azure Co")

	// Customer account billed in EUR.
	buyer := dataObj(t, do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{
		"legal_name": "Contoso BV", "default_currency": "EUR"}))
	acc := do(t, "POST", "/api/v1/docs/accounts", token, map[string]any{"account_name": "Contoso", "buying_entity_id": buyer["id"]})
	accID := dataObj(t, acc)["id"].(string)

	// Azure tenant + credentials (secret is write-only).
	tn := do(t, "POST", "/api/v1/entities/azure_tenants", token, map[string]any{"name": "Main", "directory_id": "dir-1", "client_id": "app-1"})
	if tn.Status != http.StatusCreated || dataObj(t, tn)["active"] != true {
		t.Fatalf("azure tenant create (active by default): %d %s", tn.Status, tn.Raw)
	}
	tnID := dataObj(t, tn)["id"].(string)
	if r := do(t, "POST", "/api/v1/azure/tenants/"+tnID+"/test", token, nil); r.errCode() != "no_secret" {
		t.Fatalf("test without secret should fail no_secret: %s", r.Raw)
	}
	do(t, "PUT", "/api/v1/azure/tenants/"+tnID+"/credential", token, map[string]any{"client_secret": "wrong"})
	if r := do(t, "POST", "/api/v1/azure/tenants/"+tnID+"/test", token, nil); r.Status != http.StatusBadGateway || !strings.Contains(string(r.Raw), "Invalid client secret") {
		t.Fatalf("bad secret should surface the sign-in error: %d %s", r.Status, r.Raw)
	}
	cred := do(t, "PUT", "/api/v1/azure/tenants/"+tnID+"/credential", token, map[string]any{"client_secret": fakeSecret, "expires_on": "2027-06-30"})
	if cred.Body["has_secret"] != true || strings.Contains(string(cred.Raw), fakeSecret) {
		t.Fatalf("credential should be stored and never echoed: %s", cred.Raw)
	}
	if r := do(t, "POST", "/api/v1/azure/tenants/"+tnID+"/test", token, nil); r.Status != http.StatusOK {
		t.Fatalf("test connection: %d %s", r.Status, r.Raw)
	}

	// Sync H1 2026 (synced through the day before the window end).
	sync := do(t, "POST", "/api/v1/azure/tenants/"+tnID+"/sync", token, map[string]any{"from": "2026-01-01", "to": "2026-07-01", "wait": true})
	if sync.Status != http.StatusOK || numField(t, sync.Body, "new_subscriptions") != 2 || numField(t, sync.Body, "usage_rows") != 2*182 {
		t.Fatalf("sync: %d %s", sync.Status, sync.Raw)
	}
	tnRow := dataObj(t, do(t, "GET", "/api/v1/entities/azure_tenants/"+tnID, token, nil))
	if tnRow["sync_status"] != "OK" || !strings.HasPrefix(tnRow["synced_through"].(string), "2026-06-30") {
		t.Fatalf("sync status: %v", tnRow)
	}
	// Re-syncing a window replaces its usage instead of duplicating it.
	do(t, "POST", "/api/v1/azure/tenants/"+tnID+"/sync", token, map[string]any{"from": "2026-06-01", "to": "2026-06-30", "wait": true})
	if n := numField(t, do(t, "GET", "/api/v1/entities/azure_usage?fmin_usage_date=2026-06-01&fmax_usage_date=2026-06-30", token, nil).Body, "total"); n != 60 {
		t.Fatalf("June usage rows after re-sync = %v, want 60", n)
	}

	// Map: A is TAN (rate 25%, 20% from April), B is TSS with a 30% override.
	subs := do(t, "GET", "/api/v1/entities/azure_subscriptions?sort=subscription_id", token, nil).Body["data"].([]any)
	subA, subB := subs[0].(map[string]any)["id"].(string), subs[1].(map[string]any)["id"].(string)
	do(t, "PUT", "/api/v1/entities/azure_subscriptions/"+subA, token, map[string]any{"account_id": accID, "subscription_type": "TAN"})
	do(t, "PUT", "/api/v1/entities/azure_subscriptions/"+subB, token, map[string]any{"account_id": accID, "subscription_type": "TSS", "fee_pct": 30})
	do(t, "POST", "/api/v1/entities/azure_fee_rates", token, map[string]any{"subscription_type": "TAN", "fee_pct": 20, "valid_from": "2026-04-01"})

	// Agreement for 2026 with 10% markup: Q1–Q2 actual, Q3–Q4 forecast.
	//   Q1: A 900×25% + B 360×30% = 333     Q2: A 910×20% + B 364×30% = 291.2
	//   Q3/Q4 (92 days at the run-rate): A 920×20% + B 368×30% = 294.4
	ag := do(t, "POST", "/api/v1/azure/agreements", token, map[string]any{
		"account_id": accID, "period_start": "2026-01-01", "period_end": "2026-12-31", "markup_pct": 10, "vat_pct": 0})
	if ag.Status != http.StatusCreated {
		t.Fatalf("azure agreement: %d %s", ag.Status, ag.Raw)
	}
	agID := dataObj(t, ag)["id"].(string)
	sa, sched := getAgreement(t, token, agID)
	if !near(numField(t, sa, "grand_total"), 1334.3) || sa["azure_fees"] != true || sa["billing_frequency"] != "Quarterly" {
		t.Fatalf("agreement totals: %v", sa)
	}
	wantInst := []float64{366.3, 320.32, 323.84, 323.84}
	if len(sched) != 4 {
		t.Fatalf("want 4 quarterly installments, got %v", sched)
	}
	for i, w := range wantInst {
		if !near(numField(t, sched[i], "amount"), w) {
			t.Fatalf("installment %d = %v, want %v", i, sched[i]["amount"], w)
		}
	}
	qs := do(t, "GET", "/api/v1/azure/agreements/"+agID+"/quarters", token, nil).Body["quarters"].([]any)
	if qs[0].(map[string]any)["basis"] != "Actual" || qs[2].(map[string]any)["basis"] != "Forecast" {
		t.Fatalf("quarter bases: %v", qs)
	}

	// Notes: actual usage per subscription (and quarter) of the account.
	notes := sa["notes"].(string)
	for _, want := range []string{
		"Actual usage 01-01-2026 – 30-06-2026",
		"• Prod A (sub-a) · TAN · fee 22.49% · usage 1,810.00 EUR · fee 407.00 EUR",
		"    Q1 2026: usage 900.00 · fee 225.00",
		"    Q2 2026: usage 910.00 · fee 182.00",
		"• Dev B (sub-b) · TSS · fee 30% · usage 724.00 EUR · fee 217.20 EUR",
		"Total: usage 2,534.00 EUR · fee 624.20 EUR",
		"01-07-2026 – 31-12-2026 is not in yet",
		"--- end of Azure usage ---",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("agreement notes missing %q:\n%s", want, notes)
		}
	}
	// Text added around the block survives a refresh; the block isn't duplicated.
	do(t, "PUT", "/api/v1/entities/invoices/"+agID, token, map[string]any{"notes": "Signed by the CFO.\n\n" + notes})
	if r := do(t, "POST", "/api/v1/azure/agreements/"+agID+"/refresh", token, nil); r.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.Status, r.Raw)
	}
	sa, sched = getAgreement(t, token, agID)
	if n := sa["notes"].(string); !strings.HasPrefix(n, "Signed by the CFO.\n\n--- Azure usage") || strings.Count(n, "--- Azure usage") != 1 {
		t.Fatalf("notes after refresh:\n%s", n)
	}

	// Q3 isn't synced yet: refused unless forced.
	if r := do(t, "POST", "/api/v1/azure/agreements/"+agID+"/invoices", token, map[string]any{"period_start": "2026-07-01"}); r.errCode() != "usage_incomplete" {
		t.Fatalf("Q3 invoice should need complete usage: %s", r.Raw)
	}

	// Invoice Q1: one line per subscription at its fee %, settling Q1's installment.
	ir := do(t, "POST", "/api/v1/azure/agreements/"+agID+"/invoices", token, map[string]any{"period_start": "2026-01-01"})
	if ir.Status != http.StatusCreated {
		t.Fatalf("invoice: %d %s", ir.Status, ir.Raw)
	}
	inv := dataObj(t, ir)
	invID := inv["id"].(string)
	if !near(numField(t, inv, "net_total"), 333) || !near(numField(t, inv, "grand_total"), 366.3) || inv["schedule_id"] != sched[0]["id"] || inv["currency"] != "EUR" {
		t.Fatalf("invoice header: %v", inv)
	}
	if r := do(t, "POST", "/api/v1/azure/agreements/"+agID+"/invoices", token, map[string]any{"period_start": "2026-01-01"}); r.errCode() != "already_invoiced" {
		t.Fatalf("second Q1 invoice should be refused: %s", r.Raw)
	}
	doc := do(t, "GET", "/api/v1/docs/sales_invoices/"+invID, token, nil)
	lines := doc.Body["lines"].([]any)
	byCost := map[float64]map[string]any{}
	for _, l := range lines {
		byCost[numField(t, l.(map[string]any), "usage_cost")] = l.(map[string]any)
	}
	if len(lines) != 2 || !near(numField(t, byCost[900], "fee_pct"), 25) || !near(numField(t, byCost[360], "fee_pct"), 30) {
		t.Fatalf("invoice lines: %s", doc.Raw)
	}

	// Editing the invoice trues up the agreement: Q1 follows the invoice.
	body := doc.Body["data"].(map[string]any)
	byCost[900]["unit_price"] = 200
	body["lines"] = lines
	if r := do(t, "PUT", "/api/v1/docs/sales_invoices/"+invID, token, body); r.Status != http.StatusOK {
		t.Fatalf("invoice edit: %d %s", r.Status, r.Raw)
	}
	sa, sched = getAgreement(t, token, agID)
	if !near(numField(t, sched[0], "amount"), 338.8) || !near(numField(t, sa, "grand_total"), 1306.8) || !near(numField(t, sched[3], "amount"), 323.84) {
		t.Fatalf("true-up: grand %v, installments %v / %v", sa["grand_total"], sched[0]["amount"], sched[3]["amount"])
	}

	// Paying the installment pays the invoice.
	do(t, "POST", "/api/v1/entities/payments", token, map[string]any{"schedule_id": sched[0]["id"], "payment_date": "2026-04-15"})
	inv = dataObj(t, do(t, "GET", "/api/v1/entities/sales_invoices/"+invID, token, nil))
	if inv["status"] != "Paid" || !near(numField(t, inv, "balance"), 0) {
		t.Fatalf("invoice should be paid: %v", inv)
	}

	// Dashboard: TSS only → sub B. Actual Jan–Jun = 181 days × 4 at 30%;
	// forecast Jul–Dec = 184 days at the run-rate.
	d := do(t, "GET", "/api/v1/azure/dashboard?from=2026-01-01&to=2026-12-31&subscription_type=TSS&markup_pct=10", token, nil)
	tot := d.Body["totals"].([]any)[0].(map[string]any)
	if !near(numField(t, tot, "usage"), 724) || !near(numField(t, tot, "fee"), 217.2) || !near(numField(t, tot, "forecast_fee"), 220.8) ||
		!near(numField(t, tot, "fee_with_markup"), 481.8) {
		t.Fatalf("dashboard totals: %s", d.Raw)
	}
	all := do(t, "GET", "/api/v1/azure/dashboard?from=2026-01-01&to=2026-12-31&account_id="+accID, token, nil)
	if fees := all.Body["invoiced"].([]any); len(fees) != 1 || !near(numField(t, fees[0].(map[string]any), "paid"), 338.8) {
		t.Fatalf("dashboard invoiced fees: %s", all.Raw)
	}
	if len(all.Body["by_month"].([]any)) != 12 {
		t.Fatalf("dashboard months: %s", all.Raw)
	}

	// Cancelling the invoice re-prices Q1 from usage again.
	if r := do(t, "PUT", "/api/v1/entities/sales_invoices/"+invID, token, map[string]any{"status": "Cancelled"}); r.Status != http.StatusOK {
		t.Fatalf("cancel: %d %s", r.Status, r.Raw)
	}
	_, sched = getAgreement(t, token, agID)
	if !near(numField(t, sched[0], "amount"), 366.3) {
		t.Fatalf("cancelled invoice should release Q1: %v", sched[0]["amount"])
	}
}

// A lead's contact details fill from a picked contact of its company.
func TestLeadContactFromCompany(t *testing.T) {
	token, _ := signup(t, "Lead Co")
	_, accID, _ := salesFixture(t, token)
	acc := dataObj(t, do(t, "GET", "/api/v1/entities/accounts/"+accID, token, nil))
	contacts := do(t, "GET", "/api/v1/entities/contacts?f_buying_entity_id="+acc["buying_entity_id"].(string), token, nil)
	if numField(t, contacts.Body, "total") != 1 {
		t.Fatalf("contacts should carry the account's buying entity: %s", contacts.Raw)
	}
	c := contacts.Body["data"].([]any)[0].(map[string]any)
	lead := do(t, "POST", "/api/v1/entities/leads", token, map[string]any{
		"legal_entity_id": acc["buying_entity_id"], "contact_id": c["id"], "email": "typed@globex.test"})
	if lead.Status != http.StatusCreated {
		t.Fatalf("lead: %d %s", lead.Status, lead.Raw)
	}
	l := dataObj(t, lead)
	if l["contact_first_name"] != "Jo" || l["contact_last_name"] != "Buyer" || l["email"] != "typed@globex.test" {
		t.Fatalf("lead contact fill: %v", l)
	}
}

// costCSV builds a Cost analysis download like the portal's (quoted, CRLF).
func costCSV(rows ...string) string {
	return `"UsageDate","SubscriptionName","EnrollmentAccountName","DepartmentName","CostUSD","Cost","Currency"` + "\r\n" +
		strings.Join(rows, "\r\n") + "\r\n"
}

func importCSV(t *testing.T, token string, fields map[string]string, csvText string) apiResp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", "cost-analysis.csv")
	_, _ = fw.Write([]byte(csvText))
	mw.Close()
	req, _ := http.NewRequest("POST", testServer.URL+"/api/v1/azure/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// The CSV fallback adds new subscription-days, leaves identical ones alone and
// only replaces changed days the user picks.
func TestAzureCSVImport(t *testing.T) {
	token, _ := signup(t, "Azure CSV Co")
	tnID := dataObj(t, do(t, "POST", "/api/v1/entities/azure_tenants", token, map[string]any{"name": "Main", "directory_id": "dir-1"}))["id"].(string)
	const p = `"TVC-Platform(42c01d7b-eac2-4d06-a9a3-894d9016c3bb)","TVC-Platform","GTS-Global"`
	const o = `"Other Sub(11111111-2222-3333-4444-555555555555)","Other","GTS-Global"`
	first := costCSV(
		`"2026-04-01",`+p+`,"13.2","10","GBP"`,
		`"2026-04-02",`+p+`,"26.4","20","GBP"`,
		`"2026-04-03",`+p+`,"39.6","30","GBP"`,
		`"2026-04-01",`+o+`,"6.6","5","GBP"`,
	)

	pv := importCSV(t, token, map[string]string{"mode": "preview"}, first)
	if pv.Status != http.StatusOK || numField(t, pv.Body, "new_days") != 4 || len(pv.Body["changes"].([]any)) != 0 {
		t.Fatalf("first preview: %d %s", pv.Status, pv.Raw)
	}
	if r := importCSV(t, token, map[string]string{"mode": "apply"}, first); r.Status != http.StatusBadRequest {
		t.Fatalf("apply needs an Azure tenant: %d %s", r.Status, r.Raw)
	}
	ap := importCSV(t, token, map[string]string{"mode": "apply", "azure_tenant_id": tnID}, first)
	if ap.Status != http.StatusOK || numField(t, ap.Body, "added_days") != 4 || numField(t, ap.Body, "new_subscriptions") != 2 ||
		ap.Body["synced_through"] != "2026-04-03" {
		t.Fatalf("first apply: %d %s", ap.Status, ap.Raw)
	}
	sub := do(t, "GET", "/api/v1/entities/azure_subscriptions?f_subscription_id=42c01d7b-eac2-4d06-a9a3-894d9016c3bb", token, nil)
	if numField(t, sub.Body, "total") != 1 || sub.Body["data"].([]any)[0].(map[string]any)["display_name"] != "TVC-Platform" {
		t.Fatalf("subscription created from the file: %s", sub.Raw)
	}

	// Second file: 04-02 changed, 04-04 new, the rest identical.
	second := costCSV(
		`"2026-04-01",`+p+`,"13.2","10","GBP"`,
		`"2026-04-02",`+p+`,"33","25","GBP"`,
		`"2026-04-03",`+p+`,"39.6","30","GBP"`,
		`"2026-04-04",`+p+`,"52.8","40","GBP"`,
		`"2026-04-01",`+o+`,"6.6","5","GBP"`,
	)
	pv = importCSV(t, token, map[string]string{"mode": "preview"}, second)
	ch := pv.Body["changes"].([]any)
	if numField(t, pv.Body, "new_days") != 1 || numField(t, pv.Body, "unchanged_days") != 3 || len(ch) != 1 {
		t.Fatalf("second preview: %s", pv.Raw)
	}
	c := ch[0].(map[string]any)
	if c["date"] != "2026-04-02" || numField(t, c, "existing") != 20 || numField(t, c, "incoming") != 25 {
		t.Fatalf("change: %v", c)
	}
	dayCost := func() float64 {
		r := do(t, "GET", "/api/v1/entities/azure_usage?f_usage_date=2026-04-02", token, nil)
		return numField(t, r.Body["data"].([]any)[0].(map[string]any), "cost")
	}

	// Without choosing the change, only the new day is added.
	ap = importCSV(t, token, map[string]string{"mode": "apply", "azure_tenant_id": tnID}, second)
	if numField(t, ap.Body, "added_days") != 1 || numField(t, ap.Body, "kept_days") != 1 || dayCost() != 20 {
		t.Fatalf("apply keeping changes: %s", ap.Raw)
	}
	// Choosing it replaces that day.
	keys, _ := json.Marshal([]string{c["key"].(string)})
	ap = importCSV(t, token, map[string]string{"mode": "apply", "azure_tenant_id": tnID, "overwrite": string(keys)}, second)
	if numField(t, ap.Body, "overwritten_days") != 1 || dayCost() != 25 {
		t.Fatalf("apply overwriting: %s", ap.Raw)
	}
	if n := numField(t, do(t, "GET", "/api/v1/entities/azure_usage", token, nil).Body, "total"); n != 5 {
		t.Fatalf("usage rows = %v, want 5 (no duplicates)", n)
	}

	if r := importCSV(t, token, map[string]string{"mode": "preview"}, "a,b\r\n1,2\r\n"); r.errCode() != "bad_csv" {
		t.Fatalf("unrecognised file should be bad_csv: %s", r.Raw)
	}
}
