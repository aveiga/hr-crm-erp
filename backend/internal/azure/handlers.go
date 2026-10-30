package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// Handlers exposes the Azure module. Every route requires the "Azure"
// section; billing routes also need the section of the documents they write.
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// guard checks the caller's access to the sections of the given entities and
// parses the {id} path value when the route has one.
func (h *Handlers) guard(w http.ResponseWriter, r *http.Request, entities ...string) (httpx.Principal, uuid.UUID, bool) {
	p, _ := httpx.MustPrincipal(r)
	for _, k := range append([]string{"azure_subscriptions"}, entities...) {
		if !p.CanAccessGroup(h.svc.entity(k).Group) {
			httpx.Fail(w, httpx.ErrForbidden)
			return p, uuid.Nil, false
		}
	}
	var id uuid.UUID
	if raw := r.PathValue("id"); raw != "" {
		var err error
		if id, err = uuid.Parse(raw); err != nil {
			httpx.Fail(w, httpx.ErrNotFound)
			return p, uuid.Nil, false
		}
	}
	return p, id, true
}

// GET /api/v1/azure/tenants/{id}/credential
func (h *Handlers) Credential(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r)
	if !ok {
		return
	}
	info, err := h.svc.CredentialInfo(r.Context(), p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, info)
}

// PUT /api/v1/azure/tenants/{id}/credential  {client_secret, expires_on?} (admin)
func (h *Handlers) SetCredential(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r)
	if !ok {
		return
	}
	var in struct {
		ClientSecret string `json:"client_secret"`
		ExpiresOn    string `json:"expires_on"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(in.ClientSecret) == "" {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "client_secret is required"))
		return
	}
	if in.ExpiresOn != "" {
		if _, err := parseDay(in.ExpiresOn); err != nil {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "expires_on must be YYYY-MM-DD"))
			return
		}
	}
	if _, err := h.svc.repo.Get(r.Context(), h.svc.entity("azure_tenants"), p.TenantID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.svc.SetSecret(r.Context(), p.TenantID, id, strings.TrimSpace(in.ClientSecret), in.ExpiresOn); err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Credential(w, r)
}

// POST /api/v1/azure/tenants/{id}/test
func (h *Handlers) Test(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r)
	if !ok {
		return
	}
	subs, err := h.svc.TestConnection(r.Context(), p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// POST /api/v1/azure/tenants/{id}/sync  {from?, to?, wait?}
// Without wait the sync runs in the background (status on the record).
func (h *Handlers) Sync(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r)
	if !ok {
		return
	}
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
		Wait bool   `json:"wait"`
	}
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &in); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	from, to := DefaultWindow()
	var err error
	if in.From != "" {
		if from, err = parseDay(in.From); err != nil {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "from must be YYYY-MM-DD"))
			return
		}
	}
	if in.To != "" {
		if to, err = parseDay(in.To); err != nil {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "to must be YYYY-MM-DD"))
			return
		}
	}
	if to.After(today()) {
		to = today()
	}
	if to.Before(from) || to.Sub(from) > 3*366*24*time.Hour {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "the sync window must run forward and span at most 3 years"))
		return
	}
	if _, err := h.svc.repo.Get(r.Context(), h.svc.entity("azure_tenants"), p.TenantID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Wait {
		res, err := h.svc.Sync(r.Context(), p.TenantID, id, from, to)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, res)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		_, _ = h.svc.Sync(ctx, p.TenantID, id, from, to)
	}()
	httpx.JSON(w, http.StatusAccepted, map[string]any{"status": "started", "from": fmtDay(from), "to": fmtDay(to)})
}

// POST /api/v1/azure/agreements  (AgreementRequest)
func (h *Handlers) CreateAgreement(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.guard(w, r, "invoices")
	if !ok {
		return
	}
	var in AgreementRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := h.svc.CreateAgreement(r.Context(), p.TenantID, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"entity": "invoices", "data": row})
}

// GET /api/v1/azure/agreements/{id}/quarters
func (h *Handlers) Quarters(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r, "invoices")
	if !ok {
		return
	}
	qs, through, err := h.svc.Quarters(r.Context(), p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"quarters": qs, "synced_through": through})
}

// POST /api/v1/azure/agreements/{id}/refresh  — re-price quarters not invoiced.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r, "invoices")
	if !ok {
		return
	}
	row, err := h.svc.Refresh(r.Context(), p.TenantID, id, "")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entity": "invoices", "data": row})
}

// POST /api/v1/azure/agreements/{id}/invoices  {period_start, force?}
func (h *Handlers) CreateInvoice(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.guard(w, r, "invoices", "sales_invoices")
	if !ok {
		return
	}
	var in struct {
		PeriodStart string `json:"period_start"`
		Force       bool   `json:"force"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	inv, warnings, err := h.svc.CreateInvoice(r.Context(), p.TenantID, id, in.PeriodStart, in.Force)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"entity": "sales_invoices", "data": inv, "warnings": warnings})
}

// GET /api/v1/azure/dashboard?from&to&account_id&subscription_id&azure_tenant_id&subscription_type&markup_pct
func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.guard(w, r)
	if !ok {
		return
	}
	qv := r.URL.Query()
	t := today()
	q := DashboardQuery{
		Scope: Scope{AccountID: qv.Get("account_id"), SubscriptionID: qv.Get("subscription_id"),
			AzureTenantID: qv.Get("azure_tenant_id"), Type: qv.Get("subscription_type")},
		From: time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(t.Year(), 12, 31, 0, 0, 0, 0, time.UTC),
	}
	if v, err := parseDay(qv.Get("from")); err == nil {
		q.From = v
	}
	if v, err := parseDay(qv.Get("to")); err == nil {
		q.To = v
	}
	if m, err := strconv.ParseFloat(qv.Get("markup_pct"), 64); err == nil {
		q.MarkupPct = m
	}
	if q.To.Before(q.From) || q.To.Sub(q.From) > 5*366*24*time.Hour {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "invalid date range"))
		return
	}
	d, err := h.svc.Dashboard(r.Context(), p.TenantID, q)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// POST /api/v1/azure/import  (multipart: file, mode=preview|apply,
// azure_tenant_id, overwrite=["<key>",…] JSON, overwrite_all=true)
// Imports a Cost Management CSV download. preview compares it with the stored
// usage; apply writes new days and replaces only the changed days chosen.
func (h *Handlers) Import(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.guard(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_form", "expected a multipart form with a CSV file (max 50 MB)"))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "no_file", "a CSV file is required"))
		return
	}
	defer file.Close()

	if r.FormValue("mode") != "apply" {
		pv, err := h.svc.PreviewImport(r.Context(), p.TenantID, file)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, pv)
		return
	}
	if !p.CanWrite() {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	azID, err := uuid.Parse(r.FormValue("azure_tenant_id"))
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "pick the Azure tenant these subscriptions belong to"))
		return
	}
	overwrite := map[string]bool{}
	if raw := r.FormValue("overwrite"); raw != "" {
		var keys []string
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "validation", "overwrite must be a JSON list of change keys"))
			return
		}
		for _, k := range keys {
			overwrite[k] = true
		}
	}
	res, err := h.svc.ApplyImport(r.Context(), p.TenantID, azID, file, overwrite, r.FormValue("overwrite_all") == "true")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}
