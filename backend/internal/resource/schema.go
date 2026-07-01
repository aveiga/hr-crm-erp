package resource

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureSchema creates (or evolves) the table for every entity. Because we use
// CREATE TABLE IF NOT EXISTS plus ADD COLUMN IF NOT EXISTS, adding a new field
// to the catalog is picked up automatically on the next boot — no hand-written
// migration needed for entity changes.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool, reg *Registry) error {
	for _, e := range reg.All() {
		create := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`, e.Table())
		if _, err := pool.Exec(ctx, create); err != nil {
			return fmt.Errorf("create %s: %w", e.Table(), err)
		}

		for _, f := range e.Fields {
			alter := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s%s`,
				e.Table(), quoteIdent(f.Key), f.sqlType(), f.defaultClause())
			if _, err := pool.Exec(ctx, alter); err != nil {
				return fmt.Errorf("add column %s.%s: %w", e.Table(), f.Key, err)
			}
		}

		idx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(tenant_id)`,
			"idx_"+e.Key+"_tenant", e.Table())
		if _, err := pool.Exec(ctx, idx); err != nil {
			return fmt.Errorf("index %s: %w", e.Table(), err)
		}
	}
	return nil
}

// quoteIdent guards against unexpected identifiers. Field keys are developer
// controlled (from the catalog), but we double-quote defensively.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
