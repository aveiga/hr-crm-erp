// Command server is the HR/CRM/ERP API entrypoint.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hrcrm/internal/cache"
	"hrcrm/internal/config"
	"hrcrm/internal/db"
	"hrcrm/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.MigrateOnBoot {
		if err := db.Migrate(ctx, pool); err != nil {
			slog.Error("run migrations", "err", err)
			os.Exit(1)
		}
	}

	c, err := cache.New(cfg.RedisURL)
	if err != nil {
		slog.Error("connect redis", "err", err)
		os.Exit(1)
	}
	defer c.Close()

	srv, err := server.New(ctx, cfg, pool, c)
	if err != nil {
		slog.Error("build server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.RunBackground(ctx)

	go func() {
		slog.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}
