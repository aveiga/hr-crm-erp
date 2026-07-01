package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	FullName     string    `json:"full_name"`
	PasswordHash string    `json:"-"`
}

type Tenant struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type Membership struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Tenant   string    `json:"tenant_name"`
	Slug     string    `json:"tenant_slug"`
	Role     string    `json:"role"`
}

var ErrEmailTaken = errors.New("email already registered")

// CreateUserWithTenant performs the self-service signup: it creates the user,
// a new tenant, and an owner membership atomically.
func (s *Store) CreateUserWithTenant(ctx context.Context, email, fullName, pwHash, tenantName string) (User, Tenant, error) {
	email = normalizeEmail(email)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, Tenant{}, err
	}
	defer tx.Rollback(ctx)

	var u User
	u.Email = email
	u.FullName = fullName
	err = tx.QueryRow(ctx,
		`INSERT INTO users(email, password_hash, full_name) VALUES($1,$2,$3) RETURNING id`,
		email, pwHash, fullName).Scan(&u.ID)
	if err != nil {
		if isUnique(err) {
			return User{}, Tenant{}, ErrEmailTaken
		}
		return User{}, Tenant{}, err
	}

	var t Tenant
	t.Name = tenantName
	t.Slug = uniqueSlugCandidate(tenantName)
	err = tx.QueryRow(ctx,
		`INSERT INTO tenants(name, slug) VALUES($1, $2 || '-' || substr(md5(random()::text),1,6)) RETURNING id, slug`,
		tenantName, t.Slug).Scan(&t.ID, &t.Slug)
	if err != nil {
		return User{}, Tenant{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships(tenant_id, user_id, role) VALUES($1,$2,'owner')`,
		t.ID, u.ID); err != nil {
		return User{}, Tenant{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, Tenant{}, err
	}
	return u, t, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, full_name, password_hash FROM users WHERE email=$1`,
		normalizeEmail(email)).Scan(&u.ID, &u.Email, &u.FullName, &u.PasswordHash)
	return u, err
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, full_name, password_hash FROM users WHERE id=$1`,
		id).Scan(&u.ID, &u.Email, &u.FullName, &u.PasswordHash)
	return u, err
}

// Memberships lists the tenants a user belongs to.
func (s *Store) Memberships(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.tenant_id, t.name, t.slug, m.role
		 FROM memberships m JOIN tenants t ON t.id = m.tenant_id
		 WHERE m.user_id=$1 ORDER BY t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.TenantID, &m.Tenant, &m.Slug, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MembershipFor returns the role and allowed sections of a user in a tenant.
// allowedGroups is nil when the member has full access.
func (s *Store) MembershipFor(ctx context.Context, userID, tenantID uuid.UUID) (role string, allowedGroups []string, ok bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT role, allowed_groups FROM memberships WHERE user_id=$1 AND tenant_id=$2`,
		userID, tenantID).Scan(&role, &allowedGroups)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return role, allowedGroups, true, nil
}

// MemberPerm describes a member's role and section access for the matrix.
type MemberPerm struct {
	UserID        uuid.UUID `json:"user_id"`
	Email         string    `json:"email"`
	FullName      string    `json:"full_name"`
	Role          string    `json:"role"`
	AllowedGroups []string  `json:"allowed_groups"` // nil => all sections
}

func (s *Store) MembersWithPerms(ctx context.Context, tenantID uuid.UUID) ([]MemberPerm, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.email, u.full_name, m.role, m.allowed_groups
		 FROM memberships m JOIN users u ON u.id=m.user_id
		 WHERE m.tenant_id=$1 ORDER BY u.full_name, u.email`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberPerm{}
	for rows.Next() {
		var mp MemberPerm
		if err := rows.Scan(&mp.UserID, &mp.Email, &mp.FullName, &mp.Role, &mp.AllowedGroups); err != nil {
			return nil, err
		}
		out = append(out, mp)
	}
	return out, rows.Err()
}

// SetMemberGroups sets a member's allowed sections. groups == nil grants full
// access (stored as NULL); a non-nil (possibly empty) slice restricts access.
func (s *Store) SetMemberGroups(ctx context.Context, tenantID, userID uuid.UUID, groups []string) error {
	var arg any
	if groups != nil {
		arg = groups
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE memberships SET allowed_groups=$3 WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID, arg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("member not found")
	}
	return nil
}

type Invite struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	Accepted  bool      `json:"accepted"`
}

func (s *Store) CreateInvite(ctx context.Context, tenantID, invitedBy uuid.UUID, email, role string) (Invite, error) {
	email = normalizeEmail(email)
	inv := Invite{TenantID: tenantID, Email: email, Role: role, Token: RandomToken()}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO invites(tenant_id, email, role, token, invited_by)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (tenant_id, email) DO UPDATE SET role=EXCLUDED.role, token=EXCLUDED.token, accepted_at=NULL
		 RETURNING id`, tenantID, email, role, inv.Token, invitedBy).Scan(&inv.ID)
	return inv, err
}

func (s *Store) ListInvites(ctx context.Context, tenantID uuid.UUID) ([]Invite, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, email, role, token, accepted_at IS NOT NULL
		 FROM invites WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.TenantID, &i.Email, &i.Role, &i.Token, &i.Accepted); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// AcceptInvite consumes a token: creates the user if needed and adds membership.
func (s *Store) AcceptInvite(ctx context.Context, token, fullName, pwHash string) (User, Tenant, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, Tenant{}, "", err
	}
	defer tx.Rollback(ctx)

	var inv Invite
	err = tx.QueryRow(ctx,
		`SELECT id, tenant_id, email, role FROM invites WHERE token=$1 AND accepted_at IS NULL`,
		token).Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, Tenant{}, "", errors.New("invalid or already used invite")
	}
	if err != nil {
		return User{}, Tenant{}, "", err
	}

	var u User
	u.Email = inv.Email
	err = tx.QueryRow(ctx, `SELECT id, full_name FROM users WHERE email=$1`, inv.Email).Scan(&u.ID, &u.FullName)
	if errors.Is(err, pgx.ErrNoRows) {
		if pwHash == "" {
			return User{}, Tenant{}, "", errors.New("password required for new account")
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO users(email, password_hash, full_name) VALUES($1,$2,$3) RETURNING id`,
			inv.Email, pwHash, fullName).Scan(&u.ID)
		u.FullName = fullName
	}
	if err != nil {
		return User{}, Tenant{}, "", err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships(tenant_id, user_id, role) VALUES($1,$2,$3)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		inv.TenantID, u.ID, inv.Role); err != nil {
		return User{}, Tenant{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET accepted_at=now() WHERE id=$1`, inv.ID); err != nil {
		return User{}, Tenant{}, "", err
	}

	var t Tenant
	if err := tx.QueryRow(ctx, `SELECT id, name, slug FROM tenants WHERE id=$1`, inv.TenantID).Scan(&t.ID, &t.Name, &t.Slug); err != nil {
		return User{}, Tenant{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, Tenant{}, "", err
	}
	return u, t, inv.Role, nil
}

// Members lists users in a tenant (for the team admin page).
func (s *Store) Members(ctx context.Context, tenantID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.email, u.full_name, m.role
		 FROM memberships m JOIN users u ON u.id=m.user_id
		 WHERE m.tenant_id=$1 ORDER BY u.full_name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var email, name, role string
		if err := rows.Scan(&id, &email, &name, &role); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "email": email, "full_name": name, "role": role})
	}
	return out, rows.Err()
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func uniqueSlugCandidate(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	return s
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}
