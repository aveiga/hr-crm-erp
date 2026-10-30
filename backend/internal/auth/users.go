package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// User-manager handlers (admin): members, invite lifecycle and password reset
// links. Every operation is scoped to the caller's tenant; the owner,
// last-owner and cross-tenant identity guards are enforced in the store inside
// the same transaction as the change.

const (
	// InviteTTL is how long a new or resent invite stays valid.
	InviteTTL = 14 * 24 * time.Hour
	// PasswordResetTTL is how long a reset link stays valid.
	PasswordResetTTL = 24 * time.Hour
)

// cleanGroups keeps only known sections, preserving catalog order. nil means
// full access and is passed through.
func (h *Handlers) cleanGroups(in *[]string) []string {
	if in == nil {
		return nil
	}
	known := map[string]bool{}
	for _, g := range *in {
		known[g] = true
	}
	groups := []string{}
	for _, g := range h.groups {
		if known[g] {
			groups = append(groups, g)
		}
	}
	return groups
}

// sectionGroups is cleanGroups for a given role: owners and admins always have
// full access, so their restriction is stored as NULL.
func (h *Handlers) sectionGroups(role string, in *[]string) []string {
	if role == "owner" || role == "admin" {
		return nil
	}
	return h.cleanGroups(in)
}

// memberError maps user-manager store errors to API errors.
func memberError(err error) error {
	switch {
	case errors.Is(err, ErrMemberNotFound), errors.Is(err, ErrInviteNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, ErrOwnerOnly):
		return httpx.Errorf(http.StatusForbidden, "owner_only", ErrOwnerOnly.Error())
	case errors.Is(err, ErrIdentityShared):
		return httpx.Errorf(http.StatusForbidden, "identity_shared", ErrIdentityShared.Error())
	case errors.Is(err, ErrLastOwner):
		return httpx.Errorf(http.StatusConflict, "last_owner", ErrLastOwner.Error())
	case errors.Is(err, ErrRemoveSelf):
		return httpx.Errorf(http.StatusBadRequest, "remove_self", ErrRemoveSelf.Error())
	case errors.Is(err, ErrInviteNotPending):
		return httpx.Errorf(http.StatusConflict, "invite_not_pending", ErrInviteNotPending.Error())
	case errors.Is(err, ErrEmailTaken):
		return httpx.Errorf(http.StatusConflict, "email_taken", "an account with this email already exists")
	case errors.Is(err, ErrResetInvalid):
		return httpx.Errorf(http.StatusBadRequest, "invalid_token", ErrResetInvalid.Error())
	}
	return err
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

// GET /api/v1/team/users (admin) -> sections + every member with details.
func (h *Handlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	members, err := h.store.MemberDetails(r.Context(), p.TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": h.groups, "data": members})
}

// PUT /api/v1/team/users/{id} (admin) -> partial update of a member.
// Body (all optional): {"full_name","email","role","allowed_groups"}; an
// explicit "allowed_groups": null grants full access, omitting it leaves
// access unchanged.
func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		FullName      *string         `json:"full_name"`
		Email         *string         `json:"email"`
		Role          *string         `json:"role"`
		AllowedGroups json.RawMessage `json:"allowed_groups"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	upd := MemberUpdate{FullName: req.FullName, Email: req.Email, Role: req.Role}
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		upd.FullName = &name
	}
	if req.Email != nil && !strings.Contains(*req.Email, "@") {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_email", "a valid email is required"))
		return
	}
	if req.Role != nil && !validRole(*req.Role) {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_role", "role must be owner, admin, member or viewer"))
		return
	}
	if len(req.AllowedGroups) > 0 {
		var groups *[]string
		if err := json.Unmarshal(req.AllowedGroups, &groups); err != nil {
			httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_request", "allowed_groups must be an array of section names or null"))
			return
		}
		upd.SetGroups = true
		upd.Groups = h.cleanGroups(groups)
	}
	if err := h.store.UpdateMember(r.Context(), p.TenantID, id, p.Role == "owner", upd); err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	d, err := h.store.MemberDetailByID(r.Context(), p.TenantID, id)
	if err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": d})
}

// DELETE /api/v1/team/users/{id} (admin) -> remove a member from the tenant.
func (h *Handlers) RemoveUser(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.store.RemoveMember(r.Context(), p.TenantID, p.UserID, id, p.Role == "owner"); err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/team/users/{id}/password-reset (admin) -> one-time reset link.
// There is no email delivery: the admin shares the returned link. Only the
// SHA-256 of the token is stored.
func (h *Handlers) CreatePasswordReset(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	// Minting a credential for someone else requires an interactive session,
	// like token management.
	if p.IsAPIToken {
		httpx.Fail(w, httpx.Errorf(http.StatusForbidden, "forbidden",
			"API tokens cannot issue password reset links; sign in interactively"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	token := RandomToken()
	expiresAt := time.Now().Add(PasswordResetTTL)
	if err := h.store.CreatePasswordReset(r.Context(), p.TenantID, p.UserID, id, p.Role == "owner",
		HashAPIToken(token), expiresAt); err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"user_id":    id,
		"token":      token, // shown once
		"path":       "/reset-password?token=" + url.QueryEscape(token),
		"expires_at": expiresAt,
	})
}

// POST /api/v1/auth/reset-password (public) {token, password}.
func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(req.Password) < 8 {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "weak_password", "password must be at least 8 characters"))
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		httpx.Fail(w, memberError(ErrResetInvalid))
		return
	}
	hash, err := HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.store.ResetPassword(r.Context(), HashAPIToken(req.Token), hash); err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/v1/team/invites/{id} (admin) {role, allowed_groups} -> edit a
// pending invite. allowed_groups null/omitted => full access.
func (h *Handlers) UpdateInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Role          string    `json:"role"`
		AllowedGroups *[]string `json:"allowed_groups"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !validRole(req.Role) {
		httpx.Fail(w, httpx.Errorf(http.StatusBadRequest, "bad_role", "role must be owner, admin, member or viewer"))
		return
	}
	inv, err := h.store.UpdateInvite(r.Context(), p.TenantID, id, p.Role == "owner", req.Role,
		h.sectionGroups(req.Role, req.AllowedGroups))
	if err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}

// POST /api/v1/team/invites/{id}/revoke (admin).
func (h *Handlers) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	inv, err := h.store.RevokeInvite(r.Context(), p.TenantID, id, p.Role == "owner")
	if err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}

// POST /api/v1/team/invites/{id}/resend (admin) -> new token + fresh expiry.
func (h *Handlers) ResendInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	inv, err := h.store.ResendInvite(r.Context(), p.TenantID, id, p.Role == "owner", time.Now().Add(InviteTTL))
	if err != nil {
		httpx.Fail(w, memberError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}
