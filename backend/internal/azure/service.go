package azure

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
	"hrcrm/internal/resource"
)

// Service syncs Azure usage and bills fees on it.
type Service struct {
	pool       *pgxpool.Pool
	reg        *resource.Registry
	repo       *resource.Repo
	docs       *resource.DocService
	client     *Client
	secretsKey string

	mu      sync.Mutex
	running map[uuid.UUID]bool // Azure tenant records being synced
}

func NewService(pool *pgxpool.Pool, docs *resource.DocService, client *Client, secretsKey string) *Service {
	s := &Service{pool: pool, reg: docs.Registry(), repo: docs.Repo(), docs: docs, client: client,
		secretsKey: secretsKey, running: map[uuid.UUID]bool{}}
	s.registerInvoiceHooks()
	return s
}

func (s *Service) entity(key string) resource.Entity {
	e, ok := s.reg.Get(key)
	if !ok {
		panic("azure: catalog entity missing: " + key)
	}
	return e
}

// SyncResult summarises one sync run.
type SyncResult struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	Subscriptions int      `json:"subscriptions"`
	New           int      `json:"new_subscriptions"`
	UsageRows     int      `json:"usage_rows"`
	Errors        []string `json:"errors,omitempty"`
}

var errSyncRunning = httpx.Errorf(http.StatusConflict, "sync_running", "a sync of this Azure tenant is already running")

// Sync pulls the subscriptions of one Azure tenant record and their daily
// costs for [from, to] (whole days; usage in that window is replaced). The
// record's sync status/time and "synced through" date are updated.
func (s *Service) Sync(ctx context.Context, tenantID, azTenantID uuid.UUID, from, to time.Time) (SyncResult, error) {
	s.mu.Lock()
	if s.running[azTenantID] {
		s.mu.Unlock()
		return SyncResult{}, errSyncRunning
	}
	s.running[azTenantID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, azTenantID)
		s.mu.Unlock()
	}()

	tenants := s.entity("azure_tenants")
	row, err := s.repo.Get(ctx, tenants, tenantID, azTenantID)
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{From: fmtDay(from), To: fmtDay(to)}
	_, _ = s.repo.Update(ctx, tenants, tenantID, azTenantID, map[string]any{"sync_status": "Running"})

	finish := func(err error) (SyncResult, error) {
		status, msg := "OK", fmt.Sprintf("%d subscriptions (%d new), %d usage rows for %s – %s",
			res.Subscriptions, res.New, res.UsageRows, res.From, res.To)
		upd := map[string]any{"last_sync_at": time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			status, msg = "Failed", err.Error()
		} else if len(res.Errors) > 0 {
			status, msg = "Failed", msg+"\n"+strings.Join(res.Errors, "\n")
		} else if through := fmtDay(to.AddDate(0, 0, -1)); through > day(row["synced_through"]) {
			// The window's last day is still accruing; earlier days count as in.
			upd["synced_through"] = through
		}
		upd["sync_status"], upd["sync_message"] = status, msg
		_, _ = s.repo.Update(context.WithoutCancel(ctx), tenants, tenantID, azTenantID, upd)
		return res, err
	}

	secret, err := s.secret(ctx, tenantID, azTenantID)
	if err != nil {
		return finish(err)
	}
	token, err := s.client.Token(ctx, Credentials{DirectoryID: str(row["directory_id"]), ClientID: str(row["client_id"]), ClientSecret: secret})
	if err != nil {
		return finish(err)
	}
	subs, err := s.client.Subscriptions(ctx, token)
	if err != nil {
		return finish(fmt.Errorf("list subscriptions: %w", err))
	}
	ids, created, err := s.upsertSubscriptions(ctx, tenantID, azTenantID, subs)
	if err != nil {
		return finish(err)
	}
	res.Subscriptions, res.New = len(subs), created

	for _, sub := range subs {
		n, err := s.syncCosts(ctx, tenantID, token, sub.ID, ids[strings.ToLower(sub.ID)], from, to)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s (%s): %v", sub.DisplayName, sub.ID, err))
			continue
		}
		res.UsageRows += n
	}
	return finish(nil)
}

// upsertSubscriptions creates rows for newly seen subscriptions (unmapped)
// and refreshes name/state of known ones. Returns subscription GUID → row id.
func (s *Service) upsertSubscriptions(ctx context.Context, tenantID, azTenantID uuid.UUID, subs []Subscription) (map[string]string, int, error) {
	e := s.entity("azure_subscriptions")
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id::text, lower(subscription_id), COALESCE(display_name,''), COALESCE(state,'')
		FROM %s WHERE tenant_id=$1`, e.Table()), tenantID)
	if err != nil {
		return nil, 0, err
	}
	type known struct{ id, name, state string }
	have := map[string]known{}
	for rows.Next() {
		var id, sid, name, state string
		if err := rows.Scan(&id, &sid, &name, &state); err != nil {
			rows.Close()
			return nil, 0, err
		}
		have[sid] = known{id, name, state}
	}
	rows.Close()

	ids, created := map[string]string{}, 0
	for _, sub := range subs {
		key := strings.ToLower(sub.ID)
		if k, ok := have[key]; ok {
			ids[key] = k.id
			if k.name != sub.DisplayName || k.state != sub.State {
				uid, _ := uuid.Parse(k.id)
				if _, err := s.repo.Update(ctx, e, tenantID, uid, map[string]any{"display_name": sub.DisplayName, "state": sub.State}); err != nil {
					return nil, 0, err
				}
			}
			continue
		}
		row, err := s.repo.Create(ctx, e, tenantID, map[string]any{
			"display_name": sub.DisplayName, "subscription_id": sub.ID, "state": sub.State,
			"azure_tenant_id": azTenantID.String(),
		})
		if err != nil {
			return nil, 0, err
		}
		ids[key] = str(row["id"])
		created++
	}
	return ids, created, nil
}

// syncCosts replaces a subscription's usage rows in [from, to] with fresh
// Cost Management data, queried a month at a time.
func (s *Service) syncCosts(ctx context.Context, tenantID uuid.UUID, token, subscriptionID, rowID string, from, to time.Time) (int, error) {
	var all []CostRow
	for start := from; !start.After(to); {
		end := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		if end.After(to) {
			end = to
		}
		rows, err := s.client.DailyCosts(ctx, token, subscriptionID, start, end)
		if err != nil {
			return 0, err
		}
		all = append(all, rows...)
		start = end.AddDate(0, 0, 1)
	}

	// Aggregate (a page boundary can split a day/service) and drop zero rows.
	type k struct{ date, svc, cur string }
	sum := map[k]float64{}
	var order []k
	for _, r := range all {
		key := k{r.Date, r.Service, r.Currency}
		if _, ok := sum[key]; !ok {
			order = append(order, key)
		}
		sum[key] += r.Cost
	}
	var dates, svcs, curs []string
	var costs []float64
	for _, key := range order {
		if c := sum[key]; c != 0 {
			dates, svcs, curs, costs = append(dates, key.date), append(svcs, key.svc), append(curs, key.cur), append(costs, c)
		}
	}

	usage := s.entity("azure_usage")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id=$1 AND azure_subscription_id=$2
		AND usage_date BETWEEN $3 AND $4`, usage.Table()), tenantID, rowID, fmtDay(from), fmtDay(to)); err != nil {
		return 0, err
	}
	if len(dates) > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (tenant_id, azure_subscription_id, usage_date, service_name, cost, currency)
			SELECT $1, $2, d::date, sv, c, cu FROM unnest($3::text[], $4::text[], $5::float8[], $6::text[]) AS u(d, sv, c, cu)`,
			usage.Table()), tenantID, rowID, dates, svcs, costs, curs); err != nil {
			return 0, err
		}
	}
	return len(dates), tx.Commit(ctx)
}

// TestConnection signs in and lists subscriptions without storing anything.
func (s *Service) TestConnection(ctx context.Context, tenantID, azTenantID uuid.UUID) ([]Subscription, error) {
	row, err := s.repo.Get(ctx, s.entity("azure_tenants"), tenantID, azTenantID)
	if err != nil {
		return nil, err
	}
	secret, err := s.secret(ctx, tenantID, azTenantID)
	if err != nil {
		return nil, httpx.Errorf(http.StatusBadRequest, "no_secret", err.Error())
	}
	token, err := s.client.Token(ctx, Credentials{DirectoryID: str(row["directory_id"]), ClientID: str(row["client_id"]), ClientSecret: secret})
	if err != nil {
		return nil, httpx.Errorf(http.StatusBadGateway, "azure_auth", err.Error())
	}
	subs, err := s.client.Subscriptions(ctx, token)
	if err != nil {
		return nil, httpx.Errorf(http.StatusBadGateway, "azure_error", err.Error())
	}
	return subs, nil
}

// DefaultWindow is the window a routine sync refreshes: from the start of the
// previous month (late cost adjustments land there) through today.
func DefaultWindow() (time.Time, time.Time) {
	t := today()
	return time.Date(t.Year(), t.Month()-1, 1, 0, 0, 0, 0, time.UTC), t
}

// RunScheduler syncs every active Azure tenant record of every app tenant
// once per interval until ctx ends. The sweep reads tenant ids across tenants
// by design (a system job); each sync is then tenant-scoped.
func (s *Service) RunScheduler(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		s.syncAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) syncAll(ctx context.Context) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT tenant_id, id FROM %s WHERE active`, s.entity("azure_tenants").Table()))
	if err != nil {
		slog.Error("azure sync sweep", "err", err)
		return
	}
	type pair struct{ tenant, az uuid.UUID }
	var todo []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.tenant, &p.az) == nil {
			todo = append(todo, p)
		}
	}
	rows.Close()
	from, to := DefaultWindow()
	for _, p := range todo {
		res, err := s.Sync(ctx, p.tenant, p.az, from, to)
		if err != nil || len(res.Errors) > 0 {
			slog.Warn("azure sync", "tenant", p.tenant, "azure_tenant", p.az, "err", err, "errors", res.Errors)
		} else {
			slog.Info("azure sync", "tenant", p.tenant, "azure_tenant", p.az, "rows", res.UsageRows)
		}
	}
}
