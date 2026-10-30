package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

//go:embed datamigrations/*.sql
var dataMigrationFS embed.FS

// Migrate applies every embedded migration file exactly once, in filename
// order, recording applied versions in schema_migrations. It is safe to run on
// every boot. This is a deliberately tiny runner so we avoid a migration-tool
// dependency.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return apply(ctx, pool, migrationFS, "migrations", "")
}

// MigrateData applies data migrations (datamigrations/*.sql) exactly once
// each. Unlike Migrate it runs AFTER the metadata engine's EnsureSchema, so a
// data backfill can rely on entity columns that a catalog change just added.
// Versions are recorded in schema_migrations prefixed with "data/".
func MigrateData(ctx context.Context, pool *pgxpool.Pool) error {
	return apply(ctx, pool, dataMigrationFS, "datamigrations", "data/")
}

func apply(ctx context.Context, pool *pgxpool.Pool, fsys embed.FS, dir, prefix string) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		version := prefix + name
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := fsys.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
