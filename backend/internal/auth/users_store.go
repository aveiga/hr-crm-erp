package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Errors returned by the user-manager store operations. Handlers map them to
// HTTP statuses (see memberError).
var (
	ErrMemberNotFound   = errors.New("member not found")
	ErrInviteNotFound   = errors.New("invite not found")
	ErrOwnerOnly        = errors.New("only an owner can modify an owner or grant the owner role")
	ErrLastOwner        = errors.New("the organization must keep at least one owner")
	ErrIdentityShared   = errors.New("this user also belongs to other organizations, so their email, name and password can only be changed by themselves")
	ErrRemoveSelf       = errors.New("you cannot remove yourself from the organization")
	ErrInviteNotPending = errors.New("only invites that have not been accepted can be changed")
	ErrResetInvalid     = errors.New("invalid, expired or already used reset link")
)

// TouchLastLogin stamps users.last_login_at.
func (s *Store) TouchLastLogin(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	return err
}

// IsOwnerEmail reports whether the email belongs to an owner of the tenant.
func (s *Store) IsOwnerEmail(ctx context.Context, tenantID uuid.UUID, email string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id
		 WHERE m.tenant_id=$1 AND u.email=$2 AND m.role='owner')`,
		tenantID, normalizeEmail(email)).Scan(&ok)
	return ok, err
}

// ensureOtherOwner returns ErrLastOwner unless the tenant has an owner other
// than userID. It locks the owner memberships so concurrent demotions can't
// both pass the check.
func ensureOtherOwner(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) error {
	rows, err := tx.Query(ctx,
		`SELECT user_id FROM memberships WHERE tenant_id=$1 AND role='owner' FOR UPDATE`, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	others := 0
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if id != userID {
			others++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if others == 0 {
		return ErrLastOwner
	}
	return nil
}

// --- members ---------------------------------------------------------------

// MemberDetail is a member row for the user manager.
type MemberDetail struct {
	UserID        uuid.UUID  `json:"user_id"`
	Email         string     `json:"email"`
	FullName      string     `json:"full_name"`
	Role          string     `json:"role"`
	AllowedGroups []string   `json:"allowed_groups"` // nil => all sections
	JoinedAt      time.Time  `json:"joined_at"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	// IdentityEditable is false when the user also belongs to another tenant,
	// in which case this tenant's admins may not change their email, name or
	// password.
	IdentityEditable bool `json:"identity_editable"`
}

const memberDetailSQL = `SELECT u.id, u.email, u.full_name, m.role, m.allowed_groups, m.created_at, u.last_login_at,
	NOT EXISTS (SELECT 1 FROM memberships o WHERE o.user_id=u.id AND o.tenant_id<>m.tenant_id)
	FROM memberships m JOIN users u ON u.id=m.user_id
	WHERE m.tenant_id=$1`

func scanMemberDetail(row pgx.Row) (MemberDetail, error) {
	var d MemberDetail
	err := row.Scan(&d.UserID, &d.Email, &d.FullName, &d.Role, &d.AllowedGroups, &d.JoinedAt,
		&d.LastLoginAt, &d.IdentityEditable)
	return d, err
}

// MemberDetails lists every member of a tenant.
func (s *Store) MemberDetails(ctx context.Context, tenantID uuid.UUID) ([]MemberDetail, error) {
	rows, err := s.pool.Query(ctx, memberDetailSQL+` ORDER BY u.full_name, u.email`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberDetail{}
	for rows.Next() {
		d, err := scanMemberDetail(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MemberDetailByID returns one member of a tenant, or ErrMemberNotFound.
func (s *Store) MemberDetailByID(ctx context.Context, tenantID, userID uuid.UUID) (MemberDetail, error) {
	d, err := scanMemberDetail(s.pool.QueryRow(ctx, memberDetailSQL+` AND m.user_id=$2`, tenantID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberDetail{}, ErrMemberNotFound
	}
	return d, err
}

// lockMember locks a membership row and returns its role and whether the user
// belongs to any other tenant.
func lockMember(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) (role string, shared bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT m.role, EXISTS (SELECT 1 FROM memberships o WHERE o.user_id=m.user_id AND o.tenant_id<>m.tenant_id)
		 FROM memberships m WHERE m.tenant_id=$1 AND m.user_id=$2 FOR UPDATE`,
		tenantID, userID).Scan(&role, &shared)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrMemberNotFound
	}
	return role, shared, err
}

// MemberUpdate is a partial update of a member. Nil pointers are unchanged;
// Groups applies only when SetGroups is true (nil Groups => full access).
type MemberUpdate struct {
	FullName  *string
	Email     *string
	Role      *string
	SetGroups bool
	Groups    []string
}

// UpdateMember applies a member update with the ownership, last-owner and
// cross-tenant identity guards, atomically.
func (s *Store) UpdateMember(ctx context.Context, tenantID, userID uuid.UUID, actorIsOwner bool, upd MemberUpdate) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	role, shared, err := lockMember(ctx, tx, tenantID, userID)
	if err != nil {
		return err
	}
	if role == "owner" && !actorIsOwner {
		return ErrOwnerOnly
	}

	// Global identity: only changed values count, so re-sending the current
	// name/email for a shared user is harmless.
	var curEmail, curName string
	if err := tx.QueryRow(ctx, `SELECT email, full_name FROM users WHERE id=$1`, userID).Scan(&curEmail, &curName); err != nil {
		return err
	}
	if upd.Email != nil && normalizeEmail(*upd.Email) == curEmail {
		upd.Email = nil
	}
	if upd.FullName != nil && *upd.FullName == curName {
		upd.FullName = nil
	}
	if upd.Email != nil || upd.FullName != nil {
		if shared {
			return ErrIdentityShared
		}
		email, name := curEmail, curName
		if upd.Email != nil {
			email = normalizeEmail(*upd.Email)
		}
		if upd.FullName != nil {
			name = *upd.FullName
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET email=$2, full_name=$3, updated_at=now() WHERE id=$1`,
			userID, email, name); err != nil {
			if isUnique(err) {
				return ErrEmailTaken
			}
			return err
		}
	}

	if upd.Role != nil && *upd.Role != role {
		if *upd.Role == "owner" && !actorIsOwner {
			return ErrOwnerOnly
		}
		if role == "owner" {
			if err := ensureOtherOwner(ctx, tx, tenantID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE memberships SET role=$3 WHERE tenant_id=$1 AND user_id=$2`,
			tenantID, userID, *upd.Role); err != nil {
			return err
		}
	}
	if upd.SetGroups {
		if _, err := tx.Exec(ctx,
			`UPDATE memberships SET allowed_groups=$3 WHERE tenant_id=$1 AND user_id=$2`,
			tenantID, userID, groupsArg(upd.Groups)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RemoveMember deletes a membership (the global user is kept) and revokes the
// member's API tokens and outstanding reset links in this tenant.
func (s *Store) RemoveMember(ctx context.Context, tenantID, actorID, userID uuid.UUID, actorIsOwner bool) error {
	if actorID == userID {
		return ErrRemoveSelf
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	role, _, err := lockMember(ctx, tx, tenantID, userID)
	if err != nil {
		return err
	}
	if role == "owner" {
		if !actorIsOwner {
			return ErrOwnerOnly
		}
		if err := ensureOtherOwner(ctx, tx, tenantID, userID); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`DELETE FROM memberships WHERE tenant_id=$1 AND user_id=$2`,
		`UPDATE api_tokens SET revoked_at=now() WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`,
		`DELETE FROM password_resets WHERE tenant_id=$1 AND user_id=$2 AND used_at IS NULL`,
	} {
		if _, err := tx.Exec(ctx, q, tenantID, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// --- password resets -------------------------------------------------------

// CreatePasswordReset stores the hash of a one-time reset token for a member,
// replacing any outstanding link for them in this tenant. It enforces the
// owner and cross-tenant identity guards.
func (s *Store) CreatePasswordReset(ctx context.Context, tenantID, actorID, userID uuid.UUID, actorIsOwner bool, tokenHash string, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	role, shared, err := lockMember(ctx, tx, tenantID, userID)
	if err != nil {
		return err
	}
	if role == "owner" && !actorIsOwner {
		return ErrOwnerOnly
	}
	if shared {
		return ErrIdentityShared
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM password_resets WHERE tenant_id=$1 AND user_id=$2 AND used_at IS NULL`,
		tenantID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO password_resets(tenant_id, user_id, token_hash, created_by, expires_at)
		 VALUES($1,$2,$3,$4,$5)`, tenantID, userID, tokenHash, actorID, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResetPassword redeems a reset token: it sets the user's password and marks
// the token used, once. The cross-tenant guard is re-checked at redemption, so
// a user who joined another tenant after the link was issued is protected.
func (s *Store) ResetPassword(ctx context.Context, tokenHash, pwHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var id, tenantID, userID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id, tenant_id, user_id FROM password_resets
		 WHERE token_hash=$1 AND used_at IS NULL AND expires_at > now() FOR UPDATE`,
		tokenHash).Scan(&id, &tenantID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResetInvalid
	}
	if err != nil {
		return err
	}
	_, shared, err := lockMember(ctx, tx, tenantID, userID)
	if errors.Is(err, ErrMemberNotFound) {
		return ErrResetInvalid
	}
	if err != nil {
		return err
	}
	if shared {
		return ErrIdentityShared
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, userID, pwHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE password_resets SET used_at=now() WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- invite administration -------------------------------------------------

// lockInvite locks one of a tenant's invites and checks it may be changed by
// the actor: not yet accepted, and owner invites only by owners.
func lockInvite(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, actorIsOwner bool) (Invite, error) {
	inv, err := scanInvite(tx.QueryRow(ctx,
		`SELECT `+inviteCols+` FROM invites WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrInviteNotFound
	}
	if err != nil {
		return Invite{}, err
	}
	if inv.Accepted {
		return Invite{}, ErrInviteNotPending
	}
	if inv.Role == "owner" && !actorIsOwner {
		return Invite{}, ErrOwnerOnly
	}
	return inv, nil
}

// inviteTx runs fn on a locked invite and returns the updated row.
func (s *Store) inviteTx(ctx context.Context, tenantID, id uuid.UUID, actorIsOwner bool, fn func(tx pgx.Tx, inv Invite) error) (Invite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invite{}, err
	}
	defer tx.Rollback(ctx)
	inv, err := lockInvite(ctx, tx, tenantID, id, actorIsOwner)
	if err != nil {
		return Invite{}, err
	}
	if err := fn(tx, inv); err != nil {
		return Invite{}, err
	}
	out, err := scanInvite(tx.QueryRow(ctx,
		`SELECT `+inviteCols+` FROM invites WHERE id=$1 AND tenant_id=$2`, id, tenantID))
	if err != nil {
		return Invite{}, err
	}
	return out, tx.Commit(ctx)
}

// UpdateInvite changes the role and section access of a pending invite.
func (s *Store) UpdateInvite(ctx context.Context, tenantID, id uuid.UUID, actorIsOwner bool, role string, groups []string) (Invite, error) {
	return s.inviteTx(ctx, tenantID, id, actorIsOwner, func(tx pgx.Tx, inv Invite) error {
		if inv.Status != InvitePending {
			return ErrInviteNotPending
		}
		if role == "owner" && !actorIsOwner {
			return ErrOwnerOnly
		}
		_, err := tx.Exec(ctx,
			`UPDATE invites SET role=$3, allowed_groups=$4 WHERE id=$1 AND tenant_id=$2`,
			id, tenantID, role, groupsArg(groups))
		return err
	})
}

// RevokeInvite revokes an invite that has not been accepted (idempotent).
func (s *Store) RevokeInvite(ctx context.Context, tenantID, id uuid.UUID, actorIsOwner bool) (Invite, error) {
	return s.inviteTx(ctx, tenantID, id, actorIsOwner, func(tx pgx.Tx, inv Invite) error {
		_, err := tx.Exec(ctx,
			`UPDATE invites SET revoked_at=COALESCE(revoked_at, now()) WHERE id=$1 AND tenant_id=$2`,
			id, tenantID)
		return err
	})
}

// ResendInvite issues a fresh token for an unaccepted invite, resets its expiry
// and clears any revocation. The old token stops working.
func (s *Store) ResendInvite(ctx context.Context, tenantID, id uuid.UUID, actorIsOwner bool, expiresAt time.Time) (Invite, error) {
	return s.inviteTx(ctx, tenantID, id, actorIsOwner, func(tx pgx.Tx, inv Invite) error {
		_, err := tx.Exec(ctx,
			`UPDATE invites SET token=$3, expires_at=$4, revoked_at=NULL WHERE id=$1 AND tenant_id=$2`,
			id, tenantID, RandomToken(), expiresAt)
		return err
	})
}
