package auth

import (
	"net/http"
	"strings"

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

// Require enforces a valid token — either a login JWT or a per-agent API token
// (distinguished by the APITokenPrefix). Either way it re-checks membership on
// each request so revoked access takes effect immediately (defense for
// app-enforced tenancy), and binds the resulting Principal to the request.
func (m *Middleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := httpx.BearerToken(r)
		if tok == "" {
			httpx.Fail(w, httpx.ErrUnauthorized)
			return
		}

		var (
			uid, tid uuid.UUID
			email    string
			viaAPI   bool
			readOnly bool
		)
		if strings.HasPrefix(tok, APITokenPrefix) {
			u, t, e, ro, ok, err := m.store.ResolveAPIToken(r.Context(), HashAPIToken(tok))
			if err != nil {
				httpx.Fail(w, err)
				return
			}
			if !ok {
				httpx.Fail(w, httpx.ErrUnauthorized)
				return
			}
			uid, tid, email, readOnly, viaAPI = u, t, e, ro, true
		} else {
			claims, err := m.issuer.Parse(tok)
			if err != nil {
				httpx.Fail(w, httpx.ErrUnauthorized)
				return
			}
			u, err1 := uuid.Parse(claims.UserID)
			t, err2 := uuid.Parse(claims.TenantID)
			if err1 != nil || err2 != nil {
				httpx.Fail(w, httpx.ErrUnauthorized)
				return
			}
			uid, tid, email = u, t, claims.Email
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
		// A read-only token is capped to viewer access regardless of the member's
		// real role, so an agent can be given reads without write ability.
		if readOnly {
			role = "viewer"
		}
		p := httpx.Principal{UserID: uid, Email: email, TenantID: tid, Role: role, AllowedGroups: groups, IsAPIToken: viaAPI}
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
