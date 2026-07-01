// Package server wires configuration, storage and handlers into an http.Handler.
package server

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/auth"
	"hrcrm/internal/cache"
	"hrcrm/internal/config"
	"hrcrm/internal/documents"
	"hrcrm/internal/httpx"
	"hrcrm/internal/resource"
)

type Server struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	cache *cache.Cache
	reg   *resource.Registry
}

// New builds the registry, ensures the schema, and returns a Server.
func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, c *cache.Cache) (*Server, error) {
	reg, err := resource.NewRegistry(resource.Catalog()...)
	if err != nil {
		return nil, err
	}
	if err := resource.EnsureSchema(ctx, pool, reg); err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, pool: pool, cache: c, reg: reg}, nil
}

// Handler returns the fully-wired HTTP handler.
func (s *Server) Handler() http.Handler {
	issuer := auth.NewTokenIssuer(s.cfg.JWTSecret, s.cfg.JWTTTL)
	authStore := auth.NewStore(s.pool)
	authH := auth.NewHandlers(authStore, issuer, s.cfg.BcryptCost, s.reg.Groups())
	authMW := auth.NewMiddleware(issuer, authStore)

	repo := resource.NewRepo(s.pool)
	docSvc := resource.NewDocService(s.reg, repo)
	resH := resource.NewHandlers(s.reg, repo, docSvc)

	tmplStore := documents.NewTemplateStore(s.pool)
	pdfSvc := documents.NewService(s.reg, repo, s.cfg.TemplatesDir, tmplStore)
	docH := documents.NewHandlers(pdfSvc, s.pool, s.cfg.SofficeBin)
	tmplH := documents.NewTemplateHandlers(tmplStore)

	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)

	// Public auth.
	mux.HandleFunc("POST /api/v1/auth/signup", authH.Signup)
	mux.HandleFunc("POST /api/v1/auth/login", authH.Login)
	mux.HandleFunc("POST /api/v1/auth/accept-invite", authH.AcceptInvite)

	// Authenticated helpers.
	protected := func(h http.HandlerFunc) http.Handler { return authMW.Require(h) }
	write := func(h http.HandlerFunc) http.Handler { return authMW.Require(auth.RequireWrite(http.HandlerFunc(h))) }
	admin := func(h http.HandlerFunc) http.Handler { return authMW.Require(auth.RequireAdmin(http.HandlerFunc(h))) }

	mux.Handle("GET /api/v1/auth/me", protected(authH.Me))
	mux.Handle("POST /api/v1/auth/switch-tenant", protected(authH.SwitchTenant))

	// Team administration.
	mux.Handle("GET /api/v1/team/members", admin(authH.ListMembers))
	mux.Handle("GET /api/v1/team/invites", admin(authH.ListInvites))
	mux.Handle("POST /api/v1/team/invites", admin(authH.CreateInvite))
	mux.Handle("GET /api/v1/team/permissions", admin(authH.GetPermissions))
	mux.Handle("PUT /api/v1/team/permissions", admin(authH.SetPermissions))

	// Metadata + dashboard.
	mux.Handle("GET /api/v1/meta", protected(resH.Meta))
	mux.Handle("GET /api/v1/dashboard", protected(resH.Dashboard))

	// Document entities (header + line items): quotes, sales orders, invoices,
	// purchase orders, vendor bills.
	mux.Handle("GET /api/v1/docs/{entity}/{id}", protected(resH.DocGet))
	mux.Handle("POST /api/v1/docs/{entity}", write(resH.DocCreate))
	mux.Handle("PUT /api/v1/docs/{entity}/{id}", write(resH.DocUpdate))
	mux.Handle("POST /api/v1/docs/{entity}/{id}/convert", write(resH.DocConvert))

	// Generic entity CRUD.
	mux.Handle("GET /api/v1/entities/{entity}", protected(resH.List))
	mux.Handle("POST /api/v1/entities/{entity}", write(resH.Create))
	mux.Handle("GET /api/v1/entities/{entity}/{id}", protected(resH.Get))
	mux.Handle("PUT /api/v1/entities/{entity}/{id}", write(resH.Update))
	mux.Handle("DELETE /api/v1/entities/{entity}/{id}", write(resH.Delete))

	// Documents.
	mux.Handle("GET /api/v1/documents/{kind}/{invoice_id}", protected(docH.Generate))

	// Uploadable Offer/IGTR templates (list for all; upload/delete admin-only).
	mux.Handle("GET /api/v1/templates", protected(tmplH.List))
	mux.Handle("POST /api/v1/templates", admin(tmplH.Upload))
	mux.Handle("DELETE /api/v1/templates/{id}", admin(tmplH.Delete))

	return httpx.Chain(mux,
		httpx.Recover,
		httpx.Logger,
		httpx.CORS(s.cfg.CORSOrigin),
	)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_down"})
		return
	}
	if err := s.cache.Ping(r.Context()); err != nil {
		httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "cache_down"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
