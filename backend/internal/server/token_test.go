package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// mintToken creates an API token via the authenticated REST endpoint and
// returns the one-time plaintext.
func mintToken(t *testing.T, jwt string, body map[string]any) string {
	t.Helper()
	r := do(t, "POST", "/api/v1/tokens", jwt, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create token: %d %s", r.Status, r.Raw)
	}
	tok, _ := r.Body["token"].(string)
	if !strings.HasPrefix(tok, "hrcrm_pat_") {
		t.Fatalf("expected hrcrm_pat_ token, got %q", tok)
	}
	return tok
}

// TestAPITokenAuthenticatesLikeJWT confirms a minted token works as a bearer on
// the normal API and on the MCP endpoint (the agent use case), scoped to the
// same tenant.
func TestAPITokenAuthenticatesLikeJWT(t *testing.T) {
	jwt, _ := signup(t, "Token Org")

	// Seed a person via the JWT session.
	c := do(t, "POST", "/api/v1/entities/people", jwt, map[string]any{"full_name": "Grace Hopper"})
	if c.Status != http.StatusCreated {
		t.Fatalf("seed person: %d %s", c.Status, c.Raw)
	}

	pat := mintToken(t, jwt, map[string]any{"name": "ci-agent"})

	// REST call with the PAT sees the tenant's data.
	list := do(t, "GET", "/api/v1/entities/people?q=Grace", pat, nil)
	if list.Status != http.StatusOK || !strings.Contains(string(list.Raw), "Grace Hopper") {
		t.Fatalf("PAT REST call failed: %d %s", list.Status, list.Raw)
	}

	// The same PAT authenticates the MCP endpoint.
	s := mcpConnect(t, pat)
	out, isErr := callTool(t, s, "list_people", map[string]any{"q": "Grace"})
	if isErr || !strings.Contains(out, "Grace Hopper") {
		t.Fatalf("PAT MCP call failed: err=%v %s", isErr, out)
	}
}

// TestAPITokenReadOnly confirms a read_only token can read but not write, even
// though its owner is an owner (full-access) member.
func TestAPITokenReadOnly(t *testing.T) {
	jwt, _ := signup(t, "ReadOnly Org")
	pat := mintToken(t, jwt, map[string]any{"name": "ro-agent", "read_only": true})

	// Reads are allowed.
	if r := do(t, "GET", "/api/v1/entities/people", pat, nil); r.Status != http.StatusOK {
		t.Fatalf("read-only token blocked from reading: %d %s", r.Status, r.Raw)
	}
	// Writes are rejected (capped to viewer).
	w := do(t, "POST", "/api/v1/entities/people", pat, map[string]any{"full_name": "Nope"})
	if w.Status != http.StatusForbidden {
		t.Fatalf("read-only token should not write: got %d %s", w.Status, w.Raw)
	}
}

// TestAPITokenRevoke confirms a revoked token stops working immediately.
func TestAPITokenRevoke(t *testing.T) {
	jwt, _ := signup(t, "Revoke Org")

	// Create, then find its id via the list endpoint.
	create := do(t, "POST", "/api/v1/tokens", jwt, map[string]any{"name": "temp"})
	if create.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Status, create.Raw)
	}
	pat, _ := create.Body["token"].(string)
	id, _ := create.Body["id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %s", create.Raw)
	}

	// Works before revocation.
	if r := do(t, "GET", "/api/v1/entities/people", pat, nil); r.Status != http.StatusOK {
		t.Fatalf("token should work before revoke: %d", r.Status)
	}

	if r := do(t, "DELETE", "/api/v1/tokens/"+id, jwt, nil); r.Status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.Status, r.Raw)
	}

	if r := do(t, "GET", "/api/v1/entities/people", pat, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("revoked token should be unauthorized: got %d %s", r.Status, r.Raw)
	}
}

// TestAPITokenAdminManagesAll confirms an admin sees and can revoke tokens
// belonging to other members, while a non-admin cannot reach the admin endpoints.
func TestAPITokenAdminManagesAll(t *testing.T) {
	owner, _ := signup(t, "Admin Tokens Org")
	memberJWT, memberID := inviteMember(t, owner, "member")

	// The member mints a token for themselves.
	memberPAT := mintToken(t, memberJWT, map[string]any{"name": "member-agent"})

	// The admin sees the member's token in the tenant-wide list, with owner info.
	all := do(t, "GET", "/api/v1/team/tokens", owner, nil)
	if all.Status != http.StatusOK {
		t.Fatalf("admin list: %d %s", all.Status, all.Raw)
	}
	toks, _ := all.Body["tokens"].([]any)
	var id string
	for _, raw := range toks {
		row, _ := raw.(map[string]any)
		if row["name"] == "member-agent" {
			if row["user_id"] != memberID {
				t.Fatalf("token owner mismatch: %v vs %s", row["user_id"], memberID)
			}
			id, _ = row["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("admin did not see member's token: %s", all.Raw)
	}

	// A non-admin cannot reach the admin endpoints.
	if r := do(t, "GET", "/api/v1/team/tokens", memberJWT, nil); r.Status != http.StatusForbidden {
		t.Fatalf("non-admin should not list all tokens: %d %s", r.Status, r.Raw)
	}

	// The admin revokes the member's token; it stops working immediately.
	if r := do(t, "DELETE", "/api/v1/team/tokens/"+id, owner, nil); r.Status != http.StatusNoContent {
		t.Fatalf("admin revoke: %d %s", r.Status, r.Raw)
	}
	if r := do(t, "GET", "/api/v1/entities/people", memberPAT, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("admin-revoked token should be unauthorized: %d %s", r.Status, r.Raw)
	}
}

// TestAPITokenCannotMintTokens confirms a token cannot manage tokens, closing
// the self-escalation path (e.g. a read-only token minting a writable one).
func TestAPITokenCannotMintTokens(t *testing.T) {
	jwt, _ := signup(t, "NoEscalate Org")
	pat := mintToken(t, jwt, map[string]any{"name": "agent"})

	if r := do(t, "POST", "/api/v1/tokens", pat, map[string]any{"name": "escalated"}); r.Status != http.StatusForbidden {
		t.Fatalf("API token should not mint tokens: got %d %s", r.Status, r.Raw)
	}
	if r := do(t, "GET", "/api/v1/tokens", jwt, nil); r.Status != http.StatusOK {
		t.Fatalf("owner should list own tokens: %d %s", r.Status, r.Raw)
	}
}
