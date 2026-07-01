package auth

import (
	"net/http"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// Middleware validates the bearer token and injects the principal.
type Middleware struct {
	issuer *TokenIssuer
	store  *Store
}

func NewMiddleware(issuer *TokenIssuer, store *Store) *Middleware {
	return &Middleware{issuer: issuer, store: store}
}

// Require enforces a valid token. It also re-checks membership on each request
// so revoked access takes effect immediately (defense for app-enforced tenancy).
func (m *Middleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := httpx.BearerToken(r)
		if tok == "" {
			httpx.Fail(w, httpx.ErrUnauthorized)
			return
		}
		claims, err := m.issuer.Parse(tok)
		if err != nil {
			httpx.Fail(w, httpx.ErrUnauthorized)
			return
		}
		uid, err1 := uuid.Parse(claims.UserID)
		tid, err2 := uuid.Parse(claims.TenantID)
		if err1 != nil || err2 != nil {
			httpx.Fail(w, httpx.ErrUnauthorized)
			return
		}
		role, groups, ok, err := m.store.MembershipFor(r.Context(), uid, tid)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !ok {
			httpx.Fail(w, httpx.ErrForbidden)
			return
		}
		p := httpx.Principal{UserID: uid, Email: claims.Email, TenantID: tid, Role: role, AllowedGroups: groups}
		next.ServeHTTP(w, r.WithContext(httpx.WithPrincipal(r.Context(), p)))
	})
}

// RequireWrite rejects read-only (viewer) roles for mutating endpoints.
func RequireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := httpx.MustPrincipal(r)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !p.CanWrite() {
			httpx.Fail(w, httpx.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin restricts team/admin endpoints to owner/admin roles.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := httpx.MustPrincipal(r)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !p.IsAdmin() {
			httpx.Fail(w, httpx.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
