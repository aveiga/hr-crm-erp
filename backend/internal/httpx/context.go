package httpx

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// Principal is the authenticated caller plus their active tenant context.
type Principal struct {
	UserID   uuid.UUID
	Email    string
	TenantID uuid.UUID
	Role     string
	// AllowedGroups restricts which sections the member may access. nil means
	// full access (no restriction). Ignored for owners/admins.
	AllowedGroups []string
}

func (p Principal) IsAdmin() bool { return p.Role == "owner" || p.Role == "admin" }
func (p Principal) CanWrite() bool {
	return p.Role == "owner" || p.Role == "admin" || p.Role == "member"
}

// CanAccessGroup reports whether the principal may access a section (group).
func (p Principal) CanAccessGroup(group string) bool {
	if p.IsAdmin() || p.AllowedGroups == nil {
		return true
	}
	for _, g := range p.AllowedGroups {
		if g == group {
			return true
		}
	}
	return false
}

type ctxKey int

const principalKey ctxKey = iota

// WithPrincipal stores the principal on the request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the principal, or ok=false if the request is anonymous.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// MustPrincipal returns the principal or an unauthorized error.
func MustPrincipal(r *http.Request) (Principal, error) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	return p, nil
}
