package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// CountAdminUsers is used at startup to decide whether to bootstrap the first admin account.
func CountAdminUsers(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&n)
	return n, err
}

// CreateAdminUser inserts the bootstrap admin (or any admin added later). TOTP starts disabled;
// it's enrolled on first login.
func CreateAdminUser(ctx context.Context, pool *pgxpool.Pool, username, passwordHash string) (*AdminUser, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id, username, password_hash, totp_secret, totp_enabled, created_at, updated_at
	`, username, passwordHash)
	return scanAdminUser(row)
}

func GetAdminUserByUsername(ctx context.Context, pool *pgxpool.Pool, username string) (*AdminUser, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, username, password_hash, totp_secret, totp_enabled, created_at, updated_at
		FROM admin_users WHERE username = $1
	`, username)
	u, err := scanAdminUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func GetAdminUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (*AdminUser, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, username, password_hash, totp_secret, totp_enabled, created_at, updated_at
		FROM admin_users WHERE id = $1
	`, id)
	u, err := scanAdminUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// SetAdminTOTPSecret stores a freshly generated (not-yet-confirmed) TOTP secret for enrollment.
func SetAdminTOTPSecret(ctx context.Context, pool *pgxpool.Pool, adminUserID, secret string) error {
	_, err := pool.Exec(ctx, `
		UPDATE admin_users SET totp_secret = $2, updated_at = now() WHERE id = $1
	`, adminUserID, secret)
	return err
}

// EnableAdminTOTP marks TOTP as confirmed/active after the user verifies one code.
func EnableAdminTOTP(ctx context.Context, pool *pgxpool.Pool, adminUserID string) error {
	_, err := pool.Exec(ctx, `
		UPDATE admin_users SET totp_enabled = true, updated_at = now() WHERE id = $1
	`, adminUserID)
	return err
}

func scanAdminUser(row pgx.Row) (*AdminUser, error) {
	var u AdminUser
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateSession stores a new session token. pendingTOTP=true means the session can only be
// used to complete TOTP enrollment, not to access the panel/API.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, token, adminUserID string, pendingTOTP bool, ttl time.Duration) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO admin_sessions (token, admin_user_id, pending_totp, expires_at)
		VALUES ($1, $2, $3, now() + make_interval(secs => $4))
	`, token, adminUserID, pendingTOTP, ttl.Seconds())
	return err
}

func GetSession(ctx context.Context, pool *pgxpool.Pool, token string) (*AdminSession, error) {
	row := pool.QueryRow(ctx, `
		SELECT token, admin_user_id, pending_totp, expires_at, created_at
		FROM admin_sessions WHERE token = $1 AND expires_at > now()
	`, token)
	var s AdminSession
	err := row.Scan(&s.Token, &s.AdminUserID, &s.PendingTOTP, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// PromoteSession turns a pending-TOTP session into a fully active one after enrollment is confirmed.
func PromoteSession(ctx context.Context, pool *pgxpool.Pool, token string, ttl time.Duration) error {
	_, err := pool.Exec(ctx, `
		UPDATE admin_sessions SET pending_totp = false, expires_at = now() + make_interval(secs => $2) WHERE token = $1
	`, token, ttl.Seconds())
	return err
}

func DeleteSession(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token = $1`, token)
	return err
}

// ListApps returns every tenant app for the admin panel's dashboard.
func ListApps(ctx context.Context, pool *pgxpool.Pool) ([]App, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, created_at, updated_at
		FROM apps ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ID, &a.Slug, &a.Name, &a.APIKey, &a.VAPIDPublicKey, &a.VAPIDPrivateKey, &a.VAPIDSubject, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RegenerateAppAPIKey replaces an app's API key, e.g. if the old one leaked.
func RegenerateAppAPIKey(ctx context.Context, pool *pgxpool.Pool, slug, newAPIKey string) (*App, error) {
	row := pool.QueryRow(ctx, `
		UPDATE apps SET api_key = $2, updated_at = now() WHERE slug = $1
		RETURNING id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, created_at, updated_at
	`, slug, newAPIKey)
	a, err := scanApp(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// CountSubscriptionsByApp returns how many subscriptions each app has, keyed by app id —
// used to show a subscriber count in the panel dashboard without an N+1 query.
func CountSubscriptionsByApp(ctx context.Context, pool *pgxpool.Pool) (map[string]int, error) {
	rows, err := pool.Query(ctx, `SELECT app_id, count(*) FROM subscriptions GROUP BY app_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var appID string
		var n int
		if err := rows.Scan(&appID, &n); err != nil {
			return nil, err
		}
		out[appID] = n
	}
	return out, rows.Err()
}
