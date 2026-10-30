package azure

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DashboardQuery filters the Azure dashboard.
type DashboardQuery struct {
	Scope
	From, To  time.Time
	MarkupPct float64 // what-if markup applied on top of fees
}

// Amounts is usage and fee, actual (synced) and forecast (run-rate).
type Amounts struct {
	Usage         float64 `json:"usage"`
	Fee           float64 `json:"fee"`
	ForecastUsage float64 `json:"forecast_usage"`
	ForecastFee   float64 `json:"forecast_fee"`
	FeeMarkedUp   float64 `json:"fee_with_markup"` // (fee + forecast fee) × (1 + markup)
}

func (a *Amounts) add(usage, fee float64, forecast bool) {
	if forecast {
		a.ForecastUsage += usage
		a.ForecastFee += fee
	} else {
		a.Usage += usage
		a.Fee += fee
	}
}

func (a *Amounts) finish(markup float64) {
	a.Usage, a.Fee = round2(a.Usage), round2(a.Fee)
	a.ForecastUsage, a.ForecastFee = round2(a.ForecastUsage), round2(a.ForecastFee)
	a.FeeMarkedUp = round2((a.Fee + a.ForecastFee) * (1 + markup/100))
}

type MonthRow struct {
	Month    string `json:"month"`
	Currency string `json:"currency"`
	Amounts
}

type SubRow struct {
	ID             string   `json:"id"`
	SubscriptionID string   `json:"subscription_id"`
	Name           string   `json:"name"`
	AccountID      string   `json:"account_id"`
	Account        string   `json:"account"`
	Type           string   `json:"type"`
	FeePct         *float64 `json:"fee_pct"` // override; nil = type rate
	Currency       string   `json:"currency"`
	DailyRunRate   float64  `json:"daily_run_rate"`
	Amounts
}

type AccountRow struct {
	AccountID string `json:"account_id"`
	Account   string `json:"account"`
	Currency  string `json:"currency"`
	Subs      int    `json:"subscriptions"`
	Amounts
}

type TotalRow struct {
	Currency string `json:"currency"`
	Amounts
}

// Dashboard is the Azure usage & fee overview.
type Dashboard struct {
	From          string           `json:"from"`
	To            string           `json:"to"`
	SyncedThrough string           `json:"synced_through"`
	MarkupPct     float64          `json:"markup_pct"`
	Totals        []TotalRow       `json:"totals"`
	ByMonth       []MonthRow       `json:"by_month"`
	BySub         []SubRow         `json:"by_subscription"`
	ByAccount     []AccountRow     `json:"by_account"`
	Invoiced      []map[string]any `json:"invoiced"`
	Agreements    []map[string]any `json:"agreement_forecast"`
	Warnings      []string         `json:"warnings,omitempty"`
}

func (s *Service) Dashboard(ctx context.Context, tenantID uuid.UUID, q DashboardQuery) (*Dashboard, error) {
	d := &Dashboard{From: fmtDay(q.From), To: fmtDay(q.To), MarkupPct: q.MarkupPct,
		Totals: []TotalRow{}, ByMonth: []MonthRow{}, BySub: []SubRow{}, ByAccount: []AccountRow{}}
	subs, err := s.subscriptions(ctx, tenantID, q.Scope)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return d, nil
	}
	rt, err := s.loadRates(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if d.SyncedThrough, err = s.syncedThrough(ctx, tenantID, subs); err != nil {
		return nil, err
	}
	rr, err := s.runRates(ctx, tenantID, subIDs(subs), d.SyncedThrough)
	if err != nil {
		return nil, err
	}
	names, err := s.accountNames(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	bySub := map[string]*SubRow{}
	for _, sub := range subs {
		row := &SubRow{ID: sub.ID, SubscriptionID: sub.SubID, Name: sub.Name, AccountID: sub.AccountID,
			Account: names[sub.AccountID], Type: sub.Type, FeePct: sub.FeeOverride,
			Currency: rr[sub.ID].Currency, DailyRunRate: round2(rr[sub.ID].Daily)}
		bySub[sub.ID] = row
	}
	subOf := map[string]subRow{}
	for _, sub := range subs {
		subOf[sub.ID] = sub
	}
	type mk struct{ month, cur string }
	months := map[mk]*MonthRow{}
	month := func(m, cur string) *MonthRow {
		k := mk{m[:7], cur}
		if months[k] == nil {
			months[k] = &MonthRow{Month: m[:7], Currency: cur}
		}
		return months[k]
	}

	// Actual usage through the last synced day; forecast after it.
	actualEnd := q.To
	through, err := parseDay(d.SyncedThrough)
	if err != nil {
		actualEnd = q.From.AddDate(0, 0, -1)
		d.Warnings = append(d.Warnings, "usage was never synced for some of these subscriptions")
	} else if through.Before(q.To) {
		actualEnd = through
	}
	if !actualEnd.Before(q.From) {
		aggs, err := s.usage(ctx, tenantID, subIDs(subs), fmtDay(q.From), fmtDay(actualEnd))
		if err != nil {
			return nil, err
		}
		for _, u := range aggs {
			fee := u.Cost * rt.pct(subOf[u.Sub], u.Month) / 100
			bySub[u.Sub].add(u.Cost, fee, false)
			if bySub[u.Sub].Currency == "" {
				bySub[u.Sub].Currency = u.Currency
			}
			month(u.Month, u.Currency).add(u.Cost, fee, false)
		}
	}
	if fStart := actualEnd.AddDate(0, 0, 1); !fStart.After(q.To) {
		if fStart.Before(q.From) {
			fStart = q.From
		}
		for _, seg := range segments(fStart, q.To) {
			for _, sub := range subs {
				r := rr[sub.ID]
				if r.Daily == 0 {
					continue
				}
				cost := r.Daily * float64(seg.days)
				fee := cost * rt.pct(sub, seg.month) / 100
				bySub[sub.ID].add(cost, fee, true)
				month(seg.month, r.Currency).add(cost, fee, true)
			}
		}
	}

	totals := map[string]*TotalRow{}
	accounts := map[mk]*AccountRow{}
	for _, sub := range subs {
		row := bySub[sub.ID]
		row.finish(q.MarkupPct)
		d.BySub = append(d.BySub, *row)
		if totals[row.Currency] == nil {
			totals[row.Currency] = &TotalRow{Currency: row.Currency}
		}
		t := totals[row.Currency]
		t.Usage, t.Fee = t.Usage+row.Usage, t.Fee+row.Fee
		t.ForecastUsage, t.ForecastFee = t.ForecastUsage+row.ForecastUsage, t.ForecastFee+row.ForecastFee
		k := mk{sub.AccountID, row.Currency}
		if accounts[k] == nil {
			accounts[k] = &AccountRow{AccountID: sub.AccountID, Account: names[sub.AccountID], Currency: row.Currency}
		}
		a := accounts[k]
		a.Subs++
		a.Usage, a.Fee = a.Usage+row.Usage, a.Fee+row.Fee
		a.ForecastUsage, a.ForecastFee = a.ForecastUsage+row.ForecastUsage, a.ForecastFee+row.ForecastFee
		if sub.AccountID == "" && row.Usage+row.ForecastUsage > 0 {
			d.Warnings = append(d.Warnings, sub.Name+" isn't mapped to an account")
		}
		if sub.Type == "" && sub.FeeOverride == nil && row.Usage+row.ForecastUsage > 0 {
			d.Warnings = append(d.Warnings, sub.Name+" has no type (fee 0%)")
		}
	}
	for _, t := range totals {
		t.finish(q.MarkupPct)
		d.Totals = append(d.Totals, *t)
	}
	for _, a := range accounts {
		a.finish(q.MarkupPct)
		d.ByAccount = append(d.ByAccount, *a)
	}
	for _, m := range months {
		m.finish(q.MarkupPct)
		d.ByMonth = append(d.ByMonth, *m)
	}
	sort.Slice(d.Totals, func(i, j int) bool { return d.Totals[i].Currency < d.Totals[j].Currency })
	sort.Slice(d.ByMonth, func(i, j int) bool {
		return d.ByMonth[i].Month+d.ByMonth[i].Currency < d.ByMonth[j].Month+d.ByMonth[j].Currency
	})
	sort.Slice(d.ByAccount, func(i, j int) bool {
		return d.ByAccount[i].Fee+d.ByAccount[i].ForecastFee > d.ByAccount[j].Fee+d.ByAccount[j].ForecastFee
	})
	sort.SliceStable(d.BySub, func(i, j int) bool {
		return d.BySub[i].Fee+d.BySub[i].ForecastFee > d.BySub[j].Fee+d.BySub[j].ForecastFee
	})

	accIDs, seen := []string{}, map[string]bool{}
	for _, sub := range subs {
		if sub.AccountID != "" && !seen[sub.AccountID] {
			seen[sub.AccountID] = true
			accIDs = append(accIDs, sub.AccountID)
		}
	}
	if d.Invoiced, err = s.invoicedFees(ctx, tenantID, accIDs, q); err != nil {
		return nil, err
	}
	if d.Agreements, err = s.agreementForecast(ctx, tenantID, accIDs, q); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) accountNames(ctx context.Context, tenantID uuid.UUID) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id::text, COALESCE(account_name,'') FROM %s WHERE tenant_id=$1`,
		s.entity("accounts").Table()), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// invoicedFees sums the Azure fee invoices (of Azure agreements) of the
// accounts in scope whose period overlaps the dashboard range, per currency:
// invoiced, paid, due and overdue.
func (s *Service) invoicedFees(ctx context.Context, tenantID uuid.UUID, accIDs []string, q DashboardQuery) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT COALESCE(i.currency,'') AS currency, count(*) AS count,
		       COALESCE(sum(i.net_total),0) AS net, COALESCE(sum(i.grand_total),0) AS invoiced,
		       COALESCE(sum(i.paid_total),0) AS paid, COALESCE(sum(i.balance),0) AS due,
		       COALESCE(sum(i.balance) FILTER (WHERE i.due_date < CURRENT_DATE),0) AS overdue,
		       count(*) FILTER (WHERE i.status = 'Draft') AS drafts
		FROM %s i JOIN %s a ON a.tenant_id = i.tenant_id AND a.id::text = i.agreement_id AND a.azure_fees
		WHERE i.tenant_id=$1 AND i.account_id = ANY($2) AND COALESCE(i.status,'') <> 'Cancelled'
		  AND i.period_start <= $4 AND i.period_end >= $3
		GROUP BY 1 ORDER BY 1`, s.entity("sales_invoices").Table(), s.entity("invoices").Table()),
		tenantID, accIDs, fmtDay(q.From), fmtDay(q.To))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}

// agreementForecast sums the quarters of Azure agreements in scope that are
// not invoiced yet (forecast/actual lines), per currency — what the
// agreements expect to bill, next to the live run-rate forecast.
func (s *Service) agreementForecast(ctx context.Context, tenantID uuid.UUID, accIDs []string, q DashboardQuery) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT COALESCE(a.currency,'') AS currency, COALESCE(l.basis,'') AS basis, count(*) AS quarters,
		       COALESCE(sum(l.qty * l.unit_price),0) AS net,
		       COALESCE(sum(l.qty * l.unit_price * (1 + COALESCE(a.markup_pct,0)/100)),0) AS with_markup
		FROM %s l JOIN %s a ON a.tenant_id = l.tenant_id AND a.id::text = l.invoice_id AND a.azure_fees
		WHERE l.tenant_id=$1 AND a.account_id = ANY($2) AND COALESCE(a.status,'') <> 'Cancelled'
		  AND l.period_start <= $4 AND l.period_end >= $3
		GROUP BY 1,2 ORDER BY 1,2`, s.entity("invoice_lines").Table(), s.entity("invoices").Table()),
		tenantID, accIDs, fmtDay(q.From), fmtDay(q.To))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}

// collect reads report rows into JSON maps.
func collect(rows pgx.Rows) ([]map[string]any, error) {
	out := []map[string]any{}
	fds := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			m[fd.Name] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
