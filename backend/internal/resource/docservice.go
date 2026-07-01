package resource

import (
	"context"
	"errors"
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

	// Keep the linked opportunity's pipeline value in sync with the quote it
	// spawned, so the CRM forecast reflects the real numbers.
	if e.Key == "quotes" {
		s.syncOpportunityAmount(ctx, tenantID, header, totals)
	}
	return s.repo.Get(ctx, e, tenantID, pid)
}

// syncOpportunityAmount writes a quote's net total (and probability-weighted
// value) back onto its linked opportunity. Best-effort: a missing or deleted
// opportunity is ignored, matching the tolerant style of deriveFields.
func (s *DocService) syncOpportunityAmount(ctx context.Context, tenantID uuid.UUID, header, totals map[string]any) {
	oppID := asStr(header["opportunity_id"])
	if oppID == "" {
		return
	}
	id, err := uuid.Parse(oppID)
	if err != nil {
		return
	}
	opps, ok := s.reg.Get("opportunities")
	if !ok {
		return
	}
	opp, err := s.repo.Get(ctx, opps, tenantID, id)
	if err != nil {
		return
	}
	net := toFloat64(totals["net_total"])
	prob := toFloat64(opp["probability_pct"])
	_, _ = s.repo.Update(ctx, opps, tenantID, id, map[string]any{
		"amount_net":      net,
		"weighted_amount": round2(net * prob / 100),
	})
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

// Convert advances a record to the next stage of the pipeline, carrying the
// data across so nothing is re-entered:
//
//	Lead → Account + Opportunity   (qualify a lead into the CRM funnel)
//	Opportunity → Quote            (hand a deal off to the ERP/AR side)
//	Quote → Sales Order → Invoice  (documents: header + line items copied)
//
// It returns the target entity key and the newly created row. The caller's
// section access to the *target* group is enforced, so a CRM-only user can't
// mint an ERP document via an opportunity conversion.
func (s *DocService) Convert(ctx context.Context, p httpx.Principal, fromKey string, tenantID, sourceID uuid.UUID) (string, map[string]any, error) {
	switch fromKey {
	case "leads":
		if err := s.requireGroup(p, "accounts", "opportunities"); err != nil {
			return "", nil, err
		}
		return s.convertLead(ctx, tenantID, sourceID)
	case "opportunities":
		if err := s.requireGroup(p, "quotes"); err != nil {
			return "", nil, err
		}
		return s.convertOpportunity(ctx, tenantID, sourceID)
	case "quotes":
		if err := s.requireGroup(p, "sales_orders"); err != nil {
			return "", nil, err
		}
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
		if err := s.requireGroup(p, "invoices"); err != nil {
			return "", nil, err
		}
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
		return "", nil, httpx.Errorf(http.StatusBadRequest, "bad_convert", "this record type cannot be converted")
	}
}

// requireGroup enforces that the caller can access every target entity's
// section before a conversion creates rows there.
func (s *DocService) requireGroup(p httpx.Principal, entityKeys ...string) error {
	for _, k := range entityKeys {
		if e, ok := s.reg.Get(k); ok && !p.CanAccessGroup(e.Group) {
			return httpx.ErrForbidden
		}
	}
	return nil
}

// convertLead qualifies a lead into the CRM funnel: it lands the lead on an
// Account (reusing an existing account of the same name, else creating one),
// records the lead's named person as a Contact, opens an Opportunity linked to
// the account, and stamps the lead as Converted so it can't be funneled twice.
func (s *DocService) convertLead(ctx context.Context, tenantID, sourceID uuid.UUID) (string, map[string]any, error) {
	leads, ok := s.reg.Get("leads")
	if !ok {
		return "", nil, fmt.Errorf("entity leads missing")
	}
	src, err := s.repo.Get(ctx, leads, tenantID, sourceID)
	if err != nil {
		return "", nil, err
	}
	if asStr(src["converted_opportunity_id"]) != "" {
		return "", nil, httpx.Errorf(http.StatusConflict, "already_converted", "this lead has already been converted")
	}

	accounts, ok := s.reg.Get("accounts")
	if !ok {
		return "", nil, fmt.Errorf("entity accounts missing")
	}
	// The lead's named person, as a contact row (nil when the lead has no
	// last name — the minimum a contact needs).
	var contact map[string]any
	if last := asStr(src["contact_last_name"]); last != "" {
		contact = map[string]any{
			"first_name": src["contact_first_name"],
			"last_name":  last,
			"email":      src["email"],
			"phone":      src["phone"],
			"owner":      src["owner"],
		}
	}

	// Reuse an account with the same name if one exists, merging the lead's
	// contact into it; otherwise create the account with the contact inline.
	var accountID string
	existingID, found, err := s.findAccountByName(ctx, tenantID, asStr(src["company"]))
	if err != nil {
		return "", nil, err
	}
	if found {
		accountID = existingID.String()
		if contact != nil {
			if err := s.mergeContact(ctx, tenantID, existingID, contact); err != nil {
				return "", nil, err
			}
		}
	} else {
		var contacts []map[string]any
		if contact != nil {
			contacts = append(contacts, contact)
		}
		account, err := s.Save(ctx, accounts, tenantID, nil, map[string]any{
			"account_name": src["company"],
			"type":         "Prospect",
			"country":      src["country"],
			"phone":        src["phone"],
			"email":        src["email"],
			"owner":        src["owner"],
			"created_date": today(),
			"notes":        src["notes"],
		}, contacts)
		if err != nil {
			return "", nil, err
		}
		accountID = fmt.Sprint(account["id"])
	}

	// Opportunity at the top of the pipeline, linked to the account.
	opps, ok := s.reg.Get("opportunities")
	if !ok {
		return "", nil, fmt.Errorf("entity opportunities missing")
	}
	opp, err := s.repo.Create(ctx, opps, tenantID, map[string]any{
		"account_id":       accountID,
		"opportunity_name": asStr(src["company"]) + " — Opportunity",
		"stage":            "Prospecting",
		"owner":            src["owner"],
		"created_date":     today(),
	})
	if err != nil {
		return "", nil, err
	}

	// Stamp the lead so it's out of the funnel and links to what it became.
	if _, err := s.repo.Update(ctx, leads, tenantID, sourceID, map[string]any{
		"status":                   "Converted",
		"converted_account_id":     accountID,
		"converted_opportunity_id": fmt.Sprint(opp["id"]),
	}); err != nil {
		return "", nil, err
	}
	return "opportunities", opp, nil
}

// findAccountByName looks up a tenant's account by a case-insensitive name
// match, returning the earliest-created one when several share a name.
func (s *DocService) findAccountByName(ctx context.Context, tenantID uuid.UUID, name string) (uuid.UUID, bool, error) {
	if strings.TrimSpace(name) == "" {
		return uuid.Nil, false, nil
	}
	accounts, ok := s.reg.Get("accounts")
	if !ok {
		return uuid.Nil, false, fmt.Errorf("entity accounts missing")
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT id FROM %s WHERE tenant_id=$1 AND lower(account_name)=lower($2) ORDER BY created_at LIMIT 1", accounts.Table()),
		tenantID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// mergeContact adds a contact to an existing account unless that person is
// already on file — matched by email when present, else by name. Contacts are
// inserted directly (not via the account's document Save, which would replace
// the whole child collection).
func (s *DocService) mergeContact(ctx context.Context, tenantID, accountID uuid.UUID, contact map[string]any) error {
	contacts, ok := s.reg.Get("contacts")
	if !ok {
		return fmt.Errorf("entity contacts missing")
	}
	existing, err := s.repo.List(ctx, contacts, tenantID, ListOptions{
		Page: 1, PageSize: 200,
		Filters: map[string]string{"account_id": accountID.String()},
	})
	if err != nil {
		return err
	}
	email := strings.ToLower(asStr(contact["email"]))
	fullName := contactFullName(contact)
	for _, c := range existing.Data {
		if email != "" {
			if strings.ToLower(asStr(c["email"])) == email {
				return nil // same person, already on the account
			}
			continue
		}
		if fullName != "" && contactFullName(c) == fullName {
			return nil
		}
	}
	input := map[string]any{"account_id": accountID.String()}
	for k, v := range contact {
		input[k] = v
	}
	_, err = s.repo.Create(ctx, contacts, tenantID, input)
	return err
}

// contactFullName is a lowercased "first last" key for matching contacts that
// have no email to compare on.
func contactFullName(c map[string]any) string {
	return strings.ToLower(strings.TrimSpace(asStr(c["first_name"]) + " " + asStr(c["last_name"])))
}

// convertOpportunity hands a deal off to the ERP side by opening a Quote
// pre-filled with the account, its billing legal entity, and a back-reference
// to the opportunity. The quote starts with no line items. Opportunities may be
// quoted more than once, so this is intentionally repeatable.
func (s *DocService) convertOpportunity(ctx context.Context, tenantID, sourceID uuid.UUID) (string, map[string]any, error) {
	opps, ok := s.reg.Get("opportunities")
	if !ok {
		return "", nil, fmt.Errorf("entity opportunities missing")
	}
	src, err := s.repo.Get(ctx, opps, tenantID, sourceID)
	if err != nil {
		return "", nil, err
	}

	header := map[string]any{
		"account_id":     src["account_id"],
		"opportunity_id": fmt.Sprint(src["id"]),
		"description":    src["opportunity_name"],
		"quote_date":     today(),
		"status":         "Draft",
	}
	// Default the quote's legal entity to the account's billing entity.
	if accID := asStr(src["account_id"]); accID != "" {
		if accounts, ok := s.reg.Get("accounts"); ok {
			if id, err := uuid.Parse(accID); err == nil {
				if acc, err := s.repo.Get(ctx, accounts, tenantID, id); err == nil {
					if le := acc["legal_entity_id"]; le != nil {
						header["legal_entity_id"] = le
					}
				}
			}
		}
	}

	quotes, ok := s.reg.Get("quotes")
	if !ok {
		return "", nil, fmt.Errorf("entity quotes missing")
	}
	created, err := s.Save(ctx, quotes, tenantID, nil, header, nil)
	if err != nil {
		return "", nil, err
	}
	return "quotes", created, nil
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
