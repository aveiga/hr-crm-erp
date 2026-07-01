package resource

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
)

// DocService handles "document" entities (a header with line items): quotes,
// sales orders, invoices, purchase orders, vendor bills. It saves the header
// and its lines atomically, recomputes totals (net + markup + VAT), fills
// derived fields (e.g. an invoice's legal entity name/address), and converts
// one document type into the next without re-entering data.
type DocService struct {
	reg  *Registry
	repo *Repo
	pool *pgxpool.Pool
}

func NewDocService(reg *Registry, repo *Repo) *DocService {
	return &DocService{reg: reg, repo: repo, pool: repo.pool}
}

// IsDocument reports whether an entity has line items (is a header document).
func (e Entity) IsDocument() bool { return len(e.Children) > 0 }

// Save upserts a document header together with its line items in one
// transaction. id == nil creates a new document.
func (s *DocService) Save(ctx context.Context, e Entity, tenantID uuid.UUID, id *uuid.UUID, header map[string]any, lines []map[string]any) (map[string]any, error) {
	if !e.IsDocument() {
		return nil, httpx.Errorf(http.StatusBadRequest, "not_document", e.Label+" is not a line-item document")
	}
	child, ok := s.reg.Get(e.Children[0].Entity)
	if !ok {
		return nil, fmt.Errorf("child entity %s missing", e.Children[0].Entity)
	}
	fk := e.Children[0].FK

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Upsert header (writable fields only; computed/derived set below).
	var pid uuid.UUID
	if id == nil {
		pid, err = s.insertHeader(ctx, tx, e, tenantID, header)
	} else {
		pid = *id
		err = s.updateHeader(ctx, tx, e, tenantID, pid, header)
	}
	if err != nil {
		return nil, err
	}

	// Derived fields (e.g. invoice legal entity name/address from the chosen
	// legal entity) and computed money totals are system-managed.
	derived := s.deriveFields(ctx, tx, e, tenantID, header)
	if err := s.setColumns(ctx, tx, e, tenantID, pid, derived); err != nil {
		return nil, err
	}

	if err := s.replaceChildren(ctx, tx, child, tenantID, fk, pid, lines); err != nil {
		return nil, err
	}

	totals := ComputeTotals(e, header, lines)
	if err := s.setColumns(ctx, tx, e, tenantID, pid, totals); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, e, tenantID, pid)
}

// LoadLines returns the line items of a document, ordered by line number.
func (s *DocService) LoadLines(ctx context.Context, parent Entity, tenantID, parentID uuid.UUID) ([]map[string]any, error) {
	if !parent.IsDocument() {
		return nil, nil
	}
	child, ok := s.reg.Get(parent.Children[0].Entity)
	if !ok {
		return nil, nil
	}
	res, err := s.repo.List(ctx, child, tenantID, ListOptions{
		Page: 1, PageSize: 500, Sort: "line_no",
		Filters: map[string]string{parent.Children[0].FK: parentID.String()},
	})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// --- conversions -----------------------------------------------------------

// Convert turns a quote into a sales order, or a sales order into an invoice,
// copying the header and line items. It returns the new document.
func (s *DocService) Convert(ctx context.Context, fromKey string, tenantID, sourceID uuid.UUID) (string, map[string]any, error) {
	switch fromKey {
	case "quotes":
		return s.convert(ctx, "quotes", "sales_orders", tenantID, sourceID, func(src map[string]any) map[string]any {
			return map[string]any{
				"legal_entity_id":    src["legal_entity_id"],
				"account_id":         src["account_id"],
				"currency":           src["currency"],
				"vat_pct":            src["vat_pct"],
				"markup_pct":         src["markup_pct"],
				"payment_terms_days": src["payment_terms_days"],
				"quote_description":  src["description"],
				"quote_id":           fmt.Sprint(src["id"]),
				"order_date":         today(),
				"status":             "Open",
			}
		})
	case "sales_orders":
		return s.convert(ctx, "sales_orders", "invoices", tenantID, sourceID, func(src map[string]any) map[string]any {
			terms := toInt64(src["payment_terms_days"])
			return map[string]any{
				"legal_entity_id":    src["legal_entity_id"],
				"account_id":         src["account_id"],
				"currency":           src["currency"],
				"vat_pct":            src["vat_pct"],
				"markup_pct":         src["markup_pct"],
				"payment_terms_days": src["payment_terms_days"],
				"quote_description":  src["quote_description"],
				"sales_order_id":     fmt.Sprint(src["id"]),
				"invoice_date":       today(),
				"due_date":           todayPlus(terms),
				"status":             "Draft",
			}
		})
	default:
		return "", nil, httpx.Errorf(http.StatusBadRequest, "bad_convert", "can only convert quotes or sales orders")
	}
}

func (s *DocService) convert(ctx context.Context, fromKey, toKey string, tenantID, sourceID uuid.UUID, mapHeader func(map[string]any) map[string]any) (string, map[string]any, error) {
	from, ok := s.reg.Get(fromKey)
	if !ok {
		return "", nil, fmt.Errorf("entity %s missing", fromKey)
	}
	to, ok := s.reg.Get(toKey)
	if !ok {
		return "", nil, fmt.Errorf("entity %s missing", toKey)
	}
	src, err := s.repo.Get(ctx, from, tenantID, sourceID)
	if err != nil {
		return "", nil, err
	}
	srcLines, err := s.LoadLines(ctx, from, tenantID, sourceID)
	if err != nil {
		return "", nil, err
	}
	lines := make([]map[string]any, 0, len(srcLines))
	for _, l := range srcLines {
		lines = append(lines, map[string]any{
			"sku":         l["sku"],
			"description": l["description"],
			"qty":         l["qty"],
			"unit_price":  l["unit_price"],
		})
	}
	created, err := s.Save(ctx, to, tenantID, nil, mapHeader(src), lines)
	if err != nil {
		return "", nil, err
	}
	return toKey, created, nil
}

// --- helpers ---------------------------------------------------------------

func (s *DocService) insertHeader(ctx context.Context, tx pgx.Tx, e Entity, tenantID uuid.UUID, header map[string]any) (uuid.UUID, error) {
	cols := []string{"tenant_id"}
	ph := []string{"$1"}
	args := []any{tenantID}
	n := 2
	for _, f := range e.Fields {
		if f.ReadOnly {
			continue
		}
		raw, present := header[f.Key]
		if f.Required && (!present || isEmpty(raw)) {
			return uuid.Nil, httpx.Errorf(http.StatusBadRequest, "validation", f.Label+" is required")
		}
		if !present {
			continue
		}
		val, err := coerce(f, raw)
		if err != nil {
			return uuid.Nil, err
		}
		cols = append(cols, quoteIdent(f.Key))
		ph = append(ph, fmt.Sprintf("$%d", n))
		args = append(args, val)
		n++
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id", e.Table(), strings.Join(cols, ","), strings.Join(ph, ","))
	var id uuid.UUID
	if err := tx.QueryRow(ctx, q, args...).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *DocService) updateHeader(ctx context.Context, tx pgx.Tx, e Entity, tenantID, id uuid.UUID, header map[string]any) error {
	sets := []string{"updated_at = now()"}
	args := []any{tenantID, id}
	n := 3
	for _, f := range e.Fields {
		if f.ReadOnly {
			continue
		}
		raw, present := header[f.Key]
		if !present {
			continue
		}
		if f.Required && isEmpty(raw) {
			return httpx.Errorf(http.StatusBadRequest, "validation", f.Label+" is required")
		}
		val, err := coerce(f, raw)
		if err != nil {
			return err
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdent(f.Key), n))
		args = append(args, val)
		n++
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE tenant_id=$1 AND id=$2", e.Table(), strings.Join(sets, ",")), args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

// setColumns directly writes system-managed (read-only/computed) columns.
func (s *DocService) setColumns(ctx context.Context, tx pgx.Tx, e Entity, tenantID, id uuid.UUID, vals map[string]any) error {
	if len(vals) == 0 {
		return nil
	}
	sets := []string{}
	args := []any{tenantID, id}
	n := 3
	for _, f := range e.Fields {
		v, ok := vals[f.Key]
		if !ok {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdent(f.Key), n))
		args = append(args, v)
		n++
	}
	if len(sets) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE tenant_id=$1 AND id=$2", e.Table(), strings.Join(sets, ",")), args...)
	return err
}

// replaceChildren deletes and re-inserts a parent's child rows. It works for
// any child entity (document line items, account contacts, …): it writes the
// child's own fields generically, and additionally computes line_no (sequence)
// and line_net (qty × unit_price) when the child has those columns.
func (s *DocService) replaceChildren(ctx context.Context, tx pgx.Tx, child Entity, tenantID uuid.UUID, fk string, parentID uuid.UUID, rows []map[string]any) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id=$1 AND %s=$2", child.Table(), quoteIdent(fk)), tenantID, parentID.String()); err != nil {
		return err
	}
	_, hasLineNo := child.field("line_no")
	_, hasLineNet := child.field("line_net")
	seq := 0
	for _, row := range rows {
		if childRowEmpty(child, fk, row) {
			continue // skip blank rows
		}
		seq++
		cols := []string{"tenant_id", quoteIdent(fk)}
		args := []any{tenantID, parentID.String()}
		n := 3
		add := func(col string, val any) {
			cols = append(cols, quoteIdent(col))
			args = append(args, val)
			n++
		}
		for _, f := range child.Fields {
			switch {
			case f.Key == fk:
				continue
			case f.Key == "line_no" && hasLineNo:
				add("line_no", seq)
			case f.Key == "line_net" && hasLineNet:
				add("line_net", toFloat64(row["qty"])*toFloat64(row["unit_price"]))
			case f.ReadOnly:
				continue // other computed fields are system-managed
			default:
				raw, present := row[f.Key]
				if f.Required && isEmpty(raw) {
					return httpx.Errorf(http.StatusBadRequest, "validation", child.Label+": "+f.Label+" is required")
				}
				if !present {
					continue
				}
				val, err := coerce(f, raw)
				if err != nil {
					return err
				}
				add(f.Key, val)
			}
		}
		ph := make([]string, len(cols))
		for i := range cols {
			ph[i] = fmt.Sprintf("$%d", i+1)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", child.Table(), strings.Join(cols, ","), strings.Join(ph, ",")), args...); err != nil {
			return err
		}
	}
	return nil
}

// childRowEmpty reports whether a child row has no meaningful user data (so we
// skip stray empty rows from the editor).
func childRowEmpty(child Entity, fk string, row map[string]any) bool {
	for _, f := range child.Fields {
		if f.Key == fk || f.ReadOnly || f.Key == "line_no" || f.Key == "line_net" {
			continue
		}
		if v, ok := row[f.Key]; ok && !isEmpty(v) {
			return false
		}
	}
	return true
}

// deriveFields computes system-filled, read-only fields from the header's refs.
func (s *DocService) deriveFields(ctx context.Context, tx pgx.Tx, e Entity, tenantID uuid.UUID, header map[string]any) map[string]any {
	out := map[string]any{}
	// Invoices: copy the chosen legal entity's name + address onto the invoice.
	if _, ok := e.field("legal_entity_name"); ok {
		if leID := asStr(header["legal_entity_id"]); leID != "" {
			if le, ok := s.reg.Get("legal_entities"); ok {
				if id, err := uuid.Parse(leID); err == nil {
					var name, addr *string
					_ = tx.QueryRow(ctx,
						fmt.Sprintf("SELECT legal_name, billing_address FROM %s WHERE tenant_id=$1 AND id=$2", le.Table()),
						tenantID, id).Scan(&name, &addr)
					if name != nil {
						out["legal_entity_name"] = *name
					}
					if _, ok := e.field("legal_entity_billing_address"); ok && addr != nil {
						out["legal_entity_billing_address"] = *addr
					}
				}
			}
		}
	}
	return out
}

// ComputeTotals derives net/markup/VAT/grand (and balance) from line items and
// the header's markup_pct / vat_pct / paid_total. Only fields the entity has
// are returned.
func ComputeTotals(e Entity, header map[string]any, lines []map[string]any) map[string]any {
	var net float64
	for _, ln := range lines {
		net += toFloat64(ln["qty"]) * toFloat64(ln["unit_price"])
	}
	markupPct := toFloat64(header["markup_pct"])
	vatPct := toFloat64(header["vat_pct"])
	markup := net * markupPct / 100
	base := net + markup
	vat := base * vatPct / 100
	grand := base + vat

	out := map[string]any{}
	set := func(k string, v float64) {
		if _, ok := e.field(k); ok {
			out[k] = round2(v)
		}
	}
	set("net_total", net)
	set("markup_total", markup)
	set("vat_total", vat)
	set("grand_total", grand)
	if _, ok := e.field("balance"); ok {
		out["balance"] = round2(grand - toFloat64(header["paid_total"]))
	}
	return out
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var f float64
		_, _ = fmt.Sscanf(n, "%g", &f)
		return f
	default:
		return 0
	}
}

func toInt64(v any) int64 { return int64(toFloat64(v)) }

func asStr(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func today() string { return time.Now().Format("2006-01-02") }
func todayPlus(days int64) string {
	return time.Now().AddDate(0, 0, int(days)).Format("2006-01-02")
}
