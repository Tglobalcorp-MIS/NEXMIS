package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Roles, from most to least privileged.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleHelpdesk = "helpdesk"
	RoleViewer   = "viewer"
)

// RoleRank orders roles for permission checks.
func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 4
	case RoleOperator:
		return 3
	case RoleHelpdesk:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

type User struct {
	ID           uuid.UUID  `json:"id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name"`
	Email        string     `json:"email"`
	Source       string     `json:"source"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	PasswordHash string     `json:"-"`
	FailedLogins int        `json:"-"`
	LockedUntil  *time.Time `json:"-"`
}

const userCols = `id, username, display_name, email, source, role, disabled, last_login_at, created_at,
	password_hash, failed_logins, locked_until`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Source, &u.Role, &u.Disabled,
		&u.LastLoginAt, &u.CreatedAt, &u.PasswordHash, &u.FailedLogins, &u.LockedUntil)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username) = lower($1)`, username))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	err := s.Pool.QueryRow(ctx, `INSERT INTO users (username, display_name, email, source, password_hash, role, disabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`,
		u.Username, u.DisplayName, u.Email, u.Source, u.PasswordHash, u.Role, u.Disabled).Scan(&u.ID, &u.CreatedAt)
	return mapErr(err)
}

// UserPatch carries optional user updates.
type UserPatch struct {
	DisplayName  *string `json:"display_name"`
	Email        *string `json:"email"`
	Role         *string `json:"role"`
	Disabled     *bool   `json:"disabled"`
	Password     *string `json:"password"`
	PasswordHash *string `json:"-"`
}

func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, p UserPatch) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET
		display_name = COALESCE($2, display_name),
		email = COALESCE($3, email),
		role = COALESCE($4, role),
		disabled = COALESCE($5, disabled),
		password_hash = COALESCE($6, password_hash),
		updated_at = now()
		WHERE id = $1`, id, p.DisplayName, p.Email, p.Role, p.Disabled, p.PasswordHash)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if p.Disabled != nil && *p.Disabled {
		_, err = s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordLoginFailure increments the failure counter and locks the account
// for lockFor once maxFailures is reached.
func (s *Store) RecordLoginFailure(ctx context.Context, id uuid.UUID, maxFailures int, lockFor time.Duration) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET failed_logins = failed_logins + 1,
		locked_until = CASE WHEN failed_logins + 1 >= $2 THEN now() + make_interval(secs => $3) ELSE locked_until END
		WHERE id = $1`, id, maxFailures, lockFor.Seconds())
	return err
}

func (s *Store) RecordLoginSuccess(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = now() WHERE id = $1`, id)
	return err
}

// UpsertADUser creates or refreshes an AD-backed account after a successful
// LDAP bind. The role is re-derived from group membership on every login.
func (s *Store) UpsertADUser(ctx context.Context, username, displayName, email, role string) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `INSERT INTO users (username, display_name, email, source, role)
		VALUES ($1,$2,$3,'ad',$4)
		ON CONFLICT (lower(username)) DO UPDATE SET display_name = EXCLUDED.display_name,
			email = EXCLUDED.email, role = EXCLUDED.role, updated_at = now()
		WHERE users.source = 'ad'
		RETURNING `+userCols, username, displayName, email, role))
}

// ---- sessions ----

type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, idHash string, userID uuid.UUID, ttl time.Duration, ip, ua string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions (id, user_id, expires_at, ip, user_agent) VALUES ($1,$2,$3,$4,$5)`,
		idHash, userID, time.Now().Add(ttl), ip, ua)
	return err
}

// SessionUser resolves a session to its user, sliding the expiry forward.
func (s *Store) SessionUser(ctx context.Context, idHash string, ttl time.Duration) (*User, error) {
	u, err := scanUser(s.Pool.QueryRow(ctx, `UPDATE sessions SET last_seen_at = now(),
			expires_at = GREATEST(expires_at, now() + make_interval(secs => $2))
		FROM users u WHERE sessions.id = $1 AND sessions.expires_at > now() AND sessions.created_at > now() - interval '7 days' AND u.id = sessions.user_id AND NOT u.disabled
		RETURNING u.id, u.username, u.display_name, u.email, u.source, u.role, u.disabled, u.last_login_at, u.created_at,
			u.password_hash, u.failed_logins, u.locked_until`, idHash, ttl.Seconds()))
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, idHash)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID uuid.UUID, exceptHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, exceptHash)
	return err
}

// ADUsersWithSessions lists enabled AD-sourced users that hold at least one
// unexpired session, for periodic re-validation against the directory.
func (s *Store) ADUsersWithSessions(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users u WHERE source = 'ad' AND NOT disabled
		AND EXISTS (SELECT 1 FROM sessions WHERE user_id = u.id AND expires_at > now()) ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}
