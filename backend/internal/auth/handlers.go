package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

type Handlers struct {
	store      *Store
	issuer     *TokenIssuer
	bcryptCost int
	groups     []string // catalog section names (for the permissions matrix)
}

func NewHandlers(store *Store, issuer *TokenIssuer, bcryptCost int, groups []string) *Handlers {
	return &Handlers{store: store, issuer: issuer, bcryptCost: bcryptCost, groups: groups}
}

type authResponse struct {
	Token       string       `json:"token"`
	User        User         `json:"user"`
	Tenant      Tenant       `json:"tenant"`
	Role        string       `json:"role"`
	Memberships []Membership `json:"memberships"`
}

// POST /api/v1/auth/signup  -> creates a user + a new org, returns a token.
func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		FullName     string `json:"full_name"`
		Organization string `json:"organization"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validateCredentials(req.Email, req.Password); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.Organization) == "" {
		req.Organization = "My Organization"
	}
	hash, err := HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	u, t, err := h.store.CreateUserWithTenant(r.Context(), req.Email, req.FullName, hash, req.Organization)
	if errors.Is(err, ErrEmailTaken) {
		httpx.Fail(w, httpx.Errorf(http.StatusConflict, "email_taken", "an account with this email already exists"))
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.respondAuth(w, u, t, "owner")
}

// POST /api/v1/auth/login
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string    `json:"email"`
		Password string    `json:"password"`
		TenantID uuid.UUID `json:"tenant_id"` // optional: pick which org to log into
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := h.store.UserByEmail(r.Context(), req.Email)
	if err != nil || !CheckPassword(u.PasswordHash, req.Password) {
		httpx.Fail(w, httpx.Errorf(http.StatusUnauthorized, "invalid_credentials", "invalid email or password"))
		return
	}
	memberships, err := h.store.Memberships(r.Context(), u.ID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(memberships) == 0 {
		httpx.Fail(w, httpx.Errorf(http.StatusForbidden, "no_org", "this account is not a member of any organization"))
		return
	}
	chosen := memberships[0]
	if req.TenantID != uuid.Nil {
		found := false
		for _, m := range memberships {
			if m.TenantID == req.TenantID {
				chosen, found = m, true
				break
			}
		}
		if !found {
			httpx.Fail(w, httpx.ErrForbidden)
			return
		}
	}
	token, err := h.issuer.Issue(u.ID, u.Email, chosen.TenantID, chosen.Role)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, authResponse{
		Token:       token,
		User:        u,
		Tenant:      Tenant{ID: chosen.TenantID, Name: chosen.Tenant, Slug: chosen.Slug},
		Role:        chosen.Role,
		Memberships: memberships,
	})
}

// GET /api/v1/auth/me
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	p, err := httpx.MustPrincipal(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := h.store.UserByID(r.Context(), p.UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	memberships, err := h.store.Memberships(r.Context(), p.UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var current Tenant
	for _, m := range memberships {
		if m.TenantID == p.TenantID {
			current = Tenant{ID: m.TenantID, Name: m.Tenant, Slug: m.Slug}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user": u, "tenant": current, "role": p.Role, "memberships": memberships,
	})
}

// POST /api/v1/auth/switch-tenant  -> reissue token for another org.
func (h *Handlers) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	p, err := httpx.MustPrincipal(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		TenantID uuid.UUID `json:"tenant_id"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	role, _, ok, err := h.store.MembershipFor(r.Context(), p.UserID, req.TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		httpx.Fail(w, httpx.ErrForbidden)
		return
	}
	u, err := h.store.UserByID(r.Context(), p.UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	token, err := h.issuer.Issue(p.UserID, p.Email, req.TenantID, role)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.respondToken(w, token, u, req.TenantID, role)
}

// POST /api/v1/team/invites  (admin)
func (h *Handlers) CreateInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !validRole(req.Role) {
		req.Role = "member"
	}
	if !strings.Contains(req.Email, "@") {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_email", "a valid email is required"))
		return
	}
	inv, err := h.store.CreateInvite(r.Context(), p.TenantID, p.UserID, req.Email, req.Role)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, inv)
}

// GET /api/v1/team/invites (admin)
func (h *Handlers) ListInvites(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	invs, err := h.store.ListInvites(r.Context(), p.TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": invs})
}

// GET /api/v1/team/members (admin)
func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	members, err := h.store.Members(r.Context(), p.TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": members})
}

// GET /api/v1/team/permissions (admin) -> sections + each member's access.
func (h *Handlers) GetPermissions(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	members, err := h.store.MembersWithPerms(r.Context(), p.TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": h.groups, "members": members})
}

// PUT /api/v1/team/permissions (admin) -> set a member's allowed sections.
// Body: {"user_id": "...", "groups": ["CRM","ERP"]}  (groups: null => full access)
func (h *Handlers) SetPermissions(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	var req struct {
		UserID uuid.UUID `json:"user_id"`
		Groups *[]string `json:"groups"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.UserID == uuid.Nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_user", "user_id is required"))
		return
	}
	var groups []string
	if req.Groups != nil {
		// Keep only known sections, preserving catalog order.
		known := map[string]bool{}
		for _, g := range *req.Groups {
			known[g] = true
		}
		groups = []string{}
		for _, g := range h.groups {
			if known[g] {
				groups = append(groups, g)
			}
		}
	}
	if err := h.store.SetMemberGroups(r.Context(), p.TenantID, req.UserID, groups); err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "set_failed", err.Error()))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/auth/accept-invite (public)
func (h *Handlers) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	var hash string
	if req.Password != "" {
		if len(req.Password) < 8 {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "weak_password", "password must be at least 8 characters"))
			return
		}
		var err error
		if hash, err = HashPassword(req.Password, h.bcryptCost); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	u, t, role, err := h.store.AcceptInvite(r.Context(), req.Token, req.FullName, hash)
	if err != nil {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "invite_failed", err.Error()))
		return
	}
	h.respondAuth(w, u, t, role)
}

func (h *Handlers) respondAuth(w http.ResponseWriter, u User, t Tenant, role string) {
	token, err := h.issuer.Issue(u.ID, u.Email, t.ID, role)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, authResponse{Token: token, User: u, Tenant: t, Role: role})
}

func (h *Handlers) respondToken(w http.ResponseWriter, token string, u User, tid uuid.UUID, role string) {
	httpx.JSON(w, http.StatusOK, authResponse{Token: token, User: u, Tenant: Tenant{ID: tid}, Role: role})
}

func validateCredentials(email, pw string) error {
	if !strings.Contains(email, "@") {
		return httpx.Errorf(http.StatusBadRequest, "bad_email", "a valid email is required")
	}
	if len(pw) < 8 {
		return httpx.Errorf(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	return nil
}

func validRole(r string) bool {
	switch r {
	case "owner", "admin", "member", "viewer":
		return true
	}
	return false
}
