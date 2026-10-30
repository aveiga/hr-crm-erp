// Package azure bills fees on Azure usage: it reads daily costs per
// subscription from Azure Cost Management (client-credentials service
// principal per Azure tenant), stores them as azure_usage rows, and turns them
// into quarterly Service Agreement lines/installments and Invoices, plus a
// usage & fee dashboard. The data model lives in the resource catalog
// (azure_tenants, azure_subscriptions, azure_fee_rates, azure_usage,
// sales_invoices); this package holds the behaviour the engine can't express.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to Entra ID (token) and Azure Resource Manager (subscriptions,
// Cost Management). Base URLs are configurable for tests and sovereign clouds.
type Client struct {
	LoginURL      string
	ManagementURL string
	HTTP          *http.Client
}

// Credentials of a service principal in one Azure tenant.
type Credentials struct {
	DirectoryID  string
	ClientID     string
	ClientSecret string
}

// Subscription as listed by ARM.
type Subscription struct {
	ID          string `json:"subscriptionId"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
	TenantID    string `json:"tenantId"`
}

// CostRow is one day's cost of one service in a subscription.
type CostRow struct {
	Date     string // YYYY-MM-DD
	Service  string
	Cost     float64
	Currency string
}

// Token gets an ARM access token with the client-credentials grant.
func (c *Client) Token(ctx context.Context, cr Credentials) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cr.ClientID},
		"client_secret": {cr.ClientSecret},
		"scope":         {strings.TrimRight(c.ManagementURL, "/") + "/.default"},
	}
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", strings.TrimRight(c.LoginURL, "/"), url.PathEscape(cr.DirectoryID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		msg := out.Description
		if i := strings.Index(msg, "\r\n"); i > 0 {
			msg = msg[:i] // Entra appends trace/correlation ids
		}
		return "", fmt.Errorf("sign-in failed (%s): %s", out.Error, msg)
	}
	return out.AccessToken, nil
}

// Subscriptions lists the subscriptions the service principal can see.
func (c *Client) Subscriptions(ctx context.Context, token string) ([]Subscription, error) {
	next := strings.TrimRight(c.ManagementURL, "/") + "/subscriptions?api-version=2022-12-01"
	var all []Subscription
	for next != "" {
		var page struct {
			Value    []Subscription `json:"value"`
			NextLink string         `json:"nextLink"`
		}
		if err := c.call(ctx, http.MethodGet, next, token, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Value...)
		next = page.NextLink
	}
	return all, nil
}

// DailyCosts queries a subscription's actual cost per day and service for
// [from, to] (inclusive dates, at most a year apart).
func (c *Client) DailyCosts(ctx context.Context, token, subscriptionID string, from, to time.Time) ([]CostRow, error) {
	body := map[string]any{
		"type":      "ActualCost",
		"timeframe": "Custom",
		"timePeriod": map[string]string{
			"from": from.Format("2006-01-02") + "T00:00:00Z",
			"to":   to.Format("2006-01-02") + "T23:59:59Z",
		},
		"dataset": map[string]any{
			"granularity": "Daily",
			"aggregation": map[string]any{"totalCost": map[string]string{"name": "Cost", "function": "Sum"}},
			"grouping":    []map[string]string{{"type": "Dimension", "name": "ServiceName"}},
		},
	}
	next := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.CostManagement/query?api-version=2023-11-01",
		strings.TrimRight(c.ManagementURL, "/"), url.PathEscape(subscriptionID))
	var out []CostRow
	for next != "" {
		var page struct {
			Properties struct {
				NextLink string `json:"nextLink"`
				Columns  []struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"columns"`
				Rows [][]any `json:"rows"`
			} `json:"properties"`
		}
		if err := c.call(ctx, http.MethodPost, next, token, body, &page); err != nil {
			return nil, err
		}
		col := map[string]int{}
		for i, cl := range page.Properties.Columns {
			col[strings.ToLower(cl.Name)] = i
		}
		costIdx, ok := firstCol(col, "cost", "totalcost", "pretaxcost")
		dateIdx, ok2 := firstCol(col, "usagedate")
		if !ok || !ok2 {
			return nil, fmt.Errorf("unexpected Cost Management columns: %v", page.Properties.Columns)
		}
		svcIdx, hasSvc := firstCol(col, "servicename")
		curIdx, hasCur := firstCol(col, "currency", "billingcurrency")
		for _, r := range page.Properties.Rows {
			row := CostRow{Cost: num(r[costIdx]), Date: usageDate(r[dateIdx])}
			if hasSvc {
				row.Service = fmt.Sprint(r[svcIdx])
			}
			if hasCur {
				row.Currency = fmt.Sprint(r[curIdx])
			}
			if row.Date != "" {
				out = append(out, row)
			}
		}
		next = page.Properties.NextLink
	}
	return out, nil
}

// call performs an ARM request, retrying throttled (429) and transient (5xx)
// responses a few times with the server's suggested delay.
func (c *Client) call(ctx context.Context, method, endpoint, token string, body, out any) error {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http().Do(req)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return json.Unmarshal(raw, out)
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < 4 {
			if err := sleep(ctx, retryAfter(resp.Header, attempt)); err != nil {
				return err
			}
			continue
		}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error.Message != "" {
			return fmt.Errorf("Azure %s (HTTP %d): %s", e.Error.Code, resp.StatusCode, e.Error.Message)
		}
		return fmt.Errorf("Azure HTTP %d", resp.StatusCode)
	}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// retryAfter reads Cost Management's throttling headers (seconds), falling
// back to a short exponential backoff.
func retryAfter(h http.Header, attempt int) time.Duration {
	for _, k := range []string{
		"x-ms-ratelimit-microsoft.costmanagement-entity-retry-after",
		"x-ms-ratelimit-microsoft.costmanagement-tenant-retry-after",
		"x-ms-ratelimit-microsoft.costmanagement-client-retry-after",
		"Retry-After",
	} {
		if n, err := strconv.Atoi(h.Get(k)); err == nil && n > 0 {
			return time.Duration(min(n, 120)) * time.Second
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstCol(cols map[string]int, names ...string) (int, bool) {
	for _, n := range names {
		if i, ok := cols[n]; ok {
			return i, true
		}
	}
	return 0, false
}

// usageDate normalises Cost Management's UsageDate (20260131 as a number, or
// an ISO timestamp string) to YYYY-MM-DD.
func usageDate(v any) string {
	s := fmt.Sprint(v)
	if f, ok := v.(float64); ok {
		s = strconv.FormatInt(int64(f), 10)
	}
	switch {
	case len(s) == 8 && !strings.Contains(s, "-"):
		return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
	case len(s) >= 10 && s[4] == '-':
		return s[:10]
	}
	return ""
}
