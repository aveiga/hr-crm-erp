package documents

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
	"hrcrm/internal/resource"
)

type Handlers struct {
	svc        *Service
	reg        *resource.Registry
	pool       *pgxpool.Pool
	sofficeBin string
}

func NewHandlers(svc *Service, reg *resource.Registry, pool *pgxpool.Pool, sofficeBin string) *Handlers {
	return &Handlers{svc: svc, reg: reg, pool: pool, sofficeBin: sofficeBin}
}

// GET /api/v1/documents/{kind}/{entity}/{id}?format=pdf|docx&template_id=
// Generates from a quote (Offers) or a service agreement; the caller needs
// access to that entity's section.
func (h *Handlers) GenerateFrom(w http.ResponseWriter, r *http.Request) {
	p, err := httpx.MustPrincipal(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	e, ok := h.reg.Get(r.PathValue("entity"))
	if !ok || !Sources[e.Key] {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_source", "documents can be generated from quotes or service agreements"))
		return
	}
	if !p.CanAccessGroup(e.Group) {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	h.generate(w, r, p, e.Key, r.PathValue("id"))
}

// GET /api/v1/documents/{kind}/{invoice_id}?format=pdf|docx
// The original route: generates from a service agreement (entity "invoices").
func (h *Handlers) Generate(w http.ResponseWriter, r *http.Request) {
	p, err := httpx.MustPrincipal(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// Offers/IGTRs are generated from invoices, which live in the ERP section.
	if !p.CanAccessGroup("ERP") {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	h.generate(w, r, p, "invoices", r.PathValue("invoice_id"))
}

func (h *Handlers) generate(w http.ResponseWriter, r *http.Request, p httpx.Principal, entityKey, rawID string) {
	kind := r.PathValue("kind")
	if kind != "offer" && kind != "igtr" {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_kind", "kind must be 'offer' or 'igtr'"))
		return
	}
	sourceID, err := uuid.Parse(rawID)
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_id", "invalid document id"))
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "pdf"
	}
	templateID := r.URL.Query().Get("template_id")

	var content []byte
	var filename, mime string
	if format == "docx" {
		content, filename, err = h.svc.Build(r.Context(), kind, entityKey, p.TenantID, sourceID, templateID)
		mime = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	} else {
		if !SofficeAvailable(h.sofficeBin) {
			httpx.Fail(w, httpx.Errorf(http.StatusServiceUnavailable, "no_libreoffice",
				"PDF conversion requires LibreOffice; request ?format=docx instead"))
			return
		}
		content, filename, err = h.svc.BuildPDFFrom(r.Context(), h.sofficeBin, kind, entityKey, p.TenantID, sourceID, templateID)
		mime = "application/pdf"
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}

	// Persist a copy for audit/history (best effort).
	_, _ = h.pool.Exec(r.Context(),
		`INSERT INTO documents(tenant_id, kind, source_id, filename, mime, content, created_by)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		p.TenantID, kind, sourceID, filename, mime, content, p.UserID)

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
