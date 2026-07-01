package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestInviteAndRBAC(t *testing.T) {
	ownerToken, _ := signup(t, "Team Co")

	inviteEmail := fmt.Sprintf("viewer-%d@example.com", randInt())

	// Owner invites a viewer.
	inv := do(t, "POST", "/api/v1/team/invites", ownerToken, map[string]any{
		"email": inviteEmail, "role": "viewer",
	})
	if inv.Status != http.StatusCreated {
		t.Fatalf("invite status %d: %s", inv.Status, inv.Raw)
	}
	inviteToken, _ := inv.Body["token"].(string)
	if inviteToken == "" {
		t.Fatalf("invite has no token: %s", inv.Raw)
	}

	// Invitee accepts, setting a password -> gets a token, role viewer.
	acc := do(t, "POST", "/api/v1/auth/accept-invite", "", map[string]any{
		"token": inviteToken, "full_name": "Vic Viewer", "password": "password123",
	})
	if acc.Status != http.StatusOK {
		t.Fatalf("accept-invite status %d: %s", acc.Status, acc.Raw)
	}
	if acc.Body["role"] != "viewer" {
		t.Fatalf("expected viewer role, got %v", acc.Body["role"])
	}
	viewerToken, _ := acc.Body["token"].(string)

	// Viewer can read.
	read := do(t, "GET", "/api/v1/entities/accounts", viewerToken, nil)
	if read.Status != http.StatusOK {
		t.Fatalf("viewer read status %d", read.Status)
	}

	// Viewer cannot write.
	write := do(t, "POST", "/api/v1/entities/accounts", viewerToken, map[string]any{"account_name": "Nope"})
	if write.Status != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer write, got %d: %s", write.Status, write.Raw)
	}

	// Viewer cannot access admin team endpoints.
	adminOnly := do(t, "GET", "/api/v1/team/members", viewerToken, nil)
	if adminOnly.Status != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer admin access, got %d", adminOnly.Status)
	}

	// Owner sees both members.
	members := do(t, "GET", "/api/v1/team/members", ownerToken, nil)
	if members.Status != http.StatusOK {
		t.Fatalf("members status %d", members.Status)
	}
	list, _ := members.Body["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected 2 members, got %d", len(list))
	}
}

func TestInviteRequiresAdmin(t *testing.T) {
	// A brand-new org's owner is admin; a member created via invite is not.
	ownerToken, _ := signup(t, "Admin Gate Co")
	memberEmail := fmt.Sprintf("member-%d@example.com", randInt())
	inv := do(t, "POST", "/api/v1/team/invites", ownerToken, map[string]any{"email": memberEmail, "role": "member"})
	token, _ := inv.Body["token"].(string)
	acc := do(t, "POST", "/api/v1/auth/accept-invite", "", map[string]any{
		"token": token, "full_name": "Mel Member", "password": "password123",
	})
	memberToken, _ := acc.Body["token"].(string)

	// Member (can write but not admin) cannot create invites.
	r := do(t, "POST", "/api/v1/team/invites", memberToken, map[string]any{"email": "x@example.com", "role": "member"})
	if r.Status != http.StatusForbidden {
		t.Fatalf("expected 403 for member creating invite, got %d", r.Status)
	}
}
