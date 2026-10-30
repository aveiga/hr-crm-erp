package azure

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
	"hrcrm/internal/resource"
)

// Fees: each subscription is billed fee% × its actual Azure cost. The fee %
// is the subscription's override, else its type's rate (azure_fee_rates,
// effective from the month of valid_from), else the built-in default.
var defaultRates = map[string]float64{"TAN": 25, "TSS": 27}

// forecastDays is the trailing window the daily run-rate is averaged over.
const forecastDays = 90

type subRow struct {
	ID, SubID, Name, AccountID, AzTenantID, Type string
	FeeOverride                                  *float64
}

// Scope filters subscriptions (dashboard filters, an agreement's account).
type Scope struct {
	AccountID      string
	SubscriptionID string // row id
	AzureTenantID  string // row id
	Type           string
}

func (s *Service) subscriptions(ctx context.Context, tenantID uuid.UUID, sc Scope) ([]subRow, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id::text, COALESCE(subscription_id,''), COALESCE(display_name,''),
		COALESCE(account_id,''), COALESCE(azure_tenant_id,''), COALESCE(subscription_type,''), fee_pct
		FROM %s WHERE tenant_id=$1 AND ($2='' OR account_id=$2) AND ($3='' OR id::text=$3)
		AND ($4='' OR azure_tenant_id=$4) AND ($5='' OR COALESCE(subscription_type,'')=$5)
		ORDER BY display_name`, s.entity("azure_subscriptions").Table()),
		tenantID, sc.AccountID, sc.SubscriptionID, sc.AzureTenantID, sc.Type)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subRow
	for rows.Next() {
		var r subRow
		if err := rows.Scan(&r.ID, &r.SubID, &r.Name, &r.AccountID, &r.AzTenantID, &r.Type, &r.FeeOverride); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func subIDs(subs []subRow) []string {
	ids := make([]string, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	return ids
}

type rate struct {
	month string // YYYY-MM it applies from ("" = always)
	pct   float64
}

// rates holds each type's rates, latest first.
type rates map[string][]rate

func (s *Service) loadRates(ctx context.Context, tenantID uuid.UUID) (rates, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT subscription_type, COALESCE(to_char(valid_from,'YYYY-MM'),''), COALESCE(fee_pct,0)
		FROM %s WHERE tenant_id=$1 AND subscription_type IS NOT NULL ORDER BY valid_from DESC NULLS LAST, created_at DESC`,
		s.entity("azure_fee_rates").Table()), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := rates{}
	for rows.Next() {
		var typ string
		var r rate
		if err := rows.Scan(&typ, &r.month, &r.pct); err != nil {
			return nil, err
		}
		out[typ] = append(out[typ], r)
	}
	return out, rows.Err()
}

// pct is the fee % for a subscription's usage in a month (YYYY-MM-DD).
func (rs rates) pct(sub subRow, month string) float64 {
	if sub.FeeOverride != nil {
		return *sub.FeeOverride
	}
	for _, r := range rs[sub.Type] {
		if r.month == "" || r.month <= month[:7] {
			return r.pct
		}
	}
	return defaultRates[sub.Type]
}

type usageAgg struct {
	Sub, Month, Currency string
	Cost                 float64
}

// usage sums costs per subscription, month and currency over [from, to].
func (s *Service) usage(ctx context.Context, tenantID uuid.UUID, ids []string, from, to string) ([]usageAgg, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT azure_subscription_id, to_char(date_trunc('month', usage_date),'YYYY-MM-DD'),
		COALESCE(currency,''), sum(cost) FROM %s
		WHERE tenant_id=$1 AND azure_subscription_id = ANY($2) AND usage_date BETWEEN $3 AND $4
		GROUP BY 1,2,3 ORDER BY 2`, s.entity("azure_usage").Table()), tenantID, ids, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []usageAgg
	for rows.Next() {
		var u usageAgg
		if err := rows.Scan(&u.Sub, &u.Month, &u.Currency, &u.Cost); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type runRate struct {
	Daily    float64
	Currency string
}

// runRates is each subscription's average daily cost over the forecastDays
// up to `through` (or since its first usage, when newer).
func (s *Service) runRates(ctx context.Context, tenantID uuid.UUID, ids []string, through string) (map[string]runRate, error) {
	out := map[string]runRate{}
	end, err := parseDay(through)
	if err != nil {
		return out, nil // nothing synced yet
	}
	start := end.AddDate(0, 0, -(forecastDays - 1))
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT azure_subscription_id, COALESCE(currency,''), sum(cost), min(usage_date)
		FROM %s WHERE tenant_id=$1 AND azure_subscription_id = ANY($2) AND usage_date BETWEEN $3 AND $4
		GROUP BY 1,2 ORDER BY 3`, s.entity("azure_usage").Table()), tenantID, ids, fmtDay(start), through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sub, cur string
		var cost float64
		var first time.Time
		if err := rows.Scan(&sub, &cur, &cost, &first); err != nil {
			return nil, err
		}
		from := start
		if first.After(from) {
			from = first
		}
		days := end.Sub(from).Hours()/24 + 1
		out[sub] = runRate{Daily: cost / days, Currency: cur} // ordered by cost: the main currency wins
	}
	return out, rows.Err()
}

// syncedThrough is the last day with complete usage for every Azure tenant
// the subscriptions belong to ("" when one was never synced).
func (s *Service) syncedThrough(ctx context.Context, tenantID uuid.UUID, subs []subRow) (string, error) {
	seen, ids := map[string]bool{}, []string{}
	for _, sub := range subs {
		if sub.AzTenantID == "" {
			return "", nil // not linked to a tenant: completeness unknown
		}
		if !seen[sub.AzTenantID] {
			seen[sub.AzTenantID] = true
			ids = append(ids, sub.AzTenantID)
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	var missing bool
	var min *time.Time
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(bool_or(synced_through IS NULL), true), min(synced_through) FROM %s
		WHERE tenant_id=$1 AND id::text = ANY($2)`, s.entity("azure_tenants").Table()), tenantID, ids).Scan(&missing, &min)
	if err != nil || missing || min == nil {
		return "", err
	}
	return fmtDay(*min), nil
}

// segment is the part of a month inside a date range.
type segment struct {
	month string // first day, YYYY-MM-DD
	days  int
}

func segments(from, to time.Time) []segment {
	var out []segment
	for d := from; !d.After(to); {
		monthEnd := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		end := monthEnd
		if end.After(to) {
			end = to
		}
		out = append(out, segment{
			month: fmtDay(time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)),
			days:  int(end.Sub(d).Hours()/24) + 1,
		})
		d = end.AddDate(0, 0, 1)
	}
	return out
}

// periodFee is usage and fee over a period: actual through `through`, then a
// run-rate forecast for the remaining days.
type periodFee struct {
	Usage, Fee, ForecastUsage, ForecastFee float64
	Currency                               string
}

func (s *Service) periodFee(ctx context.Context, tenantID uuid.UUID, subs []subRow, rt rates, rr map[string]runRate, through string, from, to time.Time) (periodFee, error) {
	var out periodFee
	byID := map[string]subRow{}
	for _, sub := range subs {
		byID[sub.ID] = sub
	}
	actualEnd := to
	if t, err := parseDay(through); err != nil {
		actualEnd = from.AddDate(0, 0, -1) // nothing synced: all forecast
	} else if t.Before(to) {
		actualEnd = t
	}
	if !actualEnd.Before(from) {
		aggs, err := s.usage(ctx, tenantID, subIDs(subs), fmtDay(from), fmtDay(actualEnd))
		if err != nil {
			return out, err
		}
		for _, u := range aggs {
			out.Usage += u.Cost
			out.Fee += u.Cost * rt.pct(byID[u.Sub], u.Month) / 100
			if out.Currency == "" {
				out.Currency = u.Currency
			}
		}
	}
	if fStart := actualEnd.AddDate(0, 0, 1); !fStart.After(to) {
		if fStart.Before(from) {
			fStart = from
		}
		for _, seg := range segments(fStart, to) {
			for _, sub := range subs {
				r := rr[sub.ID]
				cost := r.Daily * float64(seg.days)
				out.ForecastUsage += cost
				out.ForecastFee += cost * rt.pct(sub, seg.month) / 100
				if out.Currency == "" {
					out.Currency = r.Currency
				}
			}
		}
	}
	return out, nil
}

// --- quarters ----------------------------------------------------------------

type quarter struct {
	Start, End time.Time
	Label      string
}

// quarters lists the calendar quarters overlapping [start, end], clipped.
func quarters(start, end time.Time) []quarter {
	var out []quarter
	for d := start; !d.After(end); {
		qn := (int(d.Month()) - 1) / 3
		qEnd := time.Date(d.Year(), time.Month(qn*3+4), 0, 0, 0, 0, 0, time.UTC)
		if qEnd.After(end) {
			qEnd = end
		}
		out = append(out, quarter{Start: d, End: qEnd, Label: fmt.Sprintf("Q%d %d", qn+1, d.Year())})
		d = qEnd.AddDate(0, 0, 1)
	}
	return out
}

func dmy(t time.Time) string { return t.Format("02-01-2006") }

// agreementCtx is what planning an Azure agreement needs.
type agreementCtx struct {
	id       uuid.UUID
	header   map[string]any
	lines    []map[string]any
	schedule []map[string]any
	quarters []quarter
	subs     []subRow
	rates    rates
	runRates map[string]runRate
	through  string
	invoices map[string]invoiceRef // by period_start
}

type invoiceRef struct {
	ID, Status    string
	Net, Grand    float64
	InvoiceNumber string
}

func (s *Service) loadAgreement(ctx context.Context, tenantID, id uuid.UUID) (*agreementCtx, error) {
	sa := s.entity("invoices")
	header, err := s.repo.Get(ctx, sa, tenantID, id)
	if err != nil {
		return nil, err
	}
	if header["azure_fees"] != true {
		return nil, httpx.Errorf(http.StatusBadRequest, "not_azure", "this Service Agreement doesn't bill Azure fees")
	}
	start, err1 := parseDay(day(header["service_start_date"]))
	end, err2 := parseDay(day(header["service_end_date"]))
	if err1 != nil || err2 != nil || end.Before(start) {
		return nil, httpx.Errorf(http.StatusBadRequest, "validation", "the agreement needs a service start and end date")
	}
	a := &agreementCtx{id: id, header: header, quarters: quarters(start, end)}
	if a.lines, err = s.docs.LoadLines(ctx, sa, tenantID, id); err != nil {
		return nil, err
	}
	children, err := s.docs.LoadChildren(ctx, sa, tenantID, id)
	if err != nil {
		return nil, err
	}
	a.schedule = children["payment_schedule"]
	if a.subs, err = s.subscriptions(ctx, tenantID, Scope{AccountID: str(header["account_id"])}); err != nil {
		return nil, err
	}
	if a.rates, err = s.loadRates(ctx, tenantID); err != nil {
		return nil, err
	}
	if a.through, err = s.syncedThrough(ctx, tenantID, a.subs); err != nil {
		return nil, err
	}
	if a.runRates, err = s.runRates(ctx, tenantID, subIDs(a.subs), a.through); err != nil {
		return nil, err
	}
	a.invoices, err = s.agreementInvoices(ctx, tenantID, id)
	return a, err
}

func (s *Service) agreementInvoices(ctx context.Context, tenantID, agreementID uuid.UUID) (map[string]invoiceRef, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id::text, COALESCE(status,''), COALESCE(net_total,0), COALESCE(grand_total,0),
		COALESCE(invoice_number,''), to_char(period_start,'YYYY-MM-DD')
		FROM %s WHERE tenant_id=$1 AND agreement_id=$2 AND period_start IS NOT NULL AND COALESCE(status,'') <> 'Cancelled'
		ORDER BY created_at`, s.entity("sales_invoices").Table()), tenantID, agreementID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]invoiceRef{}
	for rows.Next() {
		var r invoiceRef
		var ps string
		if err := rows.Scan(&r.ID, &r.Status, &r.Net, &r.Grand, &r.InvoiceNumber, &ps); err != nil {
			return nil, err
		}
		out[ps] = r
	}
	return out, rows.Err()
}

func findByPeriod(rows []map[string]any, start string) map[string]any {
	for _, r := range rows {
		if day(r["period_start"]) == start {
			return r
		}
	}
	return nil
}

// plan rebuilds the quarter lines and installments of an Azure agreement.
// Invoiced quarters take their invoice (net on the line, grand total on the
// installment); other quarters are recomputed from usage (actual, then
// forecast). With only set, just that quarter is recomputed. Rows keep their
// ids (payments stay linked); lines/installments without a period are kept
// as entered. The last open installment absorbs rounding so the schedule
// adds up to the agreement total.
func (s *Service) plan(ctx context.Context, tenantID uuid.UUID, a *agreementCtx, only string) ([]map[string]any, []map[string]any, error) {
	markup := num(a.header["markup_pct"])
	vat := num(a.header["vat_pct"])
	factor := (1 + markup/100) * (1 + vat/100)
	terms := paymentTerms(a.header)

	var lines, sched []map[string]any
	used := map[string]bool{}
	absorber := -1
	for _, q := range a.quarters {
		ps := fmtDay(q.Start)
		used[ps] = true
		oldLine, oldInst := findByPeriod(a.lines, ps), findByPeriod(a.schedule, ps)
		if only != "" && ps != only {
			if oldLine != nil {
				lines = append(lines, oldLine)
			}
			if oldInst != nil {
				sched = append(sched, oldInst)
				if _, inv := a.invoices[ps]; !inv {
					absorber = len(sched) - 1
				}
			}
			continue
		}

		var net, inst float64
		basis := "Invoiced"
		if inv, ok := a.invoices[ps]; ok {
			net, inst = inv.Net, inv.Grand
		} else {
			pf, err := s.periodFee(ctx, tenantID, a.subs, a.rates, a.runRates, a.through, q.Start, q.End)
			if err != nil {
				return nil, nil, err
			}
			net = round2(pf.Fee + pf.ForecastFee)
			inst = round2(net * factor)
			basis = "Actual"
			if pf.ForecastUsage > 0 || a.through == "" || a.through < fmtDay(q.End) {
				basis = "Forecast"
			}
		}

		line := map[string]any{"qty": 1, "description": fmt.Sprintf("Azure fees %s (%s – %s)", q.Label, dmy(q.Start), dmy(q.End))}
		if oldLine != nil {
			line = copyRow(oldLine)
		}
		line["qty"], line["unit_price"], line["basis"] = 1, net, basis
		line["period_start"], line["period_end"] = ps, fmtDay(q.End)
		lines = append(lines, line)

		row := map[string]any{"due_date": fmtDay(q.End.AddDate(0, 0, terms)), "description": q.Label}
		if oldInst != nil {
			row = copyRow(oldInst)
		}
		row["amount"], row["period_start"], row["period_end"] = inst, ps, fmtDay(q.End)
		sched = append(sched, row)
		if basis != "Invoiced" {
			absorber = len(sched) - 1
		}
	}
	// Lines / installments outside the quarters (entered by hand) stay.
	for _, l := range a.lines {
		if !used[day(l["period_start"])] {
			lines = append(lines, l)
		}
	}
	for _, r := range a.schedule {
		if !used[day(r["period_start"])] {
			sched = append(sched, r)
		}
	}

	if len(sched) > 0 {
		grand := num(resource.ComputeTotals(s.entity("invoices"), a.header, lines)["grand_total"])
		sum := 0.0
		for _, r := range sched {
			sum += num(r["amount"])
		}
		if absorber < 0 {
			absorber = len(sched) - 1
		}
		sched[absorber]["amount"] = round2(num(sched[absorber]["amount"]) + grand - sum)
	}
	return lines, sched, nil
}

// paymentTerms is the agreement's payment terms in days (30 when unset).
func paymentTerms(header map[string]any) int {
	if header["payment_terms_days"] == nil {
		return 30
	}
	return int(num(header["payment_terms_days"]))
}

func copyRow(r map[string]any) map[string]any {
	out := make(map[string]any, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// AgreementRequest creates an Azure fee Service Agreement for an account.
type AgreementRequest struct {
	AccountID        string   `json:"account_id"`
	PeriodStart      string   `json:"period_start"`
	PeriodEnd        string   `json:"period_end"`
	Description      string   `json:"description"`
	MarkupPct        *float64 `json:"markup_pct"`
	VatPct           *float64 `json:"vat_pct"`
	PaymentTermsDays *int     `json:"payment_terms_days"`
	LegalEntityID    string   `json:"legal_entity_id"`
}

// CreateAgreement opens a quarterly Azure fee agreement over a period (past,
// future or both): past quarters are priced from real usage, the rest from
// the run-rate forecast. It stays a normal, editable Draft agreement.
func (s *Service) CreateAgreement(ctx context.Context, tenantID uuid.UUID, req AgreementRequest) (map[string]any, error) {
	start, err1 := parseDay(req.PeriodStart)
	end, err2 := parseDay(req.PeriodEnd)
	accID, err3 := uuid.Parse(req.AccountID)
	if err1 != nil || err2 != nil || err3 != nil || end.Before(start) {
		return nil, httpx.Errorf(http.StatusBadRequest, "validation", "account, period start and period end (after the start) are required")
	}
	acc, err := s.repo.Get(ctx, s.entity("accounts"), tenantID, accID)
	if err != nil {
		return nil, httpx.Errorf(http.StatusBadRequest, "validation", "account not found")
	}
	desc := req.Description
	if desc == "" {
		desc = fmt.Sprintf("Azure fees %s – %s", dmy(start), dmy(end))
	}
	header := map[string]any{
		"account_id": req.AccountID, "quote_description": desc, "invoice_date": fmtDay(today()), "status": "Draft",
		"service_start_date": req.PeriodStart, "service_end_date": req.PeriodEnd,
		"billing_frequency": "Quarterly", "azure_fees": true, "legal_entity_id": req.LegalEntityID,
		"payment_terms_days": acc["payment_terms_days"],
	}
	if req.LegalEntityID == "" {
		delete(header, "legal_entity_id") // derived from the account
	}
	if req.MarkupPct != nil {
		header["markup_pct"] = *req.MarkupPct
	}
	if req.VatPct != nil {
		header["vat_pct"] = *req.VatPct
	}
	if req.PaymentTermsDays != nil {
		header["payment_terms_days"] = *req.PaymentTermsDays
	}
	sa := s.entity("invoices")
	created, err := s.docs.SaveDoc(ctx, sa, tenantID, nil, header, nil, nil)
	if err != nil {
		return nil, err
	}
	return s.Refresh(ctx, tenantID, uuid.MustParse(str(created["id"])), "")
}

// Refresh re-prices an Azure agreement's quarters (only = one quarter's start
// date, or "" for all) and saves it.
func (s *Service) Refresh(ctx context.Context, tenantID, id uuid.UUID, only string) (map[string]any, error) {
	a, err := s.loadAgreement(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	lines, sched, err := s.plan(ctx, tenantID, a, only)
	if err != nil {
		return nil, err
	}
	usage, err := s.usageNotes(ctx, tenantID, a)
	if err != nil {
		return nil, err
	}
	header := map[string]any{"notes": mergeNotes(str(a.header["notes"]), usage)}
	return s.docs.SaveDoc(ctx, s.entity("invoices"), tenantID, &id, header, lines,
		map[string][]map[string]any{"payment_schedule": sched})
}

// Quarter is one billing quarter of an Azure agreement, for the UI.
type Quarter struct {
	Label         string         `json:"label"`
	PeriodStart   string         `json:"period_start"`
	PeriodEnd     string         `json:"period_end"`
	Amount        float64        `json:"amount"`
	Basis         string         `json:"basis"`
	UsageComplete bool           `json:"usage_complete"`
	Ended         bool           `json:"ended"`
	Installment   map[string]any `json:"installment,omitempty"`
	Invoice       *invoiceRef    `json:"invoice,omitempty"`
}

func (s *Service) Quarters(ctx context.Context, tenantID, id uuid.UUID) ([]Quarter, string, error) {
	a, err := s.loadAgreement(ctx, tenantID, id)
	if err != nil {
		return nil, "", err
	}
	out := []Quarter{}
	for _, q := range a.quarters {
		ps := fmtDay(q.Start)
		row := Quarter{Label: q.Label, PeriodStart: ps, PeriodEnd: fmtDay(q.End),
			UsageComplete: a.through != "" && a.through >= fmtDay(q.End), Ended: q.End.Before(today())}
		if l := findByPeriod(a.lines, ps); l != nil {
			row.Amount, row.Basis = num(l["unit_price"]), str(l["basis"])
		}
		if inst := findByPeriod(a.schedule, ps); inst != nil {
			row.Installment = map[string]any{"id": inst["id"], "due_date": inst["due_date"], "amount": inst["amount"],
				"paid_amount": inst["paid_amount"], "status": inst["status"]}
		}
		if inv, ok := a.invoices[ps]; ok {
			inv := inv
			row.Invoice = &inv
		}
		out = append(out, row)
	}
	return out, a.through, nil
}

// CreateInvoice bills one quarter of an Azure agreement: a line per
// subscription of the account with usage in the quarter, priced at its fee %.
// The invoice settles the quarter's installment, whose amount (and the
// agreement's line) then follow the invoice (see trueUp). Unless forced, the
// quarter's usage must be fully synced.
func (s *Service) CreateInvoice(ctx context.Context, tenantID, agreementID uuid.UUID, periodStart string, force bool) (map[string]any, []string, error) {
	a, err := s.loadAgreement(ctx, tenantID, agreementID)
	if err != nil {
		return nil, nil, err
	}
	var q *quarter
	for i := range a.quarters {
		if fmtDay(a.quarters[i].Start) == periodStart {
			q = &a.quarters[i]
		}
	}
	if q == nil {
		return nil, nil, httpx.Errorf(http.StatusBadRequest, "validation", "no quarter of this agreement starts on "+periodStart)
	}
	if inv, ok := a.invoices[periodStart]; ok {
		return nil, nil, httpx.Errorf(http.StatusConflict, "already_invoiced", fmt.Sprintf("%s is already invoiced (%s)", q.Label, firstNon(inv.InvoiceNumber, inv.ID)))
	}
	if len(a.subs) == 0 {
		return nil, nil, httpx.Errorf(http.StatusBadRequest, "no_subscriptions", "no Azure subscriptions are mapped to this account")
	}
	if !force && (a.through == "" || a.through < fmtDay(q.End)) {
		synced := a.through
		if synced == "" {
			synced = "never"
		}
		return nil, nil, httpx.Errorf(http.StatusConflict, "usage_incomplete",
			fmt.Sprintf("Azure usage is synced through %s but %s ends %s — sync first, or create it anyway", synced, q.Label, fmtDay(q.End)))
	}

	aggs, err := s.usage(ctx, tenantID, subIDs(a.subs), periodStart, fmtDay(q.End))
	if err != nil {
		return nil, nil, err
	}
	type acc struct{ cost, fee float64 }
	per := map[string]*acc{}
	currencies := map[string]bool{}
	byID := map[string]subRow{}
	for _, sub := range a.subs {
		byID[sub.ID] = sub
	}
	for _, u := range aggs {
		if per[u.Sub] == nil {
			per[u.Sub] = &acc{}
		}
		per[u.Sub].cost += u.Cost
		per[u.Sub].fee += u.Cost * a.rates.pct(byID[u.Sub], u.Month) / 100
		if u.Currency != "" {
			currencies[u.Currency] = true
		}
	}
	cur := ""
	for c := range currencies {
		cur = c
	}
	var warnings []string
	if len(currencies) > 1 {
		warnings = append(warnings, "usage is in more than one currency; amounts were added as-is")
	}
	if sc := str(a.header["currency"]); cur != "" && sc != "" && sc != cur {
		warnings = append(warnings, fmt.Sprintf("usage is in %s but the agreement is in %s", cur, sc))
	}

	var lines []map[string]any
	for _, sub := range a.subs {
		p := per[sub.ID]
		if p == nil || round2(p.cost) == 0 {
			continue
		}
		pct := round2(p.fee / p.cost * 100)
		typ := sub.Type
		if typ == "" {
			typ = "untyped"
			warnings = append(warnings, sub.Name+" has no type (fee 0%)")
		}
		lines = append(lines, map[string]any{
			"description": fmt.Sprintf("%s — %s fee %.2f%% on %.2f %s Azure usage (%s)", sub.Name, typ, pct, p.cost, cur, q.Label),
			"qty":         1, "unit_price": round2(p.fee),
			"azure_subscription_id": sub.ID, "usage_cost": round2(p.cost), "fee_pct": pct,
		})
	}
	if len(lines) == 0 {
		warnings = append(warnings, "no Azure usage in this quarter")
	}

	terms := paymentTerms(a.header)
	header := map[string]any{
		"account_id": a.header["account_id"], "agreement_id": agreementID.String(),
		"description":  fmt.Sprintf("Azure fees %s", q.Label),
		"invoice_date": fmtDay(today()), "due_date": fmtDay(today().AddDate(0, 0, terms)), "status": "Draft",
		"period_start": periodStart, "period_end": fmtDay(q.End),
		"markup_pct": a.header["markup_pct"], "vat_pct": a.header["vat_pct"], "payment_terms_days": a.header["payment_terms_days"],
		"legal_entity_id": a.header["legal_entity_id"], "contact_id": a.header["contact_id"],
	}
	if inst := findByPeriod(a.schedule, periodStart); inst != nil {
		header["schedule_id"] = inst["id"]
	}
	inv, err := s.docs.SaveDoc(ctx, s.entity("sales_invoices"), tenantID, nil, header, lines, nil)
	if err != nil {
		return nil, nil, err
	}
	return inv, warnings, nil
}

// registerInvoiceHooks trues up the agreement whenever an invoice for one of
// its quarters is saved, edited, cancelled or deleted: an invoiced quarter's
// line and installment follow the invoice; otherwise they're re-priced.
func (s *Service) registerInvoiceHooks() {
	s.docs.OnSave("sales_invoices", func(ctx context.Context, tenantID uuid.UUID, header map[string]any) {
		s.trueUp(ctx, tenantID, header)
	})
	s.repo.Hook("sales_invoices", resource.WriteHooks{After: func(ctx context.Context, tenantID uuid.UUID, before, after map[string]any) {
		for _, row := range []map[string]any{before, after} {
			if row != nil {
				s.trueUp(ctx, tenantID, row)
			}
		}
	}})
}

func (s *Service) trueUp(ctx context.Context, tenantID uuid.UUID, inv map[string]any) {
	id, err := uuid.Parse(str(inv["agreement_id"]))
	ps := day(inv["period_start"])
	if err != nil || ps == "" {
		return
	}
	sa, err := s.repo.Get(ctx, s.entity("invoices"), tenantID, id)
	if err != nil || sa["azure_fees"] != true {
		return
	}
	if _, err := s.Refresh(ctx, tenantID, id, ps); err != nil {
		slog.Warn("azure true-up", "agreement", id, "period", ps, "err", err)
	}
}

func firstNon(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
