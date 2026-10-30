package azure

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hrcrm/internal/httpx"
)

// CSV import: a fallback for when the API connection isn't available. It reads
// a Cost Management "Cost analysis" download (or a cost details export) with a
// date, a subscription and a cost column, compares it per subscription and day
// with the usage already stored, and applies it: new days are added, days
// whose total differs are only replaced when the user says so.

// importRow is one (subscription, day, service) cost from the file.
type importRow struct {
	subKey, subName, date, service, currency string
	cost                                     float64
}

type parsedCSV struct {
	rows    []importRow
	from    string
	to      string
	monthly bool // every date is the 1st: a monthly-granularity download
}

var subGUID = regexp.MustCompile(`(?i)\(?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\)?\s*$`)

// normHeader lowercases a header and drops everything but letters/digits, so
// "UsageDate", "Usage date" and "usage_date" match.
func normHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimPrefix(h, "\ufeff")) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseCostCSV(r io.Reader) (*parsedCSV, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, badCSV("the file is empty or not a CSV")
	}
	col := map[string]int{}
	for i, h := range head {
		if _, dup := col[normHeader(h)]; !dup {
			col[normHeader(h)] = i
		}
	}
	pick := func(names ...string) int {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return i
			}
		}
		return -1
	}
	dateI := pick("usagedate", "date")
	subIDI := pick("subscriptionid", "subscriptionguid")
	subNameI := pick("subscriptionname", "subscription")
	svcI := pick("servicename", "metercategory")
	costI := pick("cost", "costinbillingcurrency", "pretaxcost", "totalcost")
	curI := pick("currency", "billingcurrency", "billingcurrencycode")
	usd := false
	if costI < 0 {
		if costI = pick("costusd", "costinusd", "pretaxcostusd"); costI >= 0 {
			usd = true
		}
	}
	if dateI < 0 || costI < 0 || (subIDI < 0 && subNameI < 0) {
		return nil, badCSV(fmt.Sprintf("expected UsageDate, SubscriptionName (or SubscriptionId) and Cost columns; found: %s",
			strings.Join(head, ", ")))
	}

	p := &parsedCSV{monthly: true}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, badCSV(fmt.Sprintf("line %d: %v", line, err))
		}
		get := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		if strings.Join(rec, "") == "" {
			continue
		}
		date, ok := csvDate(get(dateI))
		if !ok {
			return nil, badCSV(fmt.Sprintf("line %d: unrecognised date %q (expected YYYY-MM-DD)", line, get(dateI)))
		}
		cost, err := strconv.ParseFloat(strings.ReplaceAll(get(costI), ",", ""), 64)
		if err != nil {
			return nil, badCSV(fmt.Sprintf("line %d: unrecognised cost %q", line, get(costI)))
		}
		row := importRow{date: date, cost: cost, service: get(svcI), currency: get(curI)}
		if usd {
			row.currency = "USD"
		}
		// The subscription: an explicit id, else the GUID in "Name(guid)",
		// else the name itself.
		name := get(subNameI)
		row.subName = strings.TrimSpace(subGUID.ReplaceAllString(name, ""))
		switch {
		case get(subIDI) != "":
			row.subKey = strings.ToLower(get(subIDI))
		case subGUID.MatchString(name):
			row.subKey = strings.ToLower(subGUID.FindStringSubmatch(name)[1])
		default:
			row.subKey = "name:" + strings.ToLower(name)
		}
		if row.subName == "" {
			row.subName = get(subIDI)
		}
		if row.subKey == "name:" {
			return nil, badCSV(fmt.Sprintf("line %d: no subscription", line))
		}
		if !strings.HasSuffix(date, "-01") {
			p.monthly = false
		}
		if p.from == "" || date < p.from {
			p.from = date
		}
		if date > p.to {
			p.to = date
		}
		p.rows = append(p.rows, row)
	}
	if len(p.rows) == 0 {
		return nil, badCSV("the file has no data rows")
	}
	if p.from == p.to {
		p.monthly = false // a single day can't tell
	}
	return p, nil
}

func badCSV(msg string) error { return httpx.Errorf(http.StatusBadRequest, "bad_csv", msg) }

// csvDate accepts YYYY-MM-DD (optionally with a time), YYYYMMDD and MM/DD/YYYY.
func csvDate(s string) (string, bool) {
	for _, layout := range []string{"2006-01-02", "20060102", "01/02/2006", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05"} {
		v := s
		if layout == "2006-01-02" && len(v) > 10 {
			v = v[:10]
		}
		if t, err := time.Parse(layout, v); err == nil {
			return fmtDay(t), true
		}
	}
	return "", false
}

// ImportSub is a subscription found in the file.
type ImportSub struct {
	Key            string  `json:"key"`
	Name           string  `json:"name"`
	SubscriptionID string  `json:"subscription_id"`
	RowID          string  `json:"row_id,omitempty"` // empty = will be created
	Days           int     `json:"days"`
	Total          float64 `json:"total"`
	Currency       string  `json:"currency"`
}

// ImportChange is a subscription-day whose stored total differs from the file.
type ImportChange struct {
	Key              string  `json:"key"` // "<subscription key>|<date>"
	Subscription     string  `json:"subscription"`
	Date             string  `json:"date"`
	Existing         float64 `json:"existing"`
	ExistingCurrency string  `json:"existing_currency"`
	Incoming         float64 `json:"incoming"`
	Currency         string  `json:"currency"`
}

// ImportPreview is the comparison of a file with the stored usage.
type ImportPreview struct {
	Rows          int            `json:"rows"`
	From          string         `json:"from"`
	To            string         `json:"to"`
	Subscriptions []ImportSub    `json:"subscriptions"`
	NewDays       int            `json:"new_days"`
	UnchangedDays int            `json:"unchanged_days"`
	Changes       []ImportChange `json:"changes"`
	Warnings      []string       `json:"warnings,omitempty"`
}

// ImportResult reports what an import wrote.
type ImportResult struct {
	ImportPreview
	Added            int    `json:"added_days"`
	Overwritten      int    `json:"overwritten_days"`
	KeptExisting     int    `json:"kept_days"`
	NewSubscriptions int    `json:"new_subscriptions"`
	SyncedThrough    string `json:"synced_through"`
}

type dayKey struct{ sub, date string }

type dayTotal struct {
	cost float64
	cur  string
}

// compare builds the preview: the file's daily totals per subscription vs
// the stored ones.
func (s *Service) compare(ctx context.Context, tenantID uuid.UUID, p *parsedCSV) (*ImportPreview, map[string]string, map[dayKey]dayTotal, map[dayKey]dayTotal, error) {
	// Known subscriptions by GUID and by name.
	e := s.entity("azure_subscriptions")
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id::text, lower(COALESCE(subscription_id,'')), lower(COALESCE(display_name,''))
		FROM %s WHERE tenant_id=$1`, e.Table()), tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	known := map[string]string{} // file key -> row id
	for rows.Next() {
		var id, guid, name string
		if err := rows.Scan(&id, &guid, &name); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		if guid != "" {
			known[guid] = id
		}
		if name != "" {
			if _, taken := known["name:"+name]; !taken {
				known["name:"+name] = id
			}
		}
	}
	rows.Close()

	incoming := map[dayKey]dayTotal{}
	subs := map[string]*ImportSub{}
	var order []string
	for _, r := range p.rows {
		k := dayKey{r.subKey, r.date}
		t := incoming[k]
		t.cost += r.cost
		if t.cur == "" {
			t.cur = r.currency
		}
		incoming[k] = t
		if subs[r.subKey] == nil {
			guid := ""
			if !strings.HasPrefix(r.subKey, "name:") {
				guid = r.subKey
			}
			subs[r.subKey] = &ImportSub{Key: r.subKey, Name: r.subName, SubscriptionID: guid, RowID: known[r.subKey], Currency: r.currency}
			order = append(order, r.subKey)
		}
		subs[r.subKey].Total += r.cost
	}

	// Stored daily totals for the known subscriptions over the file's range.
	var ids []string
	rowOf := map[string]string{}
	for _, k := range order {
		if id := subs[k].RowID; id != "" {
			ids = append(ids, id)
			rowOf[id] = k
		}
	}
	existing := map[dayKey]dayTotal{}
	if len(ids) > 0 {
		rs, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT azure_subscription_id, to_char(usage_date,'YYYY-MM-DD'), sum(cost), min(COALESCE(currency,''))
			FROM %s WHERE tenant_id=$1 AND azure_subscription_id = ANY($2) AND usage_date BETWEEN $3 AND $4 GROUP BY 1,2`,
			s.entity("azure_usage").Table()), tenantID, ids, p.from, p.to)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for rs.Next() {
			var id, d, cur string
			var c float64
			if err := rs.Scan(&id, &d, &c, &cur); err != nil {
				rs.Close()
				return nil, nil, nil, nil, err
			}
			existing[dayKey{rowOf[id], d}] = dayTotal{c, cur}
		}
		rs.Close()
	}

	pv := &ImportPreview{Rows: len(p.rows), From: p.from, To: p.to, Subscriptions: []ImportSub{}, Changes: []ImportChange{}}
	for k, in := range incoming {
		subs[k.sub].Days++
		old, ok := existing[k]
		switch {
		case !ok:
			pv.NewDays++
		case !isChange(old, in):
			pv.UnchangedDays++
		default:
			pv.Changes = append(pv.Changes, ImportChange{Key: k.sub + "|" + k.date, Subscription: subs[k.sub].Name, Date: k.date,
				Existing: round2(old.cost), ExistingCurrency: old.cur, Incoming: round2(in.cost), Currency: in.cur})
		}
	}
	sort.Slice(pv.Changes, func(i, j int) bool {
		if pv.Changes[i].Subscription != pv.Changes[j].Subscription {
			return pv.Changes[i].Subscription < pv.Changes[j].Subscription
		}
		return pv.Changes[i].Date < pv.Changes[j].Date
	})
	newSubs := 0
	for _, k := range order {
		sub := subs[k]
		sub.Total = round2(sub.Total)
		pv.Subscriptions = append(pv.Subscriptions, *sub)
		if sub.RowID == "" {
			newSubs++
		}
	}
	if newSubs > 0 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("%d subscription(s) aren't in the app yet and will be created (unmapped)", newSubs))
	}
	if p.monthly {
		pv.Warnings = append(pv.Warnings, "every date is the 1st of a month — this looks like a Monthly download; each month's cost would land on its first day. Download with Daily granularity for forecasts and part-quarter periods to be right.")
	}
	return pv, known, incoming, existing, nil
}

// PreviewImport parses a file and compares it with the stored usage.
func (s *Service) PreviewImport(ctx context.Context, tenantID uuid.UUID, r io.Reader) (*ImportPreview, error) {
	p, err := parseCostCSV(r)
	if err != nil {
		return nil, err
	}
	pv, _, _, _, err := s.compare(ctx, tenantID, p)
	return pv, err
}

// ApplyImport writes a file's usage: new subscription-days are added; days
// that differ from what's stored are replaced only when listed in overwrite
// (keys from the preview) or when overwriteAll. Subscriptions not in the app
// are created under the given Azure tenant record, whose "synced through" date
// advances to the file's last complete day.
func (s *Service) ApplyImport(ctx context.Context, tenantID, azTenantID uuid.UUID, r io.Reader, overwrite map[string]bool, overwriteAll bool) (*ImportResult, error) {
	tenants := s.entity("azure_tenants")
	azRow, err := s.repo.Get(ctx, tenants, tenantID, azTenantID)
	if err != nil {
		return nil, httpx.Errorf(http.StatusBadRequest, "validation", "pick the Azure tenant these subscriptions belong to")
	}
	p, err := parseCostCSV(r)
	if err != nil {
		return nil, err
	}
	pv, known, incoming, existing, err := s.compare(ctx, tenantID, p)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{ImportPreview: *pv}

	// Create missing subscriptions.
	subsE := s.entity("azure_subscriptions")
	for i, sub := range pv.Subscriptions {
		if sub.RowID != "" {
			continue
		}
		in := map[string]any{"display_name": sub.Name, "subscription_id": sub.SubscriptionID, "azure_tenant_id": azTenantID.String()}
		if sub.SubscriptionID == "" {
			in["subscription_id"] = sub.Name // matched by name until a sync brings the id
		}
		row, err := s.repo.Create(ctx, subsE, tenantID, in)
		if err != nil {
			return nil, err
		}
		known[sub.Key] = str(row["id"])
		res.Subscriptions[i].RowID = str(row["id"])
		res.NewSubscriptions++
	}

	// Which subscription-days to write.
	write := map[dayKey]bool{}
	for k := range incoming {
		_, had := existing[k]
		switch {
		case !had:
			write[k] = true
			res.Added++
		case overwriteAll || overwrite[k.sub+"|"+k.date]:
			if isChange(existing[k], incoming[k]) {
				write[k] = true
				res.Overwritten++
			}
		default:
			if isChange(existing[k], incoming[k]) {
				res.KeptExisting++
			}
		}
	}

	// Rows per subscription for the days being written (service detail kept).
	type batch struct {
		dates, svcs, curs []string
		costs             []float64
		days              map[string]bool
	}
	bySub := map[string]*batch{}
	agg := map[[3]string]*importRow{}
	var aggOrder [][3]string
	for _, r := range p.rows {
		if !write[dayKey{r.subKey, r.date}] {
			continue
		}
		k := [3]string{r.subKey, r.date, r.service}
		if agg[k] == nil {
			rr := r
			rr.cost = 0
			agg[k] = &rr
			aggOrder = append(aggOrder, k)
		}
		agg[k].cost += r.cost
	}
	for _, k := range aggOrder {
		r := agg[k]
		id := known[r.subKey]
		b := bySub[id]
		if b == nil {
			b = &batch{days: map[string]bool{}}
			bySub[id] = b
		}
		b.days[r.date] = true
		if r.cost != 0 {
			svc := r.service
			if svc == "" {
				svc = "(all services)"
			}
			b.dates, b.svcs, b.curs, b.costs = append(b.dates, r.date), append(b.svcs, svc), append(b.curs, r.currency), append(b.costs, r.cost)
		}
	}

	usage := s.entity("azure_usage")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for id, b := range bySub {
		days := make([]string, 0, len(b.days))
		for d := range b.days {
			days = append(days, d)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id=$1 AND azure_subscription_id=$2 AND usage_date = ANY($3::date[])`,
			usage.Table()), tenantID, id, days); err != nil {
			return nil, err
		}
		if len(b.dates) > 0 {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (tenant_id, azure_subscription_id, usage_date, service_name, cost, currency)
				SELECT $1, $2, d::date, sv, c, cu FROM unnest($3::text[], $4::text[], $5::float8[], $6::text[]) AS u(d, sv, c, cu)`,
				usage.Table()), tenantID, id, b.dates, b.svcs, b.costs, b.curs); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// As with a sync, today is still accruing: the file counts as complete
	// through yesterday at most. Never move "synced through" backwards.
	through := p.to
	if limit := fmtDay(today().AddDate(0, 0, -1)); through > limit {
		through = limit
	}
	if p.monthly {
		through = "" // a monthly file says nothing about which days are in
	}
	res.SyncedThrough = day(azRow["synced_through"])
	if through > res.SyncedThrough {
		res.SyncedThrough = through
		_, _ = s.repo.Update(ctx, tenants, tenantID, azTenantID, map[string]any{"synced_through": through})
	}
	return res, nil
}

func isChange(old, in dayTotal) bool {
	return math.Abs(round2(old.cost)-round2(in.cost)) >= 0.005 || (old.cur != in.cur && old.cur != "" && in.cur != "")
}
