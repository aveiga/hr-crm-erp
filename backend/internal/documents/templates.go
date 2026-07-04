package documents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
)

// TemplateStore persists per-tenant uploaded Word templates.
type TemplateStore struct{ pool *pgxpool.Pool }

func NewTemplateStore(pool *pgxpool.Pool) *TemplateStore { return &TemplateStore{pool: pool} }

type TemplateMeta struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Filename  string    `json:"filename"`
	CreatedAt string    `json:"created_at"`
}

func (s *TemplateStore) List(ctx context.Context, tenantID uuid.UUID, kind string) ([]TemplateMeta, error) {
	q := `SELECT id, kind, name, filename, created_at::text FROM doc_templates WHERE tenant_id=$1`
	args := []any{tenantID}
	if kind != "" {
		q += ` AND kind=$2`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TemplateMeta{}
	for rows.Next() {
		var t TemplateMeta
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.Filename, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *TemplateStore) Content(ctx context.Context, tenantID, id uuid.UUID, kind string) ([]byte, error) {
	var content []byte
	err := s.pool.QueryRow(ctx,
		`SELECT content FROM doc_templates WHERE tenant_id=$1 AND id=$2 AND kind=$3`,
		tenantID, id, kind).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	return content, err
}

func (s *TemplateStore) Create(ctx context.Context, tenantID, userID uuid.UUID, kind, name, filename string, content []byte) (TemplateMeta, error) {
	var t TemplateMeta
	t.Kind, t.Name, t.Filename = kind, name, filename
	err := s.pool.QueryRow(ctx,
		`INSERT INTO doc_templates(tenant_id, kind, name, filename, content, created_by)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING id, created_at::text`,
		tenantID, kind, name, filename, content, userID).Scan(&t.ID, &t.CreatedAt)
	return t, err
}

func (s *TemplateStore) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM doc_templates WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

// TemplateHandlers exposes template management endpoints.
type TemplateHandlers struct{ store *TemplateStore }

func NewTemplateHandlers(store *TemplateStore) *TemplateHandlers {
	return &TemplateHandlers{store: store}
}

// GET /api/v1/templates/fields -> the placeholders available to templates and
// their descriptions. Static (no tenant data), so any authenticated user can read it.
func (h *TemplateHandlers) Fields(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, FieldCatalog())
}

// GET /api/v1/templates?kind=offer|igtr
func (h *TemplateHandlers) List(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "offer" && kind != "igtr" {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_kind", "kind must be offer or igtr"))
		return
	}
	list, err := h.store.List(r.Context(), p.TenantID, kind)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": list})
}

// POST /api/v1/templates  (multipart: kind, name, file) — admin only.
func (h *TemplateHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_form", "expected multipart form with a file"))
		return
	}
	kind := r.FormValue("kind")
	if kind != "offer" && kind != "igtr" {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_kind", "kind must be offer or igtr"))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "no_file", "a .docx file is required"))
		return
	}
	defer file.Close()
	buf, err := io.ReadAll(file)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// A .docx is a zip; verify the magic bytes.
	if len(buf) < 2 || buf[0] != 'P' || buf[1] != 'K' {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "not_docx", "file must be a .docx Word document"))
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = hdr.Filename
	}
	meta, err := h.store.Create(r.Context(), p.TenantID, p.UserID, kind, name, hdr.Filename, buf)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, meta)
}

// DELETE /api/v1/templates/{id} — admin only.
func (h *TemplateHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, httpx.ErrNotFound)
		return
	}
	if err := h.store.Delete(r.Context(), p.TenantID, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
