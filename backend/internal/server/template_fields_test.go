package server_test

import (
	"net/http"
	"testing"
)

// TestTemplateFieldsCatalog confirms the template-fields endpoint returns the
// documented placeholders (header + line) for authenticated users.
func TestTemplateFieldsCatalog(t *testing.T) {
	token, _ := signup(t, "Fields Org")

	r := do(t, "GET", "/api/v1/templates/fields", token, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("fields: %d %s", r.Status, r.Raw)
	}

	header, _ := r.Body["header"].([]any)
	line, _ := r.Body["line"].([]any)
	if len(header) == 0 || len(line) == 0 {
		t.Fatalf("expected header and line fields: %s", r.Raw)
	}

	placeholders := map[string]bool{}
	for _, group := range [][]any{header, line} {
		for _, raw := range group {
			f, _ := raw.(map[string]any)
			ph, _ := f["placeholder"].(string)
			desc, _ := f["description"].(string)
			if ph == "" || desc == "" {
				t.Fatalf("field missing placeholder/description: %v", f)
			}
			placeholders[ph] = true
		}
	}

	for _, want := range []string{"{{Legal_Entity_Name}}", "{{Grand_Total}}", "{{line.description}}"} {
		if !placeholders[want] {
			t.Errorf("expected placeholder %q in catalog", want)
		}
	}

	// Requires authentication.
	if un := do(t, "GET", "/api/v1/templates/fields", "", nil); un.Status != http.StatusUnauthorized {
		t.Fatalf("fields without auth should be 401, got %d", un.Status)
	}
}
