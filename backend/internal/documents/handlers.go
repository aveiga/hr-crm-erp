package documents

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
)

type Handlers struct {
	svc        *Service
	pool       *pgxpool.Pool
	sofficeBin string
}

func NewHandlers(svc *Service, pool *pgxpool.Pool, sofficeBin string) *Handlers {
	return &Handlers{svc: svc, pool: pool, sofficeBin: sofficeBin}
}

// GET /api/v1/documents/{kind}/{invoice_id}?format=pdf|docx
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
	kind := r.PathValue("kind")
	if kind != "offer" && kind != "igtr" {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_kind", "kind must be 'offer' or 'igtr'"))
		return
	}
	invoiceID, err := uuid.Parse(r.PathValue("invoice_id"))
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_id", "invalid invoice id"))
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
		content, filename, err = h.svc.BuildDocx(r.Context(), kind, p.TenantID, invoiceID, templateID)
		mime = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	} else {
		if !SofficeAvailable(h.sofficeBin) {
			httpx.Fail(w, httpx.Errorf(http.StatusServiceUnavailable, "no_libreoffice",
				"PDF conversion requires LibreOffice; request ?format=docx instead"))
			return
		}
		content, filename, err = h.svc.BuildPDF(r.Context(), h.sofficeBin, kind, p.TenantID, invoiceID, templateID)
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
		p.TenantID, kind, invoiceID, filename, mime, content, p.UserID)

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
