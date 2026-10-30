package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// --- helpers ----------------------------------------------------------------

func myUserID(t *testing.T, token string) string {
	t.Helper()
	me := do(t, "GET", "/api/v1/auth/me", token, nil)
	u, _ := me.Body["user"].(map[string]any)
	id, _ := u["id"].(string)
	if id == "" {
		t.Fatalf("me returned no user id: %s", me.Raw)
	}
	return id
}

func newEmail(prefix string) string { return fmt.Sprintf("%s-%d@example.com", prefix, randInt()) }

// createInvite posts an invite and returns the decoded body.
func createInvite(t *testing.T, token string, body map[string]any) map[string]any {
	t.Helper()
	r := do(t, "POST", "/api/v1/team/invites", token, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("invite failed: %d %s", r.Status, r.Raw)
	}
	return r.Body
}

func acceptInvite(t *testing.T, inviteToken, password string) apiResp {
	t.Helper()
	body := map[string]any{"token": inviteToken, "full_name": "Invitee"}
	if password != "" {
		body["password"] = password
	}
	return do(t, "POST", "/api/v1/auth/accept-invite", "", body)
}

// findInvite returns the invite with the given id from GET /team/invites.
func findInvite(t *testing.T, token, id string) map[string]any {
	t.Helper()
	r := do(t, "GET", "/api/v1/team/invites", token, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list invites: %d %s", r.Status, r.Raw)
	}
	list, _ := r.Body["data"].([]any)
	for _, raw := range list {
		if m, _ := raw.(map[string]any); m["id"] == id {
			return m
		}
	}
	t.Fatalf("invite %s not listed: %s", id, r.Raw)
	return nil
}

// findUser returns the member with the given user id from GET /team/users.
func findUser(t *testing.T, token, userID string) map[string]any {
	t.Helper()
	r := do(t, "GET", "/api/v1/team/users", token, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list users: %d %s", r.Status, r.Raw)
	}
	list, _ := r.Body["data"].([]any)
	for _, raw := range list {
		if m, _ := raw.(map[string]any); m["user_id"] == userID {
			return m
		}
	}
	t.Fatalf("user %s not listed: %s", userID, r.Raw)
	return nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func expectStatus(t *testing.T, r apiResp, want int, what string) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("%s: expected %d, got %d %s", what, want, r.Status, r.Raw)
	}
}

// --- invites with section access ---------------------------------------------

// TestUserInviteWithGroupsRestrictsImmediately: section access chosen at invite
// time is applied on acceptance, so the member never has full access.
func TestUserInviteWithGroupsRestrictsImmediately(t *testing.T) {
	owner, _ := signup(t, "Invite Groups Co")

	inv := createInvite(t, owner, map[string]any{
		"email": newEmail("crm"), "role": "member", "allowed_groups": []string{"CRM", "NotASection"},
	})
	groups, _ := inv["allowed_groups"].([]any)
	if len(groups) != 1 || groups[0] != "CRM" {
		t.Fatalf("expected allowed_groups [CRM] (unknown dropped), got %v", inv["allowed_groups"])
	}
	if inv["status"] != "pending" || inv["expires_at"] == nil {
		t.Fatalf("new invite should be pending with an expiry: %v", inv)
	}
	exp, err := time.Parse(time.RFC3339Nano, inv["expires_at"].(string))
	if err != nil || exp.Before(time.Now().Add(13*24*time.Hour)) || exp.After(time.Now().Add(15*24*time.Hour)) {
		t.Fatalf("expected ~14 day expiry, got %v (%v)", inv["expires_at"], err)
	}

	acc := acceptInvite(t, inv["token"].(string), "password123")
	expectStatus(t, acc, http.StatusOK, "accept")
	member, _ := acc.Body["token"].(string)

	expectStatus(t, do(t, "GET", "/api/v1/entities/leads", member, nil), http.StatusOK, "CRM access")
	expectStatus(t, do(t, "GET", "/api/v1/entities/invoices", member, nil), http.StatusForbidden, "ERP blocked")

	// Admin invites ignore restrictions (stored as full access).
	adm := createInvite(t, owner, map[string]any{
		"email": newEmail("adm"), "role": "admin", "allowed_groups": []string{"HR"},
	})
	if adm["allowed_groups"] != nil {
		t.Fatalf("admin invite should have full access, got %v", adm["allowed_groups"])
	}

	// Legacy-shaped request (no allowed_groups) keeps full access.
	legacy := createInvite(t, owner, map[string]any{"email": newEmail("legacy"), "role": "member"})
	if legacy["allowed_groups"] != nil {
		t.Fatalf("invite without allowed_groups should be full access, got %v", legacy["allowed_groups"])
	}
	lacc := acceptInvite(t, legacy["token"].(string), "password123")
	expectStatus(t, lacc, http.StatusOK, "legacy accept")
	expectStatus(t, do(t, "GET", "/api/v1/entities/invoices", lacc.Body["token"].(string), nil), http.StatusOK, "legacy full access")
}

// TestUserReinviteAppliesGroups covers the ON CONFLICT branch: re-inviting an
// existing member applies the invite's section access.
func TestUserReinviteAppliesGroups(t *testing.T) {
	owner, _ := signup(t, "Reinvite Co")
	email := newEmail("re")
	first := createInvite(t, owner, map[string]any{"email": email, "role": "member"})
	expectStatus(t, acceptInvite(t, first["token"].(string), "password123"), http.StatusOK, "first accept")

	second := createInvite(t, owner, map[string]any{"email": email, "role": "member", "allowed_groups": []string{"HR"}})
	acc := acceptInvite(t, second["token"].(string), "") // existing account: no password needed
	expectStatus(t, acc, http.StatusOK, "re-accept")
	member, _ := acc.Body["token"].(string)
	expectStatus(t, do(t, "GET", "/api/v1/entities/leads", member, nil), http.StatusForbidden, "CRM blocked after re-invite")
}

// --- role guards ---------------------------------------------------------------

func TestUserRoleGuards(t *testing.T) {
	owner, _ := signup(t, "Guards Co")
	ownerID := myUserID(t, owner)
	admin, adminID := inviteMember(t, owner, "admin")
	_, memberID := inviteMember(t, owner, "member")

	// Admins cannot invite owners; owners can.
	expectStatus(t, do(t, "POST", "/api/v1/team/invites", admin, map[string]any{"email": newEmail("o"), "role": "owner"}),
		http.StatusForbidden, "admin invites owner")
	expectStatus(t, do(t, "POST", "/api/v1/team/invites", owner, map[string]any{"email": newEmail("o"), "role": "owner"}),
		http.StatusCreated, "owner invites owner")

	// Admins cannot modify owners or grant owner.
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+ownerID, admin, map[string]any{"role": "member"}),
		http.StatusForbidden, "admin demotes owner")
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+memberID, admin, map[string]any{"role": "owner"}),
		http.StatusForbidden, "admin grants owner")
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+ownerID, admin, nil),
		http.StatusForbidden, "admin removes owner")
	expectStatus(t, do(t, "POST", "/api/v1/team/users/"+ownerID+"/password-reset", admin, nil),
		http.StatusForbidden, "admin resets owner password")

	// Admins can change members' role and sections.
	upd := do(t, "PUT", "/api/v1/team/users/"+memberID, admin, map[string]any{"role": "viewer", "allowed_groups": []string{"HR"}})
	expectStatus(t, upd, http.StatusOK, "admin edits member")
	d := dataObj(t, upd)
	if d["role"] != "viewer" || fmt.Sprint(d["allowed_groups"]) != "[HR]" {
		t.Fatalf("unexpected member after update: %v", d)
	}
	// Omitting allowed_groups leaves it unchanged; null resets to full access.
	d = dataObj(t, do(t, "PUT", "/api/v1/team/users/"+memberID, admin, map[string]any{"role": "member"}))
	if fmt.Sprint(d["allowed_groups"]) != "[HR]" {
		t.Fatalf("allowed_groups should be unchanged, got %v", d["allowed_groups"])
	}
	d = dataObj(t, do(t, "PUT", "/api/v1/team/users/"+memberID, admin, map[string]any{"allowed_groups": nil}))
	if d["allowed_groups"] != nil {
		t.Fatalf("allowed_groups null should mean full access, got %v", d["allowed_groups"])
	}

	// The last owner cannot be demoted.
	r := do(t, "PUT", "/api/v1/team/users/"+ownerID, owner, map[string]any{"role": "admin"})
	expectStatus(t, r, http.StatusConflict, "demote last owner")
	if r.errCode() != "last_owner" {
		t.Fatalf("expected last_owner, got %s", r.errCode())
	}

	// Nobody can remove themselves.
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+ownerID, owner, nil), http.StatusBadRequest, "remove self")
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+adminID, admin, nil), http.StatusBadRequest, "admin remove self")

	// Owner promotes the admin; now the original owner may step down.
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+adminID, owner, map[string]any{"role": "owner"}), http.StatusOK, "grant owner")
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+ownerID, owner, map[string]any{"role": "admin"}), http.StatusOK, "demote with another owner")

	// Removing a member revokes their access immediately.
	memberTok, mid := inviteMember(t, admin, "member")
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+mid, admin, nil), http.StatusNoContent, "remove member")
	expectStatus(t, do(t, "GET", "/api/v1/entities/leads", memberTok, nil), http.StatusForbidden, "removed member access")
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+mid, admin, nil), http.StatusNotFound, "remove twice")
}

// --- cross-tenant identity guard -------------------------------------------------

func TestUserCrossTenantIdentityGuard(t *testing.T) {
	owner1, _ := signup(t, "Identity One")
	owner2, _ := signup(t, "Identity Two")

	email := newEmail("shared")
	inv := createInvite(t, owner1, map[string]any{"email": email, "role": "member"})
	acc := acceptInvite(t, inv["token"].(string), "password123")
	expectStatus(t, acc, http.StatusOK, "accept org1")
	uid := acc.Body["user"].(map[string]any)["id"].(string)

	// Single-tenant user: org1's admin may change identity.
	if u := findUser(t, owner1, uid); u["identity_editable"] != true {
		t.Fatalf("single-tenant user should be identity-editable: %v", u)
	}
	newMail := newEmail("renamed")
	d := dataObj(t, do(t, "PUT", "/api/v1/team/users/"+uid, owner1, map[string]any{"full_name": "Renamed", "email": strings.ToUpper(newMail)}))
	if d["full_name"] != "Renamed" || d["email"] != newMail {
		t.Fatalf("identity not updated: %v", d)
	}
	// Email uniqueness is enforced.
	_, ownerEmail := signup(t, "Identity Three")
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+uid, owner1, map[string]any{"email": ownerEmail}),
		http.StatusConflict, "duplicate email")

	// The user joins a second org.
	inv2 := createInvite(t, owner2, map[string]any{"email": newMail, "role": "member"})
	expectStatus(t, acceptInvite(t, inv2["token"].(string), ""), http.StatusOK, "accept org2")

	if u := findUser(t, owner1, uid); u["identity_editable"] != false {
		t.Fatalf("multi-tenant user should not be identity-editable: %v", u)
	}
	r := do(t, "PUT", "/api/v1/team/users/"+uid, owner1, map[string]any{"full_name": "Hijacked"})
	expectStatus(t, r, http.StatusForbidden, "shared name change")
	if r.errCode() != "identity_shared" {
		t.Fatalf("expected identity_shared, got %s", r.errCode())
	}
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+uid, owner1, map[string]any{"email": newEmail("x")}),
		http.StatusForbidden, "shared email change")
	expectStatus(t, do(t, "POST", "/api/v1/team/users/"+uid+"/password-reset", owner1, nil),
		http.StatusForbidden, "shared password reset")

	// Per-membership changes still work, also when the unchanged identity is resent.
	d = dataObj(t, do(t, "PUT", "/api/v1/team/users/"+uid, owner1, map[string]any{
		"full_name": "Renamed", "email": newMail, "role": "viewer", "allowed_groups": []string{"CRM"},
	}))
	if d["role"] != "viewer" {
		t.Fatalf("role change should be allowed for shared users: %v", d)
	}
	// …and stay per-tenant: org2's membership is untouched.
	if u := findUser(t, owner2, uid); u["role"] != "member" || u["allowed_groups"] != nil {
		t.Fatalf("org2 membership should be unchanged: %v", u)
	}

	// Tenant isolation: org2 cannot see or edit org1-only members.
	_, onlyOrg1 := inviteMember(t, owner1, "member")
	expectStatus(t, do(t, "PUT", "/api/v1/team/users/"+onlyOrg1, owner2, map[string]any{"role": "viewer"}),
		http.StatusNotFound, "cross-tenant edit")
	expectStatus(t, do(t, "POST", "/api/v1/team/users/"+onlyOrg1+"/password-reset", owner2, nil),
		http.StatusNotFound, "cross-tenant reset")
	expectStatus(t, do(t, "DELETE", "/api/v1/team/users/"+onlyOrg1, owner2, nil),
		http.StatusNotFound, "cross-tenant remove")
}

// --- password reset links ---------------------------------------------------------

func TestUserPasswordResetLifecycle(t *testing.T) {
	owner, _ := signup(t, "Reset Co")
	email := newEmail("reset")
	inv := createInvite(t, owner, map[string]any{"email": email, "role": "member"})
	acc := acceptInvite(t, inv["token"].(string), "password123")
	uid := acc.Body["user"].(map[string]any)["id"].(string)

	// No login yet.
	if u := findUser(t, owner, uid); u["last_login_at"] != nil || u["joined_at"] == nil {
		t.Fatalf("expected joined_at set and no last login: %v", u)
	}

	link := do(t, "POST", "/api/v1/team/users/"+uid+"/password-reset", owner, nil)
	expectStatus(t, link, http.StatusCreated, "create reset")
	tok, _ := link.Body["token"].(string)
	if tok == "" || !strings.Contains(link.Body["path"].(string), tok) || link.Body["expires_at"] == nil {
		t.Fatalf("bad reset response: %s", link.Raw)
	}
	// Only the hash is stored.
	var stored, createdBy string
	if err := testPool.QueryRow(context.Background(),
		`SELECT token_hash, created_by::text FROM password_resets WHERE user_id=$1`, uid).Scan(&stored, &createdBy); err != nil {
		t.Fatalf("query reset: %v", err)
	}
	if stored != sha256Hex(tok) || createdBy != myUserID(t, owner) {
		t.Fatalf("expected sha256 hash + created_by, got %s / %s", stored, createdBy)
	}

	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": tok, "password": "short"}),
		http.StatusBadRequest, "weak password")
	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": tok, "password": "newpassword1"}),
		http.StatusOK, "reset")

	// Old password fails, new one works and records last login.
	expectStatus(t, do(t, "POST", "/api/v1/auth/login", "", map[string]any{"email": email, "password": "password123"}),
		http.StatusUnauthorized, "old password")
	expectStatus(t, do(t, "POST", "/api/v1/auth/login", "", map[string]any{"email": email, "password": "newpassword1"}),
		http.StatusOK, "new password")
	if u := findUser(t, owner, uid); u["last_login_at"] == nil {
		t.Fatalf("last_login_at should be set after login: %v", u)
	}

	// Single use.
	r := do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": tok, "password": "another-pass"})
	expectStatus(t, r, http.StatusBadRequest, "reuse")
	if r.errCode() != "invalid_token" {
		t.Fatalf("expected invalid_token, got %s", r.errCode())
	}

	// Expired links fail.
	link2 := do(t, "POST", "/api/v1/team/users/"+uid+"/password-reset", owner, nil)
	tok2, _ := link2.Body["token"].(string)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE password_resets SET expires_at = now() - interval '1 minute' WHERE token_hash=$1`, sha256Hex(tok2)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": tok2, "password": "another-pass"}),
		http.StatusBadRequest, "expired")

	// A newer link replaces the outstanding one.
	a := do(t, "POST", "/api/v1/team/users/"+uid+"/password-reset", owner, nil).Body["token"].(string)
	b := do(t, "POST", "/api/v1/team/users/"+uid+"/password-reset", owner, nil).Body["token"].(string)
	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": a, "password": "another-pass"}),
		http.StatusBadRequest, "superseded link")
	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": b, "password": "another-pass"}),
		http.StatusOK, "latest link")

	// Unknown token.
	expectStatus(t, do(t, "POST", "/api/v1/auth/reset-password", "", map[string]any{"token": "nope", "password": "another-pass"}),
		http.StatusBadRequest, "unknown token")
}

// --- invite lifecycle ---------------------------------------------------------------

func TestUserInviteRevokeExpiryAndFailures(t *testing.T) {
	owner, _ := signup(t, "Invite Life Co")
	ctx := context.Background()

	inv := createInvite(t, owner, map[string]any{"email": newEmail("life"), "role": "member"})
	id, tok := inv["id"].(string), inv["token"].(string)

	// A weak password is recorded against the invite.
	expectStatus(t, acceptInvite(t, tok, "short"), http.StatusBadRequest, "weak password")
	got := findInvite(t, owner, id)
	if got["failed_attempts"] != float64(1) || !strings.Contains(fmt.Sprint(got["last_error"]), "8 characters") || got["last_attempt_at"] == nil {
		t.Fatalf("weak password not recorded: %v", got)
	}
	// A new account without a password is recorded too.
	expectStatus(t, acceptInvite(t, tok, ""), http.StatusBadRequest, "password required")
	if got = findInvite(t, owner, id); got["failed_attempts"] != float64(2) || !strings.Contains(fmt.Sprint(got["last_error"]), "password required") {
		t.Fatalf("password-required not recorded: %v", got)
	}

	// Edit role/sections while pending.
	ed := do(t, "PUT", "/api/v1/team/invites/"+id, owner, map[string]any{"role": "viewer", "allowed_groups": []string{"HR"}})
	expectStatus(t, ed, http.StatusOK, "edit invite")
	if ed.Body["role"] != "viewer" || fmt.Sprint(ed.Body["allowed_groups"]) != "[HR]" {
		t.Fatalf("invite not edited: %s", ed.Raw)
	}

	// Revoke -> accept rejected and recorded.
	rv := do(t, "POST", "/api/v1/team/invites/"+id+"/revoke", owner, nil)
	expectStatus(t, rv, http.StatusOK, "revoke")
	if rv.Body["status"] != "revoked" {
		t.Fatalf("expected revoked, got %v", rv.Body["status"])
	}
	r := acceptInvite(t, tok, "password123")
	expectStatus(t, r, http.StatusBadRequest, "accept revoked")
	if !strings.Contains(string(r.Raw), "revoked") {
		t.Fatalf("expected revoked message: %s", r.Raw)
	}
	if got = findInvite(t, owner, id); got["failed_attempts"] != float64(3) || got["status"] != "revoked" {
		t.Fatalf("revoked attempt not recorded: %v", got)
	}
	expectStatus(t, do(t, "PUT", "/api/v1/team/invites/"+id, owner, map[string]any{"role": "member"}),
		http.StatusConflict, "edit revoked invite")

	// Resend -> new token, pending again; the old token no longer works.
	rs := do(t, "POST", "/api/v1/team/invites/"+id+"/resend", owner, nil)
	expectStatus(t, rs, http.StatusOK, "resend")
	newTok, _ := rs.Body["token"].(string)
	if rs.Body["status"] != "pending" || newTok == "" || newTok == tok {
		t.Fatalf("resend should give a new pending token: %s", rs.Raw)
	}
	expectStatus(t, acceptInvite(t, tok, "password123"), http.StatusBadRequest, "old token")
	acc := acceptInvite(t, newTok, "password123")
	expectStatus(t, acc, http.StatusOK, "accept resent")
	if acc.Body["role"] != "viewer" {
		t.Fatalf("edited role should apply, got %v", acc.Body["role"])
	}
	viewer, _ := acc.Body["token"].(string)
	expectStatus(t, do(t, "GET", "/api/v1/entities/leads", viewer, nil), http.StatusForbidden, "edited sections apply")
	if got = findInvite(t, owner, id); got["status"] != "accepted" || got["accepted"] != true {
		t.Fatalf("expected accepted: %v", got)
	}
	// Accepted invites can't be changed.
	expectStatus(t, do(t, "POST", "/api/v1/team/invites/"+id+"/revoke", owner, nil), http.StatusConflict, "revoke accepted")
	expectStatus(t, do(t, "POST", "/api/v1/team/invites/"+id+"/resend", owner, nil), http.StatusConflict, "resend accepted")
	// Reusing an accepted token is recorded but still fails.
	expectStatus(t, acceptInvite(t, newTok, "password123"), http.StatusBadRequest, "reuse accepted")

	// Expired invites are rejected and listed as expired.
	exp := createInvite(t, owner, map[string]any{"email": newEmail("exp"), "role": "member"})
	if _, err := testPool.Exec(ctx, `UPDATE invites SET expires_at = now() - interval '1 minute' WHERE id=$1`, exp["id"]); err != nil {
		t.Fatalf("expire invite: %v", err)
	}
	if got = findInvite(t, owner, exp["id"].(string)); got["status"] != "expired" {
		t.Fatalf("expected expired status: %v", got)
	}
	r = acceptInvite(t, exp["token"].(string), "password123")
	expectStatus(t, r, http.StatusBadRequest, "accept expired")
	if !strings.Contains(string(r.Raw), "expired") {
		t.Fatalf("expected expired message: %s", r.Raw)
	}
	if got = findInvite(t, owner, exp["id"].(string)); got["failed_attempts"] != float64(1) {
		t.Fatalf("expired attempt not recorded: %v", got)
	}

	// Pre-existing invites (expires_at NULL) never expire.
	legacy := createInvite(t, owner, map[string]any{"email": newEmail("old"), "role": "member"})
	if _, err := testPool.Exec(ctx, `UPDATE invites SET expires_at = NULL, created_at = now() - interval '1 year' WHERE id=$1`, legacy["id"]); err != nil {
		t.Fatalf("legacy invite: %v", err)
	}
	expectStatus(t, acceptInvite(t, legacy["token"].(string), "password123"), http.StatusOK, "accept legacy invite")

	// Admins cannot touch owner invites.
	ownerInv := createInvite(t, owner, map[string]any{"email": newEmail("own"), "role": "owner"})
	admin, _ := inviteMember(t, owner, "admin")
	expectStatus(t, do(t, "POST", "/api/v1/team/invites/"+ownerInv["id"].(string)+"/revoke", admin, nil),
		http.StatusForbidden, "admin revokes owner invite")
	expectStatus(t, do(t, "PUT", "/api/v1/team/invites/"+id, admin, map[string]any{"role": "owner"}),
		http.StatusConflict, "admin edits accepted invite")

	// Invites are tenant-scoped.
	other, _ := signup(t, "Invite Other Co")
	expectStatus(t, do(t, "POST", "/api/v1/team/invites/"+exp["id"].(string)+"/revoke", other, nil),
		http.StatusNotFound, "cross-tenant revoke")
}

// --- non-admins ---------------------------------------------------------------------

func TestUserManagerEndpointsRequireAdmin(t *testing.T) {
	owner, _ := signup(t, "UM Admin Gate Co")
	ownerID := myUserID(t, owner)
	member, _ := inviteMember(t, owner, "member")
	viewer, _ := inviteMember(t, owner, "viewer")
	inv := createInvite(t, owner, map[string]any{"email": newEmail("gate"), "role": "member"})
	invID := inv["id"].(string)

	calls := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/team/users", nil},
		{"PUT", "/api/v1/team/users/" + ownerID, map[string]any{"role": "member"}},
		{"DELETE", "/api/v1/team/users/" + ownerID, nil},
		{"POST", "/api/v1/team/users/" + ownerID + "/password-reset", nil},
		{"PUT", "/api/v1/team/invites/" + invID, map[string]any{"role": "member"}},
		{"POST", "/api/v1/team/invites/" + invID + "/revoke", nil},
		{"POST", "/api/v1/team/invites/" + invID + "/resend", nil},
		{"POST", "/api/v1/team/invites", map[string]any{"email": newEmail("x"), "role": "member", "allowed_groups": []string{"CRM"}}},
	}
	for _, tok := range []string{member, viewer} {
		for _, c := range calls {
			expectStatus(t, do(t, c.method, c.path, tok, c.body), http.StatusForbidden, c.method+" "+c.path)
		}
	}
	// Unauthenticated callers get 401.
	expectStatus(t, do(t, "GET", "/api/v1/team/users", "", nil), http.StatusUnauthorized, "anonymous")

	// An admin's API token cannot mint password reset links.
	_, memberID := inviteMember(t, owner, "member")
	pat := mintToken(t, owner, map[string]any{"name": "agent"})
	expectStatus(t, do(t, "POST", "/api/v1/team/users/"+memberID+"/password-reset", pat, nil),
		http.StatusForbidden, "reset via API token")
}
