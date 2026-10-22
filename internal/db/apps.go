package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateApp inserts a new tenant/app with its own VAPID keypair and API key.
func CreateApp(ctx context.Context, pool *pgxpool.Pool, slug, name, apiKey, pub, priv, subject string) (*App, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO apps (slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, created_at, updated_at
	`, slug, name, apiKey, pub, priv, subject)
	return scanApp(row)
}

// GetAppByAPIKey looks up a tenant by the API key its backend presents when calling /send.
func GetAppByAPIKey(ctx context.Context, pool *pgxpool.Pool, apiKey string) (*App, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, created_at, updated_at
		FROM apps WHERE api_key = $1
	`, apiKey)
	return scanApp(row)
}

// GetAppBySlug looks up a tenant by its slug, used for the public VAPID-public-key endpoint.
func GetAppBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*App, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, created_at, updated_at
		FROM apps WHERE slug = $1
	`, slug)
	return scanApp(row)
}

func scanApp(row pgx.Row) (*App, error) {
	var a App
	err := row.Scan(&a.ID, &a.Slug, &a.Name, &a.APIKey, &a.VAPIDPublicKey, &a.VAPIDPrivateKey, &a.VAPIDSubject, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}
