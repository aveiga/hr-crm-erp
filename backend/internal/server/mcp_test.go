package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearerRT injects the tenant's bearer token on every MCP HTTP request, so the
// endpoint's auth middleware builds a Principal exactly as it does for REST.
type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpConnect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "0"}, nil)
	tr := &mcp.StreamableClientTransport{
		Endpoint:             testServer.URL + "/api/v1/mcp",
		HTTPClient:           &http.Client{Transport: bearerRT{token}},
		DisableStandaloneSSE: true,
	}
	sess, err := client.Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// callTool invokes a tool and returns its text content plus the IsError flag.
func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func toolNames(t *testing.T, s *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
	}
	return names
}

// TestMCPToolsGeneratedFromCatalog asserts the tool set is generated from the
// registry: CRUD tools for writable entities, document tools for entities with
// line items, and no direct write tools for read-only lists.
func TestMCPToolsGeneratedFromCatalog(t *testing.T) {
	token, _ := signup(t, "MCP Tools Org")
	s := mcpConnect(t, token)
	names := toolNames(t, s)

	wantPresent := []string{
		"describe_schema", "dashboard",
		"list_people", "get_people", "create_people", "update_people", "delete_people",
		"get_doc_quotes", "save_doc_quotes", "convert_quotes", // document w/ line items + pipeline
		"list_contacts", "get_contacts", // read-only list: reads only
	}
	for _, n := range wantPresent {
		if !names[n] {
			t.Errorf("expected tool %q to be exposed", n)
		}
	}

	wantAbsent := []string{
		"create_contacts", "update_contacts", "delete_contacts", // read-only entity
		"create_quotes", "update_quotes", // documents are written via save_doc_
		"create_teams", // hidden child entity: no direct writes
	}
	for _, n := range wantAbsent {
		if names[n] {
			t.Errorf("tool %q should NOT be exposed", n)
		}
	}
}

// TestMCPCRUDRoundtrip drives a create/list through MCP and confirms the write
// lands in the same tenant-scoped store the REST API reads.
func TestMCPCRUDRoundtrip(t *testing.T) {
	token, _ := signup(t, "MCP CRUD Org")
	s := mcpConnect(t, token)

	out, isErr := callTool(t, s, "create_people", map[string]any{
		"full_name":   "Ada Lovelace",
		"worker_type": "Employee",
	})
	if isErr {
		t.Fatalf("create_people errored: %s", out)
	}
	var created struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("decode create result: %v (%s)", err, out)
	}
	id, _ := created.Data["id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %s", out)
	}

	// The REST API must see the MCP-created row (same store, same tenant).
	list := do(t, "GET", "/api/v1/entities/people?q=Ada", token, nil)
	if list.Status != http.StatusOK || !strings.Contains(string(list.Raw), "Ada Lovelace") {
		t.Fatalf("REST did not see MCP-created person: %d %s", list.Status, list.Raw)
	}

	// get by id via MCP.
	got, isErr := callTool(t, s, "get_people", map[string]any{"id": id})
	if isErr || !strings.Contains(got, "Ada Lovelace") {
		t.Fatalf("get_people failed: err=%v %s", isErr, got)
	}
}

// TestMCPTenantIsolation confirms tools built for one tenant cannot see or fetch
// another tenant's rows — tenancy is enforced by the reused repo, not the UI.
func TestMCPTenantIsolation(t *testing.T) {
	tokenA, _ := signup(t, "MCP Tenant A")
	tokenB, _ := signup(t, "MCP Tenant B")

	sa := mcpConnect(t, tokenA)
	out, isErr := callTool(t, sa, "create_people", map[string]any{"full_name": "Secret Person A"})
	if isErr {
		t.Fatalf("tenant A create errored: %s", out)
	}
	var created struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal([]byte(out), &created)
	idA, _ := created.Data["id"].(string)

	sb := mcpConnect(t, tokenB)

	// B's list must not contain A's person.
	listB, isErr := callTool(t, sb, "list_people", map[string]any{"q": "Secret Person A"})
	if isErr {
		t.Fatalf("tenant B list errored: %s", listB)
	}
	if strings.Contains(listB, "Secret Person A") {
		t.Fatalf("tenant B leaked tenant A data: %s", listB)
	}

	// B fetching A's id must fail (scoped not-found), not return the row.
	getB, isErr := callTool(t, sb, "get_people", map[string]any{"id": idA})
	if !isErr {
		t.Fatalf("tenant B fetched tenant A row across tenants: %s", getB)
	}
}
