package azure

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Client secrets are sealed with AES-256-GCM under a key derived from the
// server's SECRETS_KEY and stored in azure_credentials (never returned).

func (s *Service) seal(plain string) ([]byte, error) {
	gcm, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *Service) open(sealed []byte) (string, error) {
	gcm, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("stored secret is corrupt")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored secret can't be decrypted (was SECRETS_KEY changed?) — set it again")
	}
	return string(plain), nil
}

func (s *Service) gcm() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(s.secretsKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SetSecret stores (replaces) an Azure tenant record's client secret.
func (s *Service) SetSecret(ctx context.Context, tenantID, azTenantID uuid.UUID, secret, expiresOn string) error {
	sealed, err := s.seal(secret)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO azure_credentials (tenant_id, azure_tenant_id, secret, expires_on, updated_at)
		VALUES ($1, $2, $3, NULLIF($4,'')::date, now())
		ON CONFLICT (tenant_id, azure_tenant_id) DO UPDATE SET secret=EXCLUDED.secret, expires_on=EXCLUDED.expires_on, updated_at=now()`,
		tenantID, azTenantID, sealed, expiresOn)
	return err
}

// CredentialInfo says whether a secret is stored (never the secret itself).
func (s *Service) CredentialInfo(ctx context.Context, tenantID, azTenantID uuid.UUID) (map[string]any, error) {
	var expires *time.Time
	var updated time.Time
	err := s.pool.QueryRow(ctx, `SELECT expires_on, updated_at FROM azure_credentials WHERE tenant_id=$1 AND azure_tenant_id=$2`,
		tenantID, azTenantID).Scan(&expires, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"has_secret": false}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"has_secret": true, "updated_at": updated.Format(time.RFC3339)}
	if expires != nil {
		out["expires_on"] = expires.Format("2006-01-02")
	}
	return out, nil
}

func (s *Service) secret(ctx context.Context, tenantID, azTenantID uuid.UUID) (string, error) {
	var sealed []byte
	err := s.pool.QueryRow(ctx, `SELECT secret FROM azure_credentials WHERE tenant_id=$1 AND azure_tenant_id=$2`,
		tenantID, azTenantID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("no client secret set for this Azure tenant")
	}
	if err != nil {
		return "", err
	}
	return s.open(sealed)
}

// --- small value helpers ----------------------------------------------------

func str(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// day returns the YYYY-MM-DD part of a stored date (RFC3339 string).
func day(v any) string {
	s := str(v)
	if len(s) >= 10 {
		return s[:10]
	}
	return ""
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func parseDay(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

func fmtDay(t time.Time) string { return t.Format("2006-01-02") }

func today() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
