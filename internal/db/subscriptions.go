package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UpsertSubscription stores or refreshes a browser's push subscription for an app.
// Re-subscribing with the same endpoint updates keys/user_id instead of erroring.
func UpsertSubscription(ctx context.Context, pool *pgxpool.Pool, appID string, userID *string, endpoint, p256dh, auth string, userAgent *string) (*Subscription, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO subscriptions (app_id, user_id, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (app_id, endpoint) DO UPDATE
			SET user_id = EXCLUDED.user_id,
			    p256dh = EXCLUDED.p256dh,
			    auth = EXCLUDED.auth,
			    user_agent = EXCLUDED.user_agent,
			    last_seen_at = now()
		RETURNING id, app_id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_seen_at
	`, appID, userID, endpoint, p256dh, auth, userAgent)
	return scanSubscription(row)
}

// DeleteSubscription removes a push subscription, e.g. when a user logs out or disables notifications.
func DeleteSubscription(ctx context.Context, pool *pgxpool.Pool, appID, endpoint string) error {
	_, err := pool.Exec(ctx, `DELETE FROM subscriptions WHERE app_id = $1 AND endpoint = $2`, appID, endpoint)
	return err
}

// DeleteSubscriptionByID removes a subscription by its own id, used after a push service reports
// the endpoint is gone (HTTP 404/410 from the push provider).
func DeleteSubscriptionByID(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	return err
}

// ListSubscriptionsForUser returns all subscriptions for a given app+external user id (a user may
// have several devices/browsers subscribed at once).
func ListSubscriptionsForUser(ctx context.Context, pool *pgxpool.Pool, appID, userID string) ([]Subscription, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_seen_at
		FROM subscriptions WHERE app_id = $1 AND user_id = $2
	`, appID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSubscriptions(rows)
}

// ListSubscriptionsForApp returns every subscription for an app — used for broadcast sends.
func ListSubscriptionsForApp(ctx context.Context, pool *pgxpool.Pool, appID string) ([]Subscription, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_seen_at
		FROM subscriptions WHERE app_id = $1
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSubscriptions(rows)
}

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var s Subscription
	err := row.Scan(&s.ID, &s.AppID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func collectSubscriptions(rows pgx.Rows) ([]Subscription, error) {
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.AppID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
