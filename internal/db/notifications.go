package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LogNotification records the outcome of one push attempt for debugging and auditing.
func LogNotification(ctx context.Context, pool *pgxpool.Pool, appID string, subscriptionID *string, title, body string, payload []byte, status, errMsg string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO notifications_log (app_id, subscription_id, title, body, payload, status, error)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
	`, appID, subscriptionID, title, body, payload, status, errMsg)
	return err
}

// NotificationLogEntry is one row of notifications_log, for the admin panel's "Log Push" tab.
type NotificationLogEntry struct {
	ID             string
	AppID          string
	SubscriptionID *string // set for Web Push sends, NULL for FCM sends — see Channel below
	Title          *string
	Body           *string
	Status         string // sent | failed | expired
	Error          *string
	CreatedAt      time.Time
}

// Channel reports which push channel this entry went through. send.go has exactly two call
// sites for LogNotification: sendWebPush always passes a subscription id, sendFCM always passes
// nil, so SubscriptionID's nullness alone tells them apart without needing its own DB column.
func (e NotificationLogEntry) Channel() string {
	if e.SubscriptionID == nil {
		return "fcm"
	}
	return "webpush"
}

// ListNotificationLog returns the most recent push sends (Web Push + FCM) across all tenants,
// newest first, for the admin panel.
func ListNotificationLog(ctx context.Context, pool *pgxpool.Pool, limit int) ([]NotificationLogEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, subscription_id, title, body, status, error, created_at
		FROM notifications_log ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NotificationLogEntry
	for rows.Next() {
		var e NotificationLogEntry
		if err := rows.Scan(&e.ID, &e.AppID, &e.SubscriptionID, &e.Title, &e.Body, &e.Status, &e.Error, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
