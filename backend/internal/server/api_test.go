package server_test

import (
	"net/http"
	"testing"
)

func TestSignupLoginMe(t *testing.T) {
	token, email := signup(t, "Acme HR")

	// /me reflects the signed-up identity and owner role.
	me := do(t, "GET", "/api/v1/auth/me", token, nil)
	if me.Status != http.StatusOK {
		t.Fatalf("me status %d: %s", me.Status, me.Raw)
	}
	if me.Body["role"] != "owner" {
		t.Fatalf("expected owner role, got %v", me.Body["role"])
	}

	// Login with the same credentials yields a usable token.
	login := do(t, "POST", "/api/v1/auth/login", "", map[string]any{
		"email": email, "password": "password123",
	})
	if login.Status != http.StatusOK {
		t.Fatalf("login status %d: %s", login.Status, login.Raw)
	}
	if _, ok := login.Body["token"].(string); !ok {
		t.Fatalf("login returned no token")
	}

	// Wrong password is rejected.
	bad := do(t, "POST", "/api/v1/auth/login", "", map[string]any{
		"email": email, "password": "wrong",
	})
	if bad.Status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad password, got %d", bad.Status)
	}
}

func TestSignupRejectsDuplicateEmailAndWeakPassword(t *testing.T) {
	_, email := signup(t, "Dup Org")
	dup := do(t, "POST", "/api/v1/auth/signup", "", map[string]any{
		"email": email, "password": "password123", "organization": "Other",
	})
	if dup.Status != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d: %s", dup.Status, dup.Raw)
	}

	weak := do(t, "POST", "/api/v1/auth/signup", "", map[string]any{
		"email": "weak@example.com", "password": "short",
	})
	if weak.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for weak password, got %d", weak.Status)
	}
}

func TestRequiresAuth(t *testing.T) {
	r := do(t, "GET", "/api/v1/entities/accounts", "", nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", r.Status)
	}
}

func TestCRUDAccount(t *testing.T) {
	token, _ := signup(t, "CRUD Co")

	// Missing required field -> validation error.
	bad := do(t, "POST", "/api/v1/entities/accounts", token, map[string]any{"city": "Lisbon"})
	if bad.Status != http.StatusBadRequest || bad.errCode() != "validation" {
		t.Fatalf("expected validation error, got %d %s", bad.Status, bad.Raw)
	}

	// Create.
	created := do(t, "POST", "/api/v1/entities/accounts", token, map[string]any{
		"account_name": "Globex", "country": "Portugal", "payment_terms_days": 30,
	})
	if created.Status != http.StatusCreated {
		t.Fatalf("create status %d: %s", created.Status, created.Raw)
	}
	rec := dataObj(t, created)
	id, _ := rec["id"].(string)
	if id == "" {
		t.Fatalf("created account has no id")
	}
	if rec["account_name"] != "Globex" {
		t.Fatalf("unexpected name %v", rec["account_name"])
	}
	if rec["payment_terms_days"].(float64) != 30 {
		t.Fatalf("expected terms 30, got %v", rec["payment_terms_days"])
	}

	// Get.
	got := do(t, "GET", "/api/v1/entities/accounts/"+id, token, nil)
	if got.Status != http.StatusOK || dataObj(t, got)["account_name"] != "Globex" {
		t.Fatalf("get failed: %d %s", got.Status, got.Raw)
	}

	// Update.
	upd := do(t, "PUT", "/api/v1/entities/accounts/"+id, token, map[string]any{"city": "Porto"})
	if upd.Status != http.StatusOK || dataObj(t, upd)["city"] != "Porto" {
		t.Fatalf("update failed: %d %s", upd.Status, upd.Raw)
	}

	// List includes the record.
	list := do(t, "GET", "/api/v1/entities/accounts?q=Globex", token, nil)
	if list.Status != http.StatusOK {
		t.Fatalf("list failed: %d", list.Status)
	}
	if total, _ := list.Body["total"].(float64); total < 1 {
		t.Fatalf("expected at least one result, got %v", list.Body["total"])
	}

	// Delete.
	del := do(t, "DELETE", "/api/v1/entities/accounts/"+id, token, nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", del.Status, del.Raw)
	}
	after := do(t, "GET", "/api/v1/entities/accounts/"+id, token, nil)
	if after.Status != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", after.Status)
	}
}

func TestTenantIsolation(t *testing.T) {
	tokenA, _ := signup(t, "Tenant A")
	tokenB, _ := signup(t, "Tenant B")

	// A creates an account.
	created := do(t, "POST", "/api/v1/entities/accounts", tokenA, map[string]any{"account_name": "SecretA"})
	idA := dataObj(t, created)["id"].(string)

	// B must not see A's account in its list.
	listB := do(t, "GET", "/api/v1/entities/accounts?q=SecretA", tokenB, nil)
	if total, _ := listB.Body["total"].(float64); total != 0 {
		t.Fatalf("tenant B saw tenant A data (total=%v)", listB.Body["total"])
	}

	// B must not be able to read A's account by id.
	getB := do(t, "GET", "/api/v1/entities/accounts/"+idA, tokenB, nil)
	if getB.Status != http.StatusNotFound {
		t.Fatalf("expected 404 cross-tenant read, got %d", getB.Status)
	}

	// B must not be able to delete A's account.
	delB := do(t, "DELETE", "/api/v1/entities/accounts/"+idA, tokenB, nil)
	if delB.Status != http.StatusNotFound {
		t.Fatalf("expected 404 cross-tenant delete, got %d", delB.Status)
	}

	// A still has it.
	getA := do(t, "GET", "/api/v1/entities/accounts/"+idA, tokenA, nil)
	if getA.Status != http.StatusOK {
		t.Fatalf("tenant A lost its own record: %d", getA.Status)
	}
}

func TestUnknownEntity404(t *testing.T) {
	token, _ := signup(t, "X Co")
	r := do(t, "GET", "/api/v1/entities/not_a_real_entity", token, nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown entity, got %d", r.Status)
	}
}

func TestMetaAndDashboard(t *testing.T) {
	token, _ := signup(t, "Meta Co")

	meta := do(t, "GET", "/api/v1/meta", token, nil)
	if meta.Status != http.StatusOK {
		t.Fatalf("meta status %d", meta.Status)
	}
	ents, _ := meta.Body["entities"].([]any)
	if len(ents) < 20 {
		t.Fatalf("expected the full catalog, got %d entities", len(ents))
	}

	// Create one invoice so dashboard AR has data.
	do(t, "POST", "/api/v1/entities/invoices", token, map[string]any{
		"quote_description": "Consulting", "grand_total": 1000.0, "balance": 1000.0, "status": "Issued",
	})

	dash := do(t, "GET", "/api/v1/dashboard", token, nil)
	if dash.Status != http.StatusOK {
		t.Fatalf("dashboard status %d: %s", dash.Status, dash.Raw)
	}
	counts, _ := dash.Body["counts"].(map[string]any)
	if counts["invoices"].(float64) != 1 {
		t.Fatalf("expected 1 invoice in counts, got %v", counts["invoices"])
	}
	ar, _ := dash.Body["ar"].(map[string]any)
	if ar["outstanding"].(float64) != 1000 {
		t.Fatalf("expected AR outstanding 1000, got %v", ar["outstanding"])
	}
}
