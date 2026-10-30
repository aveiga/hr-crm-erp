package resource

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hrcrm/internal/httpx"
)

type Repo struct {
	pool  *pgxpool.Pool
	reg   *Registry             // enables field rules; see rules.go
	hooks map[string]WriteHooks // per-entity write hooks
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// ListOptions controls pagination, search, sorting and equality filters.
type ListOptions struct {
	Page     int
	PageSize int
	Search   string
	Sort     string            // field key
	Desc     bool
	Filters  map[string]string // field key -> value (equality; "@empty" = blank)
	// In matches any of several values; Min/Max are inclusive bounds for
	// dates and numbers. Values may use date tokens (see resolveToken).
	In  map[string][]string
	Min map[string]string
	Max map[string]string
}

type ListResult struct {
	Data     []map[string]any `json:"data"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

func (r *Repo) List(ctx context.Context, e Entity, tenantID uuid.UUID, opt ListOptions) (ListResult, error) {
	if opt.Page < 1 {
		opt.Page = 1
	}
	if opt.PageSize < 1 || opt.PageSize > 200 {
		opt.PageSize = 50
	}

	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	argN := 2

	// Equality filters on known fields.
	for k, v := range opt.Filters {
		f, ok := e.field(k)
		if !ok || v == "" {
			continue
		}
		if v == "@empty" {
			where = append(where, fmt.Sprintf("(%s IS NULL OR %s::text = '')", quoteIdent(k), quoteIdent(k)))
			continue
		}
		arg := filterArg(f, resolveToken(v))
		if arg == nil {
			arg = v // as before: let Postgres cast the raw value
		}
		where = append(where, fmt.Sprintf("%s = $%d", quoteIdent(k), argN))
		args = append(args, arg)
		argN++
	}
	for k, vals := range opt.In {
		if _, ok := e.field(k); !ok || len(vals) == 0 {
			continue
		}
		where = append(where, fmt.Sprintf("%s::text = ANY($%d)", quoteIdent(k), argN))
		args = append(args, vals)
		argN++
	}
	for op, bounds := range map[string]map[string]string{">=": opt.Min, "<=": opt.Max} {
		for k, v := range bounds {
			f, ok := e.field(k)
			if !ok || v == "" || !isRangeable(f.Type) {
				continue
			}
			arg := filterArg(f, resolveToken(v))
			if arg == nil {
				continue // unparseable bound: ignore rather than fail the list
			}
			where = append(where, fmt.Sprintf("%s %s $%d", quoteIdent(k), op, argN))
			args = append(args, arg)
			argN++
		}
	}

	// Free-text search across text-like fields.
	if s := strings.TrimSpace(opt.Search); s != "" {
		var ors []string
		for _, f := range e.Fields {
			if isTextual(f.Type) {
				ors = append(ors, fmt.Sprintf("%s ILIKE $%d", quoteIdent(f.Key), argN))
			}
		}
		if len(ors) > 0 {
			where = append(where, "("+strings.Join(ors, " OR ")+")")
			args = append(args, "%"+s+"%")
			argN++
		}
	}

	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", e.Table(), whereSQL), args...,
	).Scan(&total); err != nil {
		return ListResult{}, err
	}

	orderBy := "created_at"
	if _, ok := e.field(opt.Sort); ok {
		orderBy = quoteIdent(opt.Sort)
	}
	dir := "ASC"
	if opt.Desc {
		dir = "DESC"
	}
	offset := (opt.Page - 1) * opt.PageSize

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s %s LIMIT %d OFFSET %d",
		selectCols(e), e.Table(), whereSQL, orderBy, dir, opt.PageSize, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()

	data, err := scanRows(rows)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Data: data, Total: total, Page: opt.Page, PageSize: opt.PageSize}, nil
}

func (r *Repo) Get(ctx context.Context, e Entity, tenantID, id uuid.UUID) (map[string]any, error) {
	query := fmt.Sprintf("SELECT %s FROM %s WHERE tenant_id=$1 AND id=$2", selectCols(e), e.Table())
	rows, err := r.pool.Query(ctx, query, tenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, httpx.ErrNotFound
	}
	return data[0], nil
}

func (r *Repo) Create(ctx context.Context, e Entity, tenantID uuid.UUID, input map[string]any) (map[string]any, error) {
	input, err := r.prepareWrite(ctx, e, tenantID, nil, input)
	if err != nil {
		return nil, err
	}
	cols := []string{"tenant_id"}
	placeholders := []string{"$1"}
	args := []any{tenantID}
	argN := 2

	for _, f := range e.Fields {
		raw, present := input[f.Key]
		if f.Required && (!present || isEmpty(raw)) {
			return nil, httpx.Errorf(http.StatusBadRequest, "validation", fmt.Sprintf("%s is required", f.Label))
		}
		if !present {
			continue
		}
		val, err := coerce(f, raw)
		if err != nil {
			return nil, err
		}
		cols = append(cols, quoteIdent(f.Key))
		placeholders = append(placeholders, fmt.Sprintf("$%d", argN))
		args = append(args, val)
		argN++
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING %s",
		e.Table(), strings.Join(cols, ","), strings.Join(placeholders, ","), selectCols(e))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	data, err := scanRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	r.afterWrite(ctx, e, tenantID, nil, data[0])
	return data[0], nil
}

func (r *Repo) Update(ctx context.Context, e Entity, tenantID, id uuid.UUID, input map[string]any) (map[string]any, error) {
	var stored map[string]any
	if r.reg != nil {
		var err error
		if stored, err = r.Get(ctx, e, tenantID, id); err != nil {
			return nil, err
		}
		if input, err = r.prepareWrite(ctx, e, tenantID, stored, input); err != nil {
			return nil, err
		}
	}
	sets := []string{"updated_at = now()"}
	args := []any{tenantID, id}
	argN := 3

	for _, f := range e.Fields {
		raw, present := input[f.Key]
		if !present {
			continue
		}
		if f.Required && isEmpty(raw) {
			return nil, httpx.Errorf(http.StatusBadRequest, "validation", fmt.Sprintf("%s is required", f.Label))
		}
		val, err := coerce(f, raw)
		if err != nil {
			return nil, err
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdent(f.Key), argN))
		args = append(args, val)
		argN++
	}

	query := fmt.Sprintf("UPDATE %s SET %s WHERE tenant_id=$1 AND id=$2 RETURNING %s",
		e.Table(), strings.Join(sets, ","), selectCols(e))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	data, err := scanRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, httpx.ErrNotFound
	}
	r.afterWrite(ctx, e, tenantID, stored, data[0])
	return data[0], nil
}

func (r *Repo) Delete(ctx context.Context, e Entity, tenantID, id uuid.UUID) error {
	var before map[string]any
	if r.hooked(e) {
		var err error
		if before, err = r.Get(ctx, e, tenantID, id); err != nil {
			return err
		}
	}
	tag, err := r.pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id=$1 AND id=$2", e.Table()), tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	r.afterWrite(ctx, e, tenantID, before, nil)
	return nil
}

// Count returns the number of rows for an entity in a tenant (used by the
// dashboard).
func (r *Repo) Count(ctx context.Context, e Entity, tenantID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE tenant_id=$1", e.Table()), tenantID).Scan(&n)
	return n, err
}

// SumMoney sums a money/number field, optionally filtered by an equality match.
func (r *Repo) SumMoney(ctx context.Context, e Entity, tenantID uuid.UUID, field, filterField, filterVal string) (float64, error) {
	if _, ok := e.field(field); !ok {
		return 0, fmt.Errorf("unknown field %s", field)
	}
	q := fmt.Sprintf("SELECT COALESCE(sum(%s),0) FROM %s WHERE tenant_id=$1", quoteIdent(field), e.Table())
	args := []any{tenantID}
	if filterField != "" {
		if _, ok := e.field(filterField); ok {
			q += fmt.Sprintf(" AND %s = $2", quoteIdent(filterField))
			args = append(args, filterVal)
		}
	}
	var sum float64
	err := r.pool.QueryRow(ctx, q, args...).Scan(&sum)
	return sum, err
}

// --- helpers ---------------------------------------------------------------

func selectCols(e Entity) string {
	cols := []string{"id::text AS id"}
	for _, f := range e.Fields {
		cols = append(cols, quoteIdent(f.Key))
	}
	cols = append(cols, "created_at", "updated_at")
	return strings.Join(cols, ", ")
}

func scanRows(rows pgx.Rows) ([]map[string]any, error) {
	fds := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			v := vals[i]
			if t, ok := v.(time.Time); ok {
				v = t.Format(time.RFC3339)
			}
			m[string(fd.Name)] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func isRangeable(t FieldType) bool {
	switch t {
	case TypeDate, TypeDateTime, TypeInt, TypeNumber, TypeMoney, TypePercent:
		return true
	}
	return false
}

// filterArg converts a filter value to the column's type; nil when it can't.
func filterArg(f Field, v string) any {
	switch f.Type {
	case TypeDate, TypeDateTime, TypeInt, TypeNumber, TypeMoney, TypePercent, TypeBool:
		val, err := coerce(f, v)
		if err != nil {
			return nil
		}
		return val
	}
	return v
}

// resolveToken expands relative date tokens in filter values so saved views
// stay current: @today, @today+N / @today-N (days), @month (first of month).
func resolveToken(v string) string {
	now := time.Now()
	switch {
	case v == "@today":
		return now.Format("2006-01-02")
	case v == "@month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	case strings.HasPrefix(v, "@today+") || strings.HasPrefix(v, "@today-"):
		n, err := strconv.Atoi(v[len("@today"):])
		if err != nil {
			return v
		}
		return now.AddDate(0, 0, n).Format("2006-01-02")
	}
	return v
}

func isTextual(t FieldType) bool {
	switch t {
	case TypeText, TypeLongText, TypeSelect, TypeRef, TypeEmail:
		return true
	}
	return false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// coerce converts a JSON-decoded value into the Go type pgx expects for the
// field's column. Empty strings become NULL.
func coerce(f Field, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch f.Type {
	case TypeInt:
		return toInt(f, raw)
	case TypeNumber, TypeMoney, TypePercent:
		return toFloat(f, raw)
	case TypeBool:
		switch b := raw.(type) {
		case bool:
			return b, nil
		case string: // catalog defaults are strings
			if v, err := strconv.ParseBool(b); err == nil {
				return v, nil
			}
		}
		return nil, badType(f, "boolean")
	case TypeDate:
		return toDate(f, raw)
	case TypeDateTime:
		return toDateTime(f, raw)
	default: // text-like
		s := fmt.Sprint(raw)
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return s, nil
	}
}

func toInt(f Field, raw any) (any, error) {
	switch v := raw.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, badType(f, "integer")
		}
		return n, nil
	default:
		return nil, badType(f, "integer")
	}
}

func toFloat(f Field, raw any) (any, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case int:
		return float64(v), nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, badType(f, "number")
		}
		return n, nil
	default:
		return nil, badType(f, "number")
	}
}

func toDate(f Field, raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, badType(f, "date string")
	}
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return nil, badType(f, "date (YYYY-MM-DD)")
}

func toDateTime(f Field, raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, badType(f, "datetime string")
	}
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, badType(f, "datetime (RFC3339)")
	}
	return t, nil
}

func badType(f Field, want string) error {
	return httpx.Errorf(http.StatusBadRequest, "validation",
		fmt.Sprintf("%s must be a %s", f.Label, want))
}
