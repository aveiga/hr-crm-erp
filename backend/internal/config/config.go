// Package config loads runtime configuration from environment variables.
// All settings have sensible defaults for local/devcontainer use so the app
// boots with zero configuration; production overrides everything via env.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env           string        // "development" | "production"
	HTTPAddr      string        // e.g. ":8080"
	DatabaseURL   string        // postgres connection string
	RedisURL      string        // redis://host:port/db ("" disables caching)
	JWTSecret     string        // signing key for access tokens
	JWTTTL        time.Duration // access token lifetime
	SofficeBin    string        // path to libreoffice/soffice binary
	TemplatesDir  string        // directory holding the .docx templates
	CORSOrigin    string        // allowed browser origin for the SPA
	BcryptCost    int           // password hashing cost
	MigrateOnBoot bool          // run migrations automatically at startup
	// Secrets stored in the database (Azure client secrets) are encrypted
	// with this key. Defaults to JWT_SECRET; changing it orphans stored secrets.
	SecretsKey string
	// Azure Cost Management sync. AzureSyncEvery = 0 disables the daily job
	// (manual "Sync now" still works). The URLs are overridable for tests and
	// sovereign clouds.
	AzureSyncEvery     time.Duration
	AzureLoginURL      string
	AzureManagementURL string
}

func Load() Config {
	c := Config{
		Env:           getenv("APP_ENV", "development"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:   getenv("DATABASE_URL", "postgres://hrcrm:hrcrm@localhost:5432/hrcrm?sslmode=disable"),
		RedisURL:      getenv("REDIS_URL", "redis://localhost:6379/0"),
		JWTSecret:     getenv("JWT_SECRET", "dev-insecure-change-me"),
		JWTTTL:        getdur("JWT_TTL", 24*time.Hour),
		SofficeBin:    getenv("SOFFICE_BIN", "soffice"),
		TemplatesDir:  getenv("TEMPLATES_DIR", ""), // empty => use templates embedded in the binary
		CORSOrigin:    getenv("CORS_ORIGIN", "http://localhost:3000"),
		BcryptCost:    getint("BCRYPT_COST", 12),
		MigrateOnBoot: getbool("MIGRATE_ON_BOOT", true),

		AzureSyncEvery:     getdur("AZURE_SYNC_EVERY", 24*time.Hour),
		AzureLoginURL:      getenv("AZURE_LOGIN_URL", "https://login.microsoftonline.com"),
		AzureManagementURL: getenv("AZURE_MANAGEMENT_URL", "https://management.azure.com"),
	}
	c.SecretsKey = getenv("SECRETS_KEY", c.JWTSecret)
	return c
}

// Validate refuses to start in production with insecure defaults.
func (c Config) Validate() error {
	if c.Env == "production" && c.JWTSecret == "dev-insecure-change-me" {
		return fmt.Errorf("JWT_SECRET must be set in production")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	return nil
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func getint(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getbool(k string, def bool) bool {
	if v, ok := os.LookupEnv(k); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getdur(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
