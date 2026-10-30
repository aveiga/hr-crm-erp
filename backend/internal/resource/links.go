package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// --- duplicate ---------------------------------------------------------------

// docDateField is the "document date" reset to today on a duplicate.
var docDateField = map[string]string{
	"quotes":          "quote_date",
	"invoices":        "invoice_date",
	"purchase_orders": "po_date",
	"vendor_bills":    "bill_date",
}

// CanDuplicate reports whether an entity supports Duplicate: line-item
// documents (quotes, orders, service agreements, POs, bills).
func (s *DocService) CanDuplicate(e Entity) bool {
	if !e.IsDocument() {
		return false
	}
	child, ok := s.reg.Get(e.Children[0].Entity)
	if !ok {
		return false
	}
	_, hasNet := child.field("line_net")
	return hasNet
}

// Duplicate copies a document — header, line items and every secondary child
// collection — into a new draft: the status resets to its first option, the
// document date to today, payments to zero, and the title gets " (copy)".
// Upstream links (account, opportunity, quote, …) are kept.
func (s *DocService) Duplicate(ctx context.Context, e Entity, tenantID, id uuid.UUID) (map[string]any, error) {
	if !s.CanDuplicate(e) {
		return nil, httpx.Errorf(http.StatusBadRequest, "bad_duplicate", e.Label+" cannot be duplicated")
	}
	src, err := s.repo.Get(ctx, e, tenantID, id)
	if err != nil {
		return nil, err
	}
	header := map[string]any{}
	for _, f := range e.Fields {
		if !f.ReadOnly {
			header[f.Key] = src[f.Key]
		}
	}
	if f, ok := e.field("status"); ok && len(f.Options) > 0 {
		header["status"] = f.Options[0]
	}
	if k, ok := docDateField[e.Key]; ok {
		header[k] = today()
	}
	if _, ok := e.field("paid_total"); ok {
		header["paid_total"] = 0
	}
	if t := asStr(src[e.TitleField]); t != "" {
		header[e.TitleField] = t + " (copy)"
	}

	lines, err := s.LoadLines(ctx, e, tenantID, id)
	if err != nil {
		return nil, err
	}
	extra, err := s.LoadChildren(ctx, e, tenantID, id)
	if err != nil {
		return nil, err
	}
	// Copies are new rows: drop ids (replaceChildren keeps given ids). Derived
	// child state (paid amounts, statuses) is read-only and recomputed.
	for _, l := range lines {
		delete(l, "id")
	}
	for _, rows := range extra {
		for _, r := range rows {
			delete(r, "id")
		}
	}
	// Keep the upstream quote link (read-only, so it's a system value).
	system := map[string]any{}
	if _, ok := e.field("quote_id"); ok && e.Key != "quotes" {
		system["quote_id"] = src["quote_id"]
	}
	return s.save(ctx, e, tenantID, nil, header, lines, extra, system)
}

// --- related records -----------------------------------------------------------

// RelatedRecord is a compact pointer to another record, with its status.
type RelatedRecord struct {
	Entity string `json:"entity"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
}

// RelatedLink is one upstream reference held by the record (e.g. its quote).
type RelatedLink struct {
	Field  string        `json:"field"`
	Label  string        `json:"label"`
	Record RelatedRecord `json:"record"`
}

// RelatedGroup lists the records of one entity that reference this record
// through one field (e.g. the sales orders created from a quote).
type RelatedGroup struct {
	Entity string          `json:"entity"`
	Field  string          `json:"field"`
	Label  string          `json:"label"`
	Total  int             `json:"total"`
	Rows   []RelatedRecord `json:"rows"`
}

// Related returns a record's upstream links (the refs it holds) and its
// downstream records (rows in other entities that reference it), each with
// title and status, so the UI can navigate both ways along quote → sales order
// → service agreement. Only entities in sections the caller can access are
// included; child collections edited inline (lines, contacts) are skipped.
func (s *DocService) Related(ctx context.Context, p httpx.Principal, e Entity, tenantID, id uuid.UUID) ([]RelatedLink, []RelatedGroup, error) {
	row, err := s.repo.Get(ctx, e, tenantID, id)
	if err != nil {
		return nil, nil, err
	}

	upstream := []RelatedLink{}
	for _, f := range e.Fields {
		if f.Type != TypeRef || asStr(row[f.Key]) == "" {
			continue
		}
		target, ok := s.reg.Get(f.RefKey)
		if !ok || !p.CanAccessGroup(target.Group) {
			continue
		}
		tid, err := uuid.Parse(asStr(row[f.Key]))
		if err != nil {
			continue
		}
		tr, err := s.repo.Get(ctx, target, tenantID, tid)
		if err != nil {
			continue // dangling reference
		}
		upstream = append(upstream, RelatedLink{Field: f.Key, Label: f.Label, Record: relatedRecord(target, tr)})
	}

	inline := map[string]bool{}
	for _, c := range e.Children {
		inline[c.Entity] = true
	}
	downstream := []RelatedGroup{}
	for _, other := range s.reg.All() {
		if other.Hidden || inline[other.Key] || !p.CanAccessGroup(other.Group) {
			continue
		}
		for _, f := range other.Fields {
			if f.Type != TypeRef || f.RefKey != e.Key {
				continue
			}
			res, err := s.repo.List(ctx, other, tenantID, ListOptions{
				Page: 1, PageSize: 20, Desc: true,
				Filters: map[string]string{f.Key: id.String()},
			})
			if err != nil {
				return nil, nil, err
			}
			if res.Total == 0 {
				continue
			}
			g := RelatedGroup{Entity: other.Key, Field: f.Key, Label: other.Plural, Total: res.Total}
			if refsTo(other, e.Key) > 1 {
				g.Label = fmt.Sprintf("%s (%s)", other.Plural, f.Label)
			}
			for _, r := range res.Data {
				g.Rows = append(g.Rows, relatedRecord(other, r))
			}
			downstream = append(downstream, g)
		}
	}
	return upstream, downstream, nil
}

func relatedRecord(e Entity, row map[string]any) RelatedRecord {
	rr := RelatedRecord{Entity: e.Key, ID: asStr(row["id"]), Title: asStr(row[e.TitleField])}
	if rr.Title == "" {
		rr.Title = e.Label
	}
	for _, k := range []string{"status", "stage"} {
		if _, ok := e.field(k); ok {
			rr.Status = asStr(row[k])
			break
		}
	}
	return rr
}

// refsTo counts an entity's ref fields pointing at target.
func refsTo(e Entity, target string) int {
	n := 0
	for _, f := range e.Fields {
		if f.Type == TypeRef && f.RefKey == target {
			n++
		}
	}
	return n
}
