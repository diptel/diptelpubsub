package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fcmTokenColumns = `id, app_id, user_id, fcm_token, device_id, client_app, created_at, last_seen_at`

// UpsertFCMToken stores or refreshes an Android device's FCM token. Upserted by (app_id,
// fcm_token) rather than user_id — the same token gets re-registered every time the app opens,
// and user_id can change when a different account logs into the same phone (riwariwi contract,
// claude/riwariwi-android-fcm-kontrak-pushhub.md bagian 3).
func UpsertFCMToken(ctx context.Context, pool *pgxpool.Pool, appID string, userID *string, token string, deviceID, clientApp *string) (*FCMToken, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO fcm_tokens (app_id, user_id, fcm_token, device_id, client_app)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (app_id, fcm_token) DO UPDATE
			SET user_id = EXCLUDED.user_id,
			    device_id = EXCLUDED.device_id,
			    client_app = EXCLUDED.client_app,
			    last_seen_at = now()
		RETURNING `+fcmTokenColumns, appID, userID, token, deviceID, clientApp)
	return scanFCMToken(row)
}

// DeleteFCMToken removes one token, e.g. when the app disables notifications or the user logs
// out (POST /unsubscribe with kind "fcm").
func DeleteFCMToken(ctx context.Context, pool *pgxpool.Pool, appID, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM fcm_tokens WHERE app_id = $1 AND fcm_token = $2`, appID, token)
	return err
}

// DeleteFCMTokenByID removes a token by its own id — used after FCM itself reports the token
// gone (UNREGISTERED/INVALID_ARGUMENT), same pattern as DeleteSubscriptionByID for Web Push.
func DeleteFCMTokenByID(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM fcm_tokens WHERE id = $1`, id)
	return err
}

// ListFCMTokensForUser returns every FCM token for one app+external user id (a user may have
// several Android devices registered at once).
func ListFCMTokensForUser(ctx context.Context, pool *pgxpool.Pool, appID, userID string) ([]FCMToken, error) {
	rows, err := pool.Query(ctx, `SELECT `+fcmTokenColumns+` FROM fcm_tokens WHERE app_id = $1 AND user_id = $2`, appID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFCMTokens(rows)
}

// ListFCMTokensForApp returns every FCM token for an app — used for broadcast sends.
func ListFCMTokensForApp(ctx context.Context, pool *pgxpool.Pool, appID string) ([]FCMToken, error) {
	rows, err := pool.Query(ctx, `SELECT `+fcmTokenColumns+` FROM fcm_tokens WHERE app_id = $1`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFCMTokens(rows)
}

func scanFCMToken(row pgx.Row) (*FCMToken, error) {
	var t FCMToken
	err := row.Scan(&t.ID, &t.AppID, &t.UserID, &t.Token, &t.DeviceID, &t.ClientApp, &t.CreatedAt, &t.LastSeenAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func collectFCMTokens(rows pgx.Rows) ([]FCMToken, error) {
	var out []FCMToken
	for rows.Next() {
		var t FCMToken
		if err := rows.Scan(&t.ID, &t.AppID, &t.UserID, &t.Token, &t.DeviceID, &t.ClientApp, &t.CreatedAt, &t.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountFCMTokensByApp returns, per app, how many FCM (Android) tokens are currently registered —
// used by the admin panel to show "N perangkat Android" next to each tenant, same pattern as
// CountSubscriptionsByApp for Web Push.
func CountFCMTokensByApp(ctx context.Context, pool *pgxpool.Pool) (map[string]int, error) {
	rows, err := pool.Query(ctx, `SELECT app_id, count(*) FROM fcm_tokens GROUP BY app_id`)
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
