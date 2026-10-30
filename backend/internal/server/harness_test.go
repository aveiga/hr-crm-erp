package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"hrcrm/internal/cache"
	"hrcrm/internal/config"
	"hrcrm/internal/db"
	"hrcrm/internal/server"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	testPool   *pgxpool.Pool
	testServer *httptest.Server
)

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://hrcrm:hrcrm@localhost:55432/hrcrm_test?sslmode=disable"
	}
	ctx := context.Background()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot reach test database at %s: %v\n", url, err)
		os.Exit(1)
	}
	testPool = pool

	if err := db.Migrate(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: migrate: %v\n", err)
		os.Exit(1)
	}

	cfg := config.Load()
	cfg.Env = "test"
	cfg.JWTSecret = "test-secret"
	cfg.JWTTTL = time.Hour
	cfg.BcryptCost = 4    // fast hashing in tests
	cfg.TemplatesDir = "" // use templates embedded in the binary
	fakeAzure := httptest.NewServer(fakeAzureHandler())
	defer fakeAzure.Close()
	cfg.AzureLoginURL, cfg.AzureManagementURL = fakeAzure.URL, fakeAzure.URL

	c, _ := cache.New("") // no-op cache in tests
	srv, err := server.New(ctx, cfg, pool, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: build server: %v\n", err)
		os.Exit(1)
	}
	testServer = httptest.NewServer(srv.Handler())

	code := m.Run()
	testServer.Close()
	pool.Close()
	os.Exit(code)
}

// apiResp is a decoded JSON response with status.
type apiResp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (r apiResp) errCode() string {
	if e, ok := r.Body["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

func do(t *testing.T, method, path, token string, body any) apiResp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, testServer.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Raw: raw}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

// signup creates a fresh user+org and returns its token. Emails are unique.
func signup(t *testing.T, org string) (token, email string) {
	t.Helper()
	email = fmt.Sprintf("user-%d-%d@example.com", time.Now().UnixNano(), randInt())
	r := do(t, "POST", "/api/v1/auth/signup", "", map[string]any{
		"email": email, "password": "password123", "full_name": "Test User", "organization": org,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("signup failed: %d %s", r.Status, r.Raw)
	}
	tok, _ := r.Body["token"].(string)
	if tok == "" {
		t.Fatalf("signup returned no token: %s", r.Raw)
	}
	return tok, email
}

var seed = time.Now().UnixNano()

func randInt() int64 {
	seed = seed*6364136223846793005 + 1442695040888963407
	if seed < 0 {
		return -seed
	}
	return seed
}

// dataField pulls the "data" object from a response body.
func dataObj(t *testing.T, r apiResp) map[string]any {
	t.Helper()
	d, ok := r.Body["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %s", r.Raw)
	}
	return d
}
