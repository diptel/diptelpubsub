package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateApp inserts a new tenant/app with its own VAPID keypair and API key.
// webhookURL is optional (pass "" for none) — it's where incoming WhatsApp replies
// matched to this tenant get forwarded (e.g. an n8n webhook).
func CreateApp(ctx context.Context, pool *pgxpool.Pool, slug, name, apiKey, pub, priv, subject, webhookURL string) (*App, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO apps (slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		RETURNING id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url, created_at, updated_at
	`, slug, name, apiKey, pub, priv, subject, webhookURL)
	return scanApp(row)
}

// GetAppByAPIKey looks up a tenant by the API key its backend presents when calling /send*.
func GetAppByAPIKey(ctx context.Context, pool *pgxpool.Pool, apiKey string) (*App, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url, created_at, updated_at
		FROM apps WHERE api_key = $1
	`, apiKey)
	return scanApp(row)
}

// GetAppBySlug looks up a tenant by its slug, used for the public VAPID-public-key endpoint.
func GetAppBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*App, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url, created_at, updated_at
		FROM apps WHERE slug = $1
	`, slug)
	return scanApp(row)
}

// GetAppByID looks up a tenant by its primary key, e.g. when matching an incoming WA reply.
func GetAppByID(ctx context.Context, pool *pgxpool.Pool, id string) (*App, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url, created_at, updated_at
		FROM apps WHERE id = $1
	`, id)
	a, err := scanApp(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// UpdateAppWebhook sets or clears (webhookURL="") a tenant's incoming-WA forward URL.
func UpdateAppWebhook(ctx context.Context, pool *pgxpool.Pool, slug, webhookURL string) (*App, error) {
	row := pool.QueryRow(ctx, `
		UPDATE apps SET webhook_url = NULLIF($2, ''), updated_at = now() WHERE slug = $1
		RETURNING id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url, created_at, updated_at
	`, slug, webhookURL)
	a, err := scanApp(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func scanApp(row pgx.Row) (*App, error) {
	var a App
	err := row.Scan(&a.ID, &a.Slug, &a.Name, &a.APIKey, &a.VAPIDPublicKey, &a.VAPIDPrivateKey, &a.VAPIDSubject, &a.WebhookURL, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}
