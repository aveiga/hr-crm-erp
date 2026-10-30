package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// Sales reporting: live, tenant-scoped aggregates over quotes, service
// agreements, their payment schedules and payments. Each section is included
// only when the caller can access its entity's section. Amounts are summed per
// currency, since documents may be in different currencies.

// GET /api/v1/reports/sales
func (h *Handlers) SalesReport(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.MustPrincipal(r)
	ctx := r.Context()
	out := map[string]any{}
	can := func(key string) (Entity, bool) {
		e, ok := h.reg.Get(key)
		return e, ok && p.CanAccessGroup(e.Group)
	}
	run := func(name string, fn func() (any, error)) bool {
		v, err := fn()
		if err != nil {
			httpx.Fail(w, err)
			return false
		}
		out[name] = v
		return true
	}

	if sa, ok := can("invoices"); ok {
		sched, _ := h.reg.Get("payment_schedule")
		accounts, _ := h.reg.Get("accounts")
		if !run("agreements", func() (any, error) { return h.agreementSummary(ctx, sa, p.TenantID) }) {
			return
		}
		if !run("overdue_installments", func() (any, error) {
			return h.installments(ctx, sa, sched, accounts, p.TenantID, "s.due_date < CURRENT_DATE")
		}) {
			return
		}
		if !run("upcoming_installments", func() (any, error) {
			return h.installments(ctx, sa, sched, accounts, p.TenantID, "s.due_date >= CURRENT_DATE AND s.due_date <= CURRENT_DATE + 30")
		}) {
			return
		}
	}
	if q, ok := can("quotes"); ok {
		if !run("quotes", func() (any, error) { return h.quoteSummary(ctx, q, p.TenantID) }) {
			return
		}
	}
	if pay, ok := can("payments"); ok {
		if !run("payments", func() (any, error) { return h.paymentSummary(ctx, pay, p.TenantID) }) {
			return
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handlers) agreementSummary(ctx context.Context, sa Entity, tenantID uuid.UUID) (any, error) {
	byCurrency, err := h.rows(ctx, fmt.Sprintf(`
		SELECT COALESCE(currency,'') AS currency, count(*) AS count,
		       COALESCE(sum(grand_total),0) AS contracted, COALESCE(sum(paid_total),0) AS paid,
		       COALESCE(sum(balance),0) AS outstanding
		FROM %s WHERE tenant_id=$1 AND COALESCE(status,'') <> 'Cancelled'
		GROUP BY 1 ORDER BY 1`, sa.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	byPayment, err := h.rows(ctx, fmt.Sprintf(`
		SELECT COALESCE(payment_status,'Unscheduled') AS status, count(*) AS count,
		       COALESCE(sum(balance),0) AS outstanding,
		       count(*) FILTER (WHERE next_due_date < CURRENT_DATE) AS overdue
		FROM %s WHERE tenant_id=$1 AND COALESCE(status,'') <> 'Cancelled'
		GROUP BY 1 ORDER BY 1`, sa.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	byStatus, err := h.rows(ctx, fmt.Sprintf(`
		SELECT COALESCE(status,'') AS status, count(*) AS count, COALESCE(sum(grand_total),0) AS contracted
		FROM %s WHERE tenant_id=$1 GROUP BY 1 ORDER BY 1`, sa.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"by_currency": byCurrency, "by_payment_status": byPayment, "by_status": byStatus}, nil
}

// installments lists open (not fully paid) installments matching a due-date
// condition, with their agreement and account, earliest first.
func (h *Handlers) installments(ctx context.Context, sa, sched, accounts Entity, tenantID uuid.UUID, cond string) (any, error) {
	return h.rows(ctx, fmt.Sprintf(`
		SELECT s.id::text AS id, a.id::text AS agreement_id, a.quote_description AS agreement,
		       acc.account_name AS account, COALESCE(a.currency,'') AS currency,
		       s.due_date, s.description, COALESCE(s.amount,0) AS amount, COALESCE(s.paid_amount,0) AS paid,
		       COALESCE(s.amount,0) - COALESCE(s.paid_amount,0) AS outstanding
		FROM %s s
		JOIN %s a ON a.tenant_id = s.tenant_id AND a.id::text = s.invoice_id
		LEFT JOIN %s acc ON acc.tenant_id = a.tenant_id AND acc.id::text = a.account_id
		WHERE s.tenant_id=$1 AND COALESCE(s.status,'Pending') <> 'Paid'
		  AND COALESCE(a.status,'') <> 'Cancelled' AND %s
		ORDER BY s.due_date, a.quote_description LIMIT 100`, sched.Table(), sa.Table(), accounts.Table(), cond), tenantID)
}

func (h *Handlers) quoteSummary(ctx context.Context, q Entity, tenantID uuid.UUID) (any, error) {
	byStatus, err := h.rows(ctx, fmt.Sprintf(`
		SELECT COALESCE(status,'') AS status, count(*) AS count,
		       COALESCE(sum(net_total),0) AS net, COALESCE(sum(grand_total),0) AS grand
		FROM %s WHERE tenant_id=$1 GROUP BY 1 ORDER BY 1`, q.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	conv, err := h.rows(ctx, fmt.Sprintf(`
		SELECT count(*) AS quotes,
		       count(*) FILTER (WHERE status = 'Accepted') AS accepted,
		       count(*) FILTER (WHERE COALESCE(agreement_status,'') <> '') AS contracted,
		       count(*) FILTER (WHERE status = 'Accepted' AND COALESCE(agreement_status,'') = '') AS accepted_not_contracted,
		       count(*) FILTER (WHERE status IN ('Draft','Sent') AND valid_until < CURRENT_DATE) AS open_expired
		FROM %s WHERE tenant_id=$1`, q.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	byMonth, err := h.rows(ctx, fmt.Sprintf(`
		SELECT to_char(date_trunc('month', quote_date), 'YYYY-MM') AS month, count(*) AS count,
		       COALESCE(sum(net_total),0) AS net
		FROM %s WHERE tenant_id=$1 AND quote_date >= date_trunc('month', CURRENT_DATE) - interval '11 months'
		GROUP BY 1 ORDER BY 1`, q.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"by_status": byStatus, "conversion": conv[0], "by_month": byMonth}, nil
}

func (h *Handlers) paymentSummary(ctx context.Context, pay Entity, tenantID uuid.UUID) (any, error) {
	byMonth, err := h.rows(ctx, fmt.Sprintf(`
		SELECT to_char(date_trunc('month', payment_date), 'YYYY-MM') AS month, count(*) AS count,
		       COALESCE(sum(amount),0) AS amount
		FROM %s WHERE tenant_id=$1 AND payment_date >= date_trunc('month', CURRENT_DATE) - interval '11 months'
		GROUP BY 1 ORDER BY 1`, pay.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	byMethod, err := h.rows(ctx, fmt.Sprintf(`
		SELECT COALESCE(method,'') AS method, count(*) AS count, COALESCE(sum(amount),0) AS amount
		FROM %s WHERE tenant_id=$1 GROUP BY 1 ORDER BY 3 DESC`, pay.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	unallocated, err := h.rows(ctx, fmt.Sprintf(`
		SELECT count(*) AS count, COALESCE(sum(amount),0) AS amount
		FROM %s WHERE tenant_id=$1 AND COALESCE(schedule_id,'') = ''`, pay.Table()), tenantID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"by_month": byMonth, "by_method": byMethod, "unallocated": unallocated[0]}, nil
}

// rows runs a tenant-scoped report query ($1 = tenant id) into JSON rows.
func (h *Handlers) rows(ctx context.Context, q string, tenantID uuid.UUID) ([]map[string]any, error) {
	rs, err := h.repo.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return scanRows(rs)
}
