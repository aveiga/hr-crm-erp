package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// inviteMember creates a member via invite+accept and returns their token+id.
func inviteMember(t *testing.T, ownerToken, role string) (token, userID string) {
	t.Helper()
	em := fmt.Sprintf("m-%d@example.com", randInt())
	inv := do(t, "POST", "/api/v1/team/invites", ownerToken, map[string]any{"email": em, "role": role})
	if inv.Status != http.StatusCreated {
		t.Fatalf("invite failed: %d %s", inv.Status, inv.Raw)
	}
	tok, _ := inv.Body["token"].(string)
	acc := do(t, "POST", "/api/v1/auth/accept-invite", "", map[string]any{
		"token": tok, "full_name": "Member", "password": "password123",
	})
	if acc.Status != http.StatusOK {
		t.Fatalf("accept failed: %d %s", acc.Status, acc.Raw)
	}
	token, _ = acc.Body["token"].(string)
	user, _ := acc.Body["user"].(map[string]any)
	userID, _ = user["id"].(string)
	return token, userID
}

func TestSectionPermissionsRestrictMember(t *testing.T) {
	owner, _ := signup(t, "Perms Co")
	memberToken, memberID := inviteMember(t, owner, "member")

	// By default the member has full access: can list CRM (leads) and ERP (invoices list).
	if r := do(t, "GET", "/api/v1/entities/leads", memberToken, nil); r.Status != http.StatusOK {
		t.Fatalf("member should access leads by default: %d", r.Status)
	}
	if r := do(t, "GET", "/api/v1/entities/invoices", memberToken, nil); r.Status != http.StatusOK {
		t.Fatalf("member should access invoices by default: %d", r.Status)
	}

	// Admin restricts the member to CRM only.
	set := do(t, "PUT", "/api/v1/team/permissions", owner, map[string]any{
		"user_id": memberID, "groups": []string{"CRM"},
	})
	if set.Status != http.StatusOK {
		t.Fatalf("set permissions failed: %d %s", set.Status, set.Raw)
	}

	// CRM still works…
	if r := do(t, "GET", "/api/v1/entities/leads", memberToken, nil); r.Status != http.StatusOK {
		t.Fatalf("member should still access CRM leads: %d", r.Status)
	}
	// …but ERP is now forbidden (list, get, create, docs, document generation).
	if r := do(t, "GET", "/api/v1/entities/invoices", memberToken, nil); r.Status != http.StatusForbidden {
		t.Fatalf("member should be blocked from ERP invoices, got %d", r.Status)
	}
	if r := do(t, "POST", "/api/v1/entities/products", memberToken, map[string]any{"sku": "X", "name": "Y"}); r.Status != http.StatusForbidden {
		t.Fatalf("member should be blocked from creating ERP products, got %d", r.Status)
	}
	if r := do(t, "POST", "/api/v1/docs/invoices", memberToken, map[string]any{"quote_description": "x"}); r.Status != http.StatusForbidden {
		t.Fatalf("member should be blocked from ERP docs, got %d", r.Status)
	}

	// /meta hides the forbidden sections.
	meta := do(t, "GET", "/api/v1/meta", memberToken, nil)
	ents, _ := meta.Body["entities"].([]any)
	for _, e := range ents {
		if g := e.(map[string]any)["group"]; g != "CRM" {
			t.Fatalf("meta should only contain CRM entities, found group %v", g)
		}
	}
	if len(ents) == 0 {
		t.Fatalf("meta should still contain CRM entities")
	}

	// Dashboard counts only include accessible sections.
	dash := do(t, "GET", "/api/v1/dashboard", memberToken, nil)
	counts, _ := dash.Body["counts"].(map[string]any)
	if _, ok := counts["invoices"]; ok {
		t.Fatalf("dashboard should not count ERP invoices for a CRM-only member")
	}
	if _, ok := counts["leads"]; !ok {
		t.Fatalf("dashboard should count CRM leads for the member")
	}
	if _, ok := dash.Body["ar"]; ok {
		t.Fatalf("dashboard AR should be hidden for a CRM-only member")
	}
}

func TestPermissionsResetToFullAccess(t *testing.T) {
	owner, _ := signup(t, "Perms Reset Co")
	memberToken, memberID := inviteMember(t, owner, "member")

	// Restrict to HR, then reset to full (groups: null).
	do(t, "PUT", "/api/v1/team/permissions", owner, map[string]any{"user_id": memberID, "groups": []string{"HR"}})
	if r := do(t, "GET", "/api/v1/entities/invoices", memberToken, nil); r.Status != http.StatusForbidden {
		t.Fatalf("expected ERP blocked after HR-only, got %d", r.Status)
	}
	reset := do(t, "PUT", "/api/v1/team/permissions", owner, map[string]any{"user_id": memberID, "groups": nil})
	if reset.Status != http.StatusOK {
		t.Fatalf("reset failed: %d %s", reset.Status, reset.Raw)
	}
	if r := do(t, "GET", "/api/v1/entities/invoices", memberToken, nil); r.Status != http.StatusOK {
		t.Fatalf("expected full access after reset, got %d", r.Status)
	}
}

func TestAdminsAlwaysFullAccessAndNonAdminCannotManage(t *testing.T) {
	owner, _ := signup(t, "Perms Admin Co")
	adminToken, adminID := inviteMember(t, owner, "admin")

	// Even if we try to restrict an admin, they keep full access.
	do(t, "PUT", "/api/v1/team/permissions", owner, map[string]any{"user_id": adminID, "groups": []string{"HR"}})
	if r := do(t, "GET", "/api/v1/entities/invoices", adminToken, nil); r.Status != http.StatusOK {
		t.Fatalf("admin must keep full access, got %d", r.Status)
	}

	// A non-admin cannot read or change the permissions matrix.
	memberToken, _ := inviteMember(t, owner, "member")
	if r := do(t, "GET", "/api/v1/team/permissions", memberToken, nil); r.Status != http.StatusForbidden {
		t.Fatalf("member must not read permissions, got %d", r.Status)
	}
	if r := do(t, "PUT", "/api/v1/team/permissions", memberToken, map[string]any{"user_id": adminID, "groups": nil}); r.Status != http.StatusForbidden {
		t.Fatalf("member must not set permissions, got %d", r.Status)
	}
}
