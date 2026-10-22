package db

import (
	"context"

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
