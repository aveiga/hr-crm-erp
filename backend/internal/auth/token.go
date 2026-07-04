package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// APITokenPrefix marks a bearer token as a long-lived per-agent API token
// (rather than a login JWT), so the middleware can route it to the right
// verifier just by looking at the prefix.
const APITokenPrefix = "hrcrm_pat_"

// NewAPIToken mints a random API token. It returns the plaintext (shown to the
// user exactly once) and the SHA-256 hash to persist; the plaintext is never
// stored.
func NewAPIToken() (plaintext, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	plaintext = APITokenPrefix + hex.EncodeToString(b)
	return plaintext, HashAPIToken(plaintext)
}

// HashAPIToken returns the hex SHA-256 of a token, used for storage and lookup.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// Claims is the JWT payload. It binds a user to one active tenant + role so the
// API stays stateless; switching tenants means minting a new token.
type Claims struct {
	jwt.RegisteredClaims
	UserID   string `json:"uid"`
	Email    string `json:"email"`
	TenantID string `json:"tid"`
	Role     string `json:"role"`
}

type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenIssuer(secret string, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), ttl: ttl}
}

func (ti *TokenIssuer) Issue(userID uuid.UUID, email string, tenantID uuid.UUID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ti.ttl)),
		},
		UserID:   userID.String(),
		Email:    email,
		TenantID: tenantID.String(),
		Role:     role,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(ti.secret)
}

func (ti *TokenIssuer) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return ti.secret, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// HashPassword and CheckPassword wrap bcrypt.
func HashPassword(pw string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), cost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// RandomToken returns a URL-safe random token for invites.
func RandomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
