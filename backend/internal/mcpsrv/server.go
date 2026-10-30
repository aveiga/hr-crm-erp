// Package mcpsrv exposes the metadata-driven entity engine as a Model Context
// Protocol (MCP) server, so agents can drive the HR/CRM/ERP app the same way
// the REST API and UI do. Like every other surface in this codebase, the tool
// set is GENERATED from the resource Registry (catalog.go) — adding a module or
// field grows the MCP API for free, with no code here to touch.
//
// It reuses the app's existing enforcement rather than reimplementing it:
//   - Tenancy: every call goes through the same Repo/DocService, which scope all
//     queries by tenant_id. This package never touches the database directly.
//   - Permissions: the endpoint is wrapped by the auth middleware, so each
//     request carries a Principal. Section (group) access is filtered when tools
//     are built, and write tools re-check CanWrite — mirroring RequireWrite and
//     resolve() on the HTTP handlers.
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hrcrm/internal/httpx"
	"hrcrm/internal/resource"
)

const (
	serverName    = "hr-crm-erp"
	serverVersion = "0.1.0"

	instructions = "This server exposes the HR/CRM/ERP app. Tools are generated " +
		"from the app's module catalog. Every call is automatically scoped to your " +
		"tenant and your section permissions — you cannot read or write another " +
		"tenant's data. Start with `describe_schema` to learn the available modules " +
		"and their fields, then use list_/get_/create_/update_/delete_<module>. " +
		"Modules with line items (quotes, service agreements — module key `invoices` — " +
		"purchase orders, vendor bills, accounts) use get_doc_/save_doc_<module> which " +
		"handle the child rows and recompute totals in one transaction. A service " +
		"agreement's payment_schedule must add up to its grand total; register " +
		"payments with create_payments (optionally against a schedule_id)."
)

// convertibleFrom lists the entities that support pipeline conversion. Kept in
// sync with DocService.Convert (resource/docservice.go); an unlisted key there
// returns a "cannot be converted" error.
var convertibleFrom = map[string]bool{
	"leads":          true,
	"opportunities":  true,
	"quotes":         true,
	"legal_entities": true,
}

// MCP builds per-request MCP servers backed by the resource engine.
type MCP struct {
	reg  *resource.Registry
	repo *resource.Repo
	docs *resource.DocService
}

func New(reg *resource.Registry, repo *resource.Repo, docs *resource.DocService) *MCP {
	return &MCP{reg: reg, repo: repo, docs: docs}
}

// Handler returns the streamable-HTTP MCP endpoint. It MUST be mounted behind
// the auth middleware so each request carries a Principal; the server is built
// per request (stateless) and bound to that caller's tenant and permissions.
func (m *MCP) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, ok := httpx.PrincipalFrom(r.Context())
		if !ok {
			// The middleware should guarantee a principal; degrade to an empty
			// server (no tools) rather than leaking an unscoped one.
			return mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)
		}
		return m.buildServer(p)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
}

// buildServer registers the tools this caller is allowed to see, given their
// section permissions.
func (m *MCP) buildServer(p httpx.Principal) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions})

	s.AddTool(&mcp.Tool{
		Name:        "describe_schema",
		Description: "List the modules you can access and their field definitions (types, options, required, read-only). Call this first to learn what to send to create_/update_ tools.",
		InputSchema: objectSchema(nil, nil),
	}, m.describeSchema(p))

	s.AddTool(&mcp.Tool{
		Name:        "dashboard",
		Description: "Headline counts per module plus AR/AP and weighted-pipeline figures for your tenant.",
		InputSchema: objectSchema(nil, nil),
	}, m.dashboard(p))

	for _, e := range m.reg.All() {
		if !p.CanAccessGroup(e.Group) {
			continue
		}
		m.registerEntity(s, p, e)
	}
	return s
}

// registerEntity adds the CRUD/document tools appropriate to one entity.
func (m *MCP) registerEntity(s *mcp.Server, p httpx.Principal, e resource.Entity) {
	// Reads are available for every accessible entity.
	s.AddTool(&mcp.Tool{
		Name:        "list_" + e.Key,
		Description: fmt.Sprintf("List/search %s. Supports free-text search, equality filters, sorting and pagination.", e.Plural),
		InputSchema: listSchema(e),
	}, m.list(p, e))

	s.AddTool(&mcp.Tool{
		Name:        "get_" + e.Key,
		Description: fmt.Sprintf("Fetch one %s by id.", e.Label),
		InputSchema: idSchema(),
	}, m.get(p, e))

	switch {
	case e.IsDocument():
		// Documents (header + line items) are read and written through the doc
		// service so totals/derived fields/child rows stay consistent.
		s.AddTool(&mcp.Tool{
			Name:        "get_doc_" + e.Key,
			Description: fmt.Sprintf("Fetch a %s with its line items.", e.Label),
			InputSchema: idSchema(),
		}, m.getDoc(p, e))

		s.AddTool(&mcp.Tool{
			Name:        "save_doc_" + e.Key,
			Description: fmt.Sprintf("Create or update a %s and replace its line items in one transaction (totals recomputed). Omit `id` to create.", e.Label),
			InputSchema: saveDocSchema(m.reg, e),
		}, m.saveDoc(p, e))

		s.AddTool(&mcp.Tool{
			Name:        "delete_" + e.Key,
			Description: fmt.Sprintf("Delete a %s by id.", e.Label),
			InputSchema: idSchema(),
		}, m.del(p, e))

		if m.docs.CanDuplicate(e) {
			s.AddTool(&mcp.Tool{
				Name:        "duplicate_" + e.Key,
				Description: fmt.Sprintf("Copy a %s (header, line items and other child rows) into a new draft.", e.Label),
				InputSchema: idSchema(),
			}, m.duplicate(p, e))
		}

		if convertibleFrom[e.Key] {
			s.AddTool(&mcp.Tool{
				Name:        "convert_" + e.Key,
				Description: fmt.Sprintf("Advance a %s to the next pipeline stage (e.g. quote→sales order→service agreement), copying header and lines.", e.Label),
				InputSchema: idSchema(),
			}, m.convert(p, e))
		}

	case e.ReadOnly || e.Hidden:
		// ReadOnly lists (e.g. contacts) and Hidden line/child entities are edited
		// via their parent document, so no direct write tools are exposed.

	default:
		s.AddTool(&mcp.Tool{
			Name:        "create_" + e.Key,
			Description: fmt.Sprintf("Create a %s.", e.Label),
			InputSchema: writeSchema(e, true),
		}, m.create(p, e))

		s.AddTool(&mcp.Tool{
			Name:        "update_" + e.Key,
			Description: fmt.Sprintf("Update fields on a %s. Only include fields you want to change.", e.Label),
			InputSchema: writeSchema(e, false),
		}, m.update(p, e))

		s.AddTool(&mcp.Tool{
			Name:        "delete_" + e.Key,
			Description: fmt.Sprintf("Delete a %s by id.", e.Label),
			InputSchema: idSchema(),
		}, m.del(p, e))
	}
}

// --- tool handlers ---------------------------------------------------------

func (m *MCP) describeSchema(p httpx.Principal) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		entities := []resource.Entity{}
		for _, e := range m.reg.All() {
			if p.CanAccessGroup(e.Group) {
				entities = append(entities, e)
			}
		}
		return jsonResult(map[string]any{"entities": entities})
	}
}

func (m *MCP) dashboard(p httpx.Principal) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		counts := map[string]int{}
		for _, e := range m.reg.All() {
			if !p.CanAccessGroup(e.Group) {
				continue
			}
			n, err := m.repo.Count(ctx, e, p.TenantID)
			if err != nil {
				return errResult(err)
			}
			counts[e.Key] = n
		}
		return jsonResult(map[string]any{"counts": counts})
	}
}

func (m *MCP) list(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return errResult(err)
		}
		opt := resource.ListOptions{
			Page:     intArg(args, "page", 1),
			PageSize: intArg(args, "page_size", 50),
			Search:   strArg(args, "q"),
			Sort:     strArg(args, "sort"),
			Desc:     strArg(args, "dir") == "desc",
			Filters:  map[string]string{},
		}
		if raw := strArg(args, "query"); raw != "" {
			if q, err := url.ParseQuery(raw); err == nil {
				resource.ParseFilters(e, q, &opt)
			}
		}
		if raw, ok := args["filters"].(map[string]any); ok {
			for k, v := range raw {
				opt.Filters[k] = fmt.Sprint(v)
			}
		}
		res, err := m.repo.List(ctx, e, p.TenantID, opt)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(res)
	}
}

func (m *MCP) get(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		row, err := m.repo.Get(ctx, e, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"data": row})
	}
}

func (m *MCP) getDoc(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		row, err := m.repo.Get(ctx, e, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		lines, err := m.docs.LoadLines(ctx, e, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		out := map[string]any{"data": row, "lines": lines}
		children, err := m.docs.LoadChildren(ctx, e, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		if children != nil {
			out["children"] = children
		}
		return jsonResult(out)
	}
}

func (m *MCP) create(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		args, err := parseArgs(req)
		if err != nil {
			return errResult(err)
		}
		row, err := m.repo.Create(ctx, e, p.TenantID, args)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"data": row})
	}
}

func (m *MCP) update(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		args, err := parseArgs(req)
		if err != nil {
			return errResult(err)
		}
		delete(args, "id")
		row, err := m.repo.Update(ctx, e, p.TenantID, id, args)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"data": row})
	}
}

func (m *MCP) saveDoc(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		args, err := parseArgs(req)
		if err != nil {
			return errResult(err)
		}
		var idPtr *uuid.UUID
		if raw, ok := args["id"].(string); ok && raw != "" {
			id, perr := uuid.Parse(raw)
			if perr != nil {
				return errResult(httpx.Errorf(http.StatusBadRequest, "bad_request", "id must be a uuid"))
			}
			idPtr = &id
		}
		lines := []map[string]any{}
		if raw, ok := args["lines"].([]any); ok {
			for _, it := range raw {
				if mrow, ok := it.(map[string]any); ok {
					lines = append(lines, mrow)
				}
			}
		}
		// Secondary child collections (e.g. payment_schedule) are replaced only
		// when passed, so omitting one keeps the stored rows.
		var extra map[string][]map[string]any
		for _, c := range e.Children[1:] {
			raw, ok := args[c.Entity].([]any)
			if !ok {
				continue
			}
			if extra == nil {
				extra = map[string][]map[string]any{}
			}
			rows := []map[string]any{}
			for _, it := range raw {
				if mrow, ok := it.(map[string]any); ok {
					rows = append(rows, mrow)
				}
			}
			extra[c.Entity] = rows
		}
		header := map[string]any{}
		for k, v := range args {
			if k == "id" || k == "lines" {
				continue
			}
			if _, isChild := extra[k]; isChild {
				continue
			}
			header[k] = v
		}
		row, err := m.docs.SaveDoc(ctx, e, p.TenantID, idPtr, header, lines, extra)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"data": row})
	}
}

func (m *MCP) duplicate(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		row, err := m.docs.Duplicate(ctx, e, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"entity": e.Key, "data": row})
	}
}

func (m *MCP) convert(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		toKey, row, err := m.docs.Convert(ctx, p, e.Key, p.TenantID, id)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"entity": toKey, "data": row})
	}
}

func (m *MCP) del(p httpx.Principal, e resource.Entity) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !p.CanWrite() {
			return errResult(httpx.ErrForbidden)
		}
		id, err := idArg(req)
		if err != nil {
			return errResult(err)
		}
		if err := m.repo.Delete(ctx, e, p.TenantID, id); err != nil {
			return errResult(err)
		}
		return jsonResult(map[string]any{"deleted": id.String()})
	}
}

// --- argument + result helpers ---------------------------------------------

func parseArgs(req *mcp.CallToolRequest) (map[string]any, error) {
	args := map[string]any{}
	raw := req.Params.Arguments
	if len(raw) == 0 {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, httpx.Errorf(http.StatusBadRequest, "bad_request", "invalid arguments: "+err.Error())
	}
	return args, nil
}

func idArg(req *mcp.CallToolRequest) (uuid.UUID, error) {
	args, err := parseArgs(req)
	if err != nil {
		return uuid.Nil, err
	}
	raw, _ := args["id"].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httpx.Errorf(http.StatusBadRequest, "bad_request", "id must be a uuid")
	}
	return id, nil
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func strArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

// errResult reports a business error as a tool result with IsError set, so the
// model sees the message and can self-correct, rather than a transport-level
// failure. Unexpected (non-API) errors are surfaced with their message too.
func errResult(err error) (*mcp.CallToolResult, error) {
	msg := err.Error()
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) {
		msg = apiErr.Message
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil
}

// --- JSON Schema generation (mirrors the field types the UI renders) --------

// objectSchema builds a 2020-12 object schema from property schemas.
func objectSchema(props map[string]map[string]any, required []string) map[string]any {
	p := map[string]any{}
	for k, v := range props {
		p[k] = v
	}
	schema := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// idSchema is the argument schema for tools that take a single record id.
func idSchema() map[string]any {
	return objectSchema(map[string]map[string]any{
		"id": {"type": "string", "description": "record id (uuid)"},
	}, []string{"id"})
}

func listSchema(e resource.Entity) map[string]any {
	filterProps := map[string]any{}
	for _, f := range e.Fields {
		filterProps[f.Key] = map[string]any{"type": "string"}
	}
	return objectSchema(map[string]map[string]any{
		"q":         {"type": "string", "description": "free-text search across text fields"},
		"page":      {"type": "integer", "minimum": 1},
		"page_size": {"type": "integer", "minimum": 1, "maximum": 200},
		"sort":      {"type": "string", "description": "field key to sort by"},
		"dir":       {"type": "string", "enum": []string{"asc", "desc"}},
		"filters": {
			"type":        "object",
			"description": "equality filters keyed by field (\"@empty\" matches blank)",
			"properties":  filterProps,
		},
		"query": {"type": "string", "description": queryHelp(e)},
	}, nil)
}

// queryHelp documents the advanced filter string, listing the entity's views.
func queryHelp(e resource.Entity) string {
	h := "advanced filters as a query string: f_<field>=v (equals; @empty), fin_<field>=a,b (any of), " +
		"fmin_/fmax_<field>=v (inclusive range on dates/numbers). Date tokens: @today, @today+N, @today-N, @month."
	if len(e.Views) > 0 {
		h += " Saved views:"
		for _, v := range e.Views {
			h += fmt.Sprintf(" %q = %s;", v.Label, v.Query)
		}
	}
	return h
}

// writeSchema builds the create/update argument schema from an entity's fields.
// On create, required fields are marked required; on update all fields are
// optional.
func writeSchema(e resource.Entity, create bool) map[string]any {
	props := map[string]any{}
	var required []string
	for _, f := range e.Fields {
		props[f.Key] = fieldSchema(f)
		if create && f.Required {
			required = append(required, f.Key)
		}
	}
	if !create {
		props["id"] = map[string]any{"type": "string", "description": "record id (uuid)"}
		required = []string{"id"}
	}
	return objectSchema(mapToPropMap(props), required)
}

// saveDocSchema builds the header + line-items schema for a document entity.
func saveDocSchema(reg *resource.Registry, e resource.Entity) map[string]any {
	props := map[string]any{
		"id": map[string]any{"type": "string", "description": "record id (uuid); omit to create"},
	}
	for _, f := range e.Fields {
		props[f.Key] = fieldSchema(f)
	}
	if len(e.Children) > 0 {
		child, ok := reg.Get(e.Children[0].Entity)
		if ok {
			itemProps := map[string]any{}
			for _, f := range child.Fields {
				if f.Key == e.Children[0].FK {
					continue // parent link is set by the server
				}
				itemProps[f.Key] = fieldSchema(f)
			}
			props["lines"] = map[string]any{
				"type":        "array",
				"description": e.Children[0].Label + " — replaces the existing line items",
				"items":       map[string]any{"type": "object", "properties": itemProps},
			}
		}
		for _, c := range e.Children[1:] {
			child, ok := reg.Get(c.Entity)
			if !ok {
				continue
			}
			itemProps := map[string]any{}
			for _, f := range child.Fields {
				if f.Key != c.FK {
					itemProps[f.Key] = fieldSchema(f)
				}
			}
			props[c.Entity] = map[string]any{
				"type":        "array",
				"description": c.Label + " — when given, replaces the existing rows; omit to keep them",
				"items":       map[string]any{"type": "object", "properties": itemProps},
			}
		}
	}
	return objectSchema(mapToPropMap(props), nil)
}

// mapToPropMap adapts a map[string]any of property schemas to the shape
// objectSchema expects.
func mapToPropMap(in map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(in))
	for k, v := range in {
		if mv, ok := v.(map[string]any); ok {
			out[k] = mv
		}
	}
	return out
}

func fieldSchema(f resource.Field) map[string]any {
	var s map[string]any
	switch f.Type {
	case resource.TypeInt:
		s = map[string]any{"type": "integer"}
	case resource.TypeNumber, resource.TypeMoney, resource.TypePercent:
		s = map[string]any{"type": "number"}
	case resource.TypeBool:
		s = map[string]any{"type": "boolean"}
	case resource.TypeDate:
		s = map[string]any{"type": "string", "format": "date"}
	case resource.TypeDateTime:
		s = map[string]any{"type": "string", "format": "date-time"}
	case resource.TypeEmail:
		s = map[string]any{"type": "string", "format": "email"}
	case resource.TypeSelect:
		s = map[string]any{"type": "string"}
		if len(f.Options) > 0 {
			s["enum"] = f.Options
		}
	default:
		s = map[string]any{"type": "string"}
	}
	desc := f.Label
	switch f.Type {
	case resource.TypeRef:
		desc += fmt.Sprintf(" (id of a %s record)", f.RefKey)
	case resource.TypeLookup:
		desc += fmt.Sprintf(" (a %s value from %s)", f.LabelField, f.RefKey)
	}
	if f.ReadOnly {
		desc += " [read-only/computed — usually set by the server]"
	}
	if f.Help != "" {
		desc += " — " + f.Help
	}
	s["description"] = desc
	return s
}
