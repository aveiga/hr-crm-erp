package resource

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

type Handlers struct {
	reg  *Registry
	repo *Repo
	docs *DocService
}

func NewHandlers(reg *Registry, repo *Repo, docs *DocService) *Handlers {
	return &Handlers{reg: reg, repo: repo, docs: docs}
}

func (h *Handlers) entity(r *http.Request) (Entity, bool) {
	return h.reg.Get(r.PathValue("entity"))
}

// resolve fetches the target entity and enforces section (group) access for the
// caller. It writes the error response and returns ok=false when access is
// denied or the entity is unknown.
func (h *Handlers) resolve(w http.ResponseWriter, r *http.Request) (Entity, httpx.Principal, bool) {
	p, _ := httpx.MustPrincipal(r)
	e, ok := h.entity(r)
	if !ok {
		httpx.Fail(w, httpx.ErrNotFound)
		return Entity{}, p, false
	}
	if !p.CanAccessGroup(e.Group) {
		httpx.Fail(w, httpx.ErrForbidden)
		return Entity{}, p, false
	}
	return e, p, true
}

// GET /api/v1/meta -> the catalog driving the UI, filtered to the sections the
// caller is allowed to access so restricted areas disappear from the app.
func (h *Handlers) Meta(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	entities := []Entity{}
	for _, e := range h.reg.All() {
		if p.CanAccessGroup(e.Group) {
			entities = append(entities, e)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entities": entities})
}

// GET /api/v1/{entity}
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	opt := ListOptions{
		Page:     atoiDefault(q.Get("page"), 1),
		PageSize: atoiDefault(q.Get("page_size"), 50),
		Search:   q.Get("q"),
		Sort:     q.Get("sort"),
		Desc:     q.Get("dir") == "desc",
		Filters:  map[string]string{},
	}
	ParseFilters(e, q, &opt)
	res, err := h.repo.List(r.Context(), e, p.TenantID, opt)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GET /api/v1/{entity}/{id}
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	row, err := h.repo.Get(r.Context(), e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": row})
}

// readOnlyErr is returned when a write is attempted on a read-only entity.
var readOnlyErr = httpx.Errorf(http.StatusForbidden, "read_only", "this list is read-only; edit these records via their parent")

// POST /api/v1/{entity}
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if e.ReadOnly {
		httpx.Fail(w, readOnlyErr)
		return
	}
	var input map[string]any
	if err := httpx.DecodeJSON(r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := h.repo.Create(r.Context(), e, p.TenantID, input)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"data": row})
}

// PUT /api/v1/{entity}/{id}
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if e.ReadOnly {
		httpx.Fail(w, readOnlyErr)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	var input map[string]any
	if err := httpx.DecodeJSON(r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := h.repo.Update(r.Context(), e, p.TenantID, id, input)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": row})
}

// DELETE /api/v1/{entity}/{id}
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if e.ReadOnly {
		httpx.Fail(w, readOnlyErr)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	if err := h.repo.Delete(r.Context(), e, p.TenantID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// splitLines pulls the "lines" array out of a document payload, returning the
// header map and the line items.
func splitLines(body map[string]any) (header map[string]any, lines []map[string]any) {
	header = map[string]any{}
	for k, v := range body {
		if k == "lines" {
			continue
		}
		header[k] = v
	}
	return header, rowsOf(body["lines"])
}

// splitDoc is splitLines plus the document's secondary child collections, sent
// as arrays keyed by child entity (e.g. "invoice_schedule": [...]). Only the
// collections present in the body are returned, so omitting one keeps it.
func splitDoc(e Entity, body map[string]any) (header map[string]any, lines []map[string]any, extra map[string][]map[string]any) {
	header, lines = splitLines(body)
	for _, c := range e.Children[1:] {
		if raw, ok := body[c.Entity]; ok {
			if extra == nil {
				extra = map[string][]map[string]any{}
			}
			extra[c.Entity] = rowsOf(raw)
			delete(header, c.Entity)
		}
	}
	return header, lines, extra
}

func rowsOf(raw any) []map[string]any {
	var rows []map[string]any
	if arr, ok := raw.([]any); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
	}
	if raw != nil && rows == nil {
		rows = []map[string]any{} // explicit empty array clears the collection
	}
	return rows
}

// POST /api/v1/docs/{entity}  -> create a document (header + lines).
func (h *Handlers) DocCreate(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	e, ok := h.entity(r)
	if !ok || !e.IsDocument() {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	if !p.CanAccessGroup(e.Group) {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	var body map[string]any
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	header, lines, extra := splitDoc(e, body)
	row, err := h.docs.SaveDoc(r.Context(), e, p.TenantID, nil, header, lines, extra)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"data": row})
}

// PUT /api/v1/docs/{entity}/{id}  -> update a document (header + lines).
func (h *Handlers) DocUpdate(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	e, ok := h.entity(r)
	if !ok || !e.IsDocument() {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	if !p.CanAccessGroup(e.Group) {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	var body map[string]any
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	header, lines, extra := splitDoc(e, body)
	row, err := h.docs.SaveDoc(r.Context(), e, p.TenantID, &id, header, lines, extra)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": row})
}

// GET /api/v1/docs/{entity}/{id}  -> document header + line items.
func (h *Handlers) DocGet(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	e, ok := h.entity(r)
	if !ok || !e.IsDocument() {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	if !p.CanAccessGroup(e.Group) {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	row, err := h.repo.Get(r.Context(), e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	lines, err := h.docs.LoadLines(r.Context(), e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out := map[string]any{"data": row, "lines": lines}
	children, err := h.docs.LoadChildren(r.Context(), e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if children != nil {
		out["children"] = children
	}
	httpx.JSON(w, http.StatusOK, out)
}

// POST /api/v1/docs/{entity}/{id}/duplicate  -> copy a document (header, lines
// and other child collections) into a new draft.
func (h *Handlers) DocDuplicate(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	row, err := h.docs.Duplicate(r.Context(), e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"entity": e.Key, "data": row})
}

// GET /api/v1/entities/{entity}/{id}/related  -> upstream links and downstream
// records (with statuses) for navigating the document chain both ways.
func (h *Handlers) Related(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	up, down, err := h.docs.Related(r.Context(), p, e, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"upstream": up, "downstream": down})
}

// POST /api/v1/docs/{entity}/{id}/convert  -> advance a record to the next
// pipeline stage (lead→opportunity, opportunity→quote, quote→order→invoice).
func (h *Handlers) DocConvert(w http.ResponseWriter, r *http.Request) {
	e, p, ok := h.resolve(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	toKey, row, err := h.docs.Convert(r.Context(), p, e.Key, p.TenantID, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"entity": toKey, "data": row})
}

// GET /api/v1/dashboard -> headline counts and AR/AP figures.
func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	ctx := r.Context()
	counts := map[string]int{}
	for _, e := range h.reg.All() {
		if !p.CanAccessGroup(e.Group) {
			continue
		}
		n, err := h.repo.Count(ctx, e, p.TenantID)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		counts[e.Key] = n
	}

	out := map[string]any{"counts": counts}

	if inv, ok := h.reg.Get("invoices"); ok && p.CanAccessGroup(inv.Group) {
		arOutstanding, _ := h.repo.SumMoney(ctx, inv, p.TenantID, "balance", "", "")
		arInvoiced, _ := h.repo.SumMoney(ctx, inv, p.TenantID, "grand_total", "", "")
		arOverdue, _ := h.repo.SumMoney(ctx, inv, p.TenantID, "balance", "status", "Overdue")
		out["ar"] = map[string]float64{
			"invoiced": arInvoiced, "outstanding": arOutstanding, "overdue": arOverdue,
		}
	}
	if bills, ok := h.reg.Get("vendor_bills"); ok && p.CanAccessGroup(bills.Group) {
		apOutstanding, _ := h.repo.SumMoney(ctx, bills, p.TenantID, "balance", "", "")
		out["ap"] = map[string]float64{"outstanding": apOutstanding}
	}
	if opps, ok := h.reg.Get("opportunities"); ok && p.CanAccessGroup(opps.Group) {
		pipeline, _ := h.repo.SumMoney(ctx, opps, p.TenantID, "weighted_amount", "", "")
		out["pipeline_weighted"] = pipeline
	}
	httpx.JSON(w, http.StatusOK, out)
}

// ParseFilters reads the list filter params: f_<field>=v (equality, or
// @empty), fin_<field>=a,b (any of), fmin_/fmax_<field>=v (inclusive range).
func ParseFilters(e Entity, q url.Values, opt *ListOptions) {
	opt.In, opt.Min, opt.Max = map[string][]string{}, map[string]string{}, map[string]string{}
	for _, f := range e.Fields {
		if v := q.Get("f_" + f.Key); v != "" {
			opt.Filters[f.Key] = v
		}
		if v := q.Get("fin_" + f.Key); v != "" {
			opt.In[f.Key] = strings.Split(v, ",")
		}
		if v := q.Get("fmin_" + f.Key); v != "" {
			opt.Min[f.Key] = v
		}
		if v := q.Get("fmax_" + f.Key); v != "" {
			opt.Max[f.Key] = v
		}
	}
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
