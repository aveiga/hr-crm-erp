package resource

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// Service agreement payments: the payment schedule (due installments), the
// payments registered against it, and the agreement's derived payment
// position (paid total, balance, payment status, next due date).

// prepareSchedule validates a service agreement's payment schedule against its
// grand total. Rows without an amount share what the explicit amounts leave
// (evenly, the last row absorbing rounding); the amounts must then add up to
// the total. Rows also get their display label. Blank rows are dropped.
func prepareSchedule(rows []map[string]any, grand float64) ([]map[string]any, error) {
	var kept []map[string]any
	explicit, blanks := 0.0, 0
	for _, r := range rows {
		if isEmpty(r["due_date"]) && isEmpty(r["amount"]) && isEmpty(r["description"]) {
			continue
		}
		if isEmpty(r["due_date"]) {
			return nil, httpx.Errorf(http.StatusBadRequest, "validation", "Payment Schedule: every payment needs a due date")
		}
		if isEmpty(r["amount"]) {
			blanks++
		} else {
			explicit += toFloat64(r["amount"])
		}
		kept = append(kept, r)
	}
	if len(kept) == 0 {
		return kept, nil
	}
	if blanks > 0 {
		share := math.Floor((grand-explicit)/float64(blanks)*100) / 100
		left := round2(grand - explicit - share*float64(blanks-1))
		n := 0
		for _, r := range kept {
			if isEmpty(r["amount"]) {
				n++
				if n == blanks {
					r["amount"] = left
				} else {
					r["amount"] = share
				}
			}
		}
	}
	sum := 0.0
	for _, r := range kept {
		sum += toFloat64(r["amount"])
		r["label"] = scheduleLabel(r)
	}
	if math.Abs(round2(sum)-round2(grand)) > 0.005 {
		return nil, httpx.Errorf(http.StatusBadRequest, "schedule_mismatch",
			fmt.Sprintf("Payment Schedule adds up to %.2f but the agreement total is %.2f", sum, grand))
	}
	return kept, nil
}

// scheduleLabel is an installment's option label: "31-03-2026 · 1000.00 · Q1".
func scheduleLabel(r map[string]any) string {
	d := asStr(r["due_date"])
	if len(d) >= 10 {
		d = d[8:10] + "-" + d[5:7] + "-" + d[0:4]
	}
	parts := []string{d, fmt.Sprintf("%.2f", toFloat64(r["amount"]))}
	if desc := asStr(r["description"]); desc != "" {
		parts = append(parts, desc)
	}
	return strings.Join(parts, " · ")
}

// registerPaymentHooks keeps agreements in step with payments written through
// any path (REST, MCP), and quotes with their agreements' status.
func (s *DocService) registerPaymentHooks() {
	s.repo.Hook("payments", WriteHooks{Before: s.beforePayment, After: s.afterPayment})
	s.repo.Hook("invoices", WriteHooks{After: func(ctx context.Context, tenantID uuid.UUID, before, after map[string]any) {
		// Generic writes/deletes of an agreement (doc saves sync themselves).
		for _, row := range []map[string]any{before, after} {
			if row != nil {
				s.syncQuoteAgreementStatus(ctx, tenantID, row["quote_id"])
			}
		}
	}})
}

// beforePayment checks a payment's installment belongs to its agreement (and
// fills the agreement from the installment), and defaults a blank amount to
// the installment's outstanding amount, else the agreement's balance.
func (s *DocService) beforePayment(ctx context.Context, tenantID uuid.UUID, row map[string]any) (map[string]any, error) {
	out := map[string]any{}
	load := s.repo.poolLoader(ctx, tenantID)
	var inst map[string]any
	if asStr(row["schedule_id"]) != "" {
		if inst = load("payment_schedule", row["schedule_id"]); inst == nil {
			return nil, httpx.Errorf(http.StatusBadRequest, "validation", "Scheduled Payment not found")
		}
		agreement := asStr(inst["invoice_id"])
		switch asStr(row["invoice_id"]) {
		case "":
			out["invoice_id"] = agreement
		case agreement:
		default:
			return nil, httpx.Errorf(http.StatusBadRequest, "validation", "the Scheduled Payment belongs to a different Service Agreement")
		}
	}
	if isEmpty(row["amount"]) {
		switch {
		case inst != nil:
			out["amount"] = round2(math.Max(0, toFloat64(inst["amount"])-toFloat64(inst["paid_amount"])))
		case asStr(row["invoice_id"]) != "":
			if sa := load("invoices", row["invoice_id"]); sa != nil {
				out["amount"] = toFloat64(sa["balance"])
			}
		}
	}
	return out, nil
}

func (s *DocService) afterPayment(ctx context.Context, tenantID uuid.UUID, before, after map[string]any) {
	seen := map[string]bool{}
	for _, row := range []map[string]any{before, after} {
		if row == nil {
			continue
		}
		id := asStr(row["invoice_id"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if uid, err := uuid.Parse(id); err == nil {
			_ = s.recomputeAgreement(ctx, tenantID, uid)
		}
	}
}

// recomputeAgreement derives a service agreement's payment position from its
// schedule and payments. Payments linked to an installment pay it first (any
// excess joins the unallocated pool); unallocated payments then settle the
// open installments in due-date order. paid_total is the payments' sum — an
// agreement with no payments keeps the paid total it was given before
// payments were tracked.
func (s *DocService) recomputeAgreement(ctx context.Context, tenantID, id uuid.UUID) error {
	sa, ok := s.reg.Get("invoices")
	if !ok {
		return nil
	}
	sched, ok1 := s.reg.Get("payment_schedule")
	pays, ok2 := s.reg.Get("payments")
	if !ok1 || !ok2 {
		return nil
	}
	agreement, err := s.repo.Get(ctx, sa, tenantID, id)
	if err != nil {
		return nil // deleted meanwhile
	}
	rowsRes, err := s.repo.List(ctx, sched, tenantID, ListOptions{Page: 1, PageSize: 200, Sort: "line_no",
		Filters: map[string]string{"invoice_id": id.String()}})
	if err != nil {
		return err
	}
	payRes, err := s.repo.List(ctx, pays, tenantID, ListOptions{Page: 1, PageSize: 200,
		Filters: map[string]string{"invoice_id": id.String()}})
	if err != nil {
		return err
	}
	rows := rowsRes.Data
	sort.SliceStable(rows, func(i, j int) bool { return asStr(rows[i]["due_date"]) < asStr(rows[j]["due_date"]) })

	byRow := map[string]float64{}
	for _, r := range rows {
		byRow[asStr(r["id"])] = 0
	}
	paidTotal, pool := 0.0, 0.0
	for _, p := range payRes.Data {
		amt := toFloat64(p["amount"])
		paidTotal += amt
		if _, ok := byRow[asStr(p["schedule_id"])]; ok {
			byRow[asStr(p["schedule_id"])] += amt
		} else {
			pool += amt
		}
	}
	for _, r := range rows {
		id := asStr(r["id"])
		if excess := byRow[id] - toFloat64(r["amount"]); excess > 0 {
			byRow[id] -= excess
			pool += excess
		}
	}

	scheduled, paidCount := 0.0, 0
	var nextDue any
	for _, r := range rows {
		rid := asStr(r["id"])
		amount := toFloat64(r["amount"])
		if need := amount - byRow[rid]; need > 0 && pool > 0 {
			take := math.Min(need, pool)
			byRow[rid] += take
			pool -= take
		}
		paid := round2(byRow[rid])
		status := "Pending"
		switch {
		case paid >= round2(amount):
			status = "Paid"
			paidCount++
		case paid > 0:
			status = "Partially Paid"
		}
		if status != "Paid" && nextDue == nil {
			nextDue = r["due_date"]
		}
		scheduled += amount
		if _, err := s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET paid_amount=$3, status=$4 WHERE tenant_id=$1 AND id=$2`, sched.Table()),
			tenantID, rid, paid, status); err != nil {
			return err
		}
	}

	// Invoices settling an installment take its paid amount; a fully paid
	// installment marks its invoice Paid (and back to Issued if that changes).
	if inv, ok := s.reg.Get("sales_invoices"); ok {
		if _, err := s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s i SET paid_total = COALESCE(s.paid_amount,0),
			balance = round((COALESCE(i.grand_total,0) - COALESCE(s.paid_amount,0))::numeric, 2),
			status = CASE WHEN i.status = 'Cancelled' THEN i.status WHEN s.status = 'Paid' THEN 'Paid'
			              WHEN i.status = 'Paid' THEN 'Issued' ELSE i.status END
			FROM %s s WHERE i.tenant_id=$1 AND s.tenant_id=$1 AND s.invoice_id=$2 AND i.schedule_id = s.id::text`,
			inv.Table(), sched.Table()), tenantID, id.String()); err != nil {
			return err
		}
	}

	if len(payRes.Data) == 0 {
		paidTotal = toFloat64(agreement["paid_total"])
	}
	payStatus := "Unscheduled"
	switch {
	case len(rows) == 0:
	case paidCount == len(rows):
		payStatus = "Paid"
	case paidTotal > 0:
		payStatus = "Partially Paid"
	default:
		payStatus = "Not Paid"
	}
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET paid_total=$3, balance=$4, payment_status=$5,
		next_due_date=$6, scheduled_total=$7, installments=$8, installments_paid=$9 WHERE tenant_id=$1 AND id=$2`, sa.Table()),
		tenantID, id, round2(paidTotal), round2(toFloat64(agreement["grand_total"])-paidTotal), payStatus,
		dateArg(nextDue), round2(scheduled), len(rows), paidCount)
	return err
}

// syncQuoteAgreementStatus stamps a quote with the status of its most recent
// service agreement (blank when none remain), for the quote list/filters.
func (s *DocService) syncQuoteAgreementStatus(ctx context.Context, tenantID uuid.UUID, quoteRef any) {
	qid, err := uuid.Parse(asStr(quoteRef))
	if err != nil {
		return
	}
	quotes, ok1 := s.reg.Get("quotes")
	sa, ok2 := s.reg.Get("invoices")
	if !ok1 || !ok2 {
		return
	}
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s q SET agreement_status = (
		SELECT a.status FROM %s a WHERE a.tenant_id=$1 AND a.quote_id=$2::text ORDER BY a.created_at DESC LIMIT 1)
		WHERE q.tenant_id=$1 AND q.id=$2`, quotes.Table(), sa.Table()), tenantID, qid)
}

// dateArg turns a stored RFC3339/ISO date string into a DATE parameter.
func dateArg(v any) any {
	d := asStr(v)
	if len(d) < 10 {
		return nil
	}
	return d[:10]
}
