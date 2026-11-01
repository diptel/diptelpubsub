package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const appColumns = `id, slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url,
	cs_enabled, cs_menu_label, cs_ref_prefix, created_at, updated_at`

// CreateApp inserts a new tenant/app with its own VAPID keypair and API key.
// webhookURL is optional (pass "" for none) — it's where incoming WhatsApp replies
// matched to this tenant get forwarded (e.g. an n8n webhook).
func CreateApp(ctx context.Context, pool *pgxpool.Pool, slug, name, apiKey, pub, priv, subject, webhookURL string) (*App, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO apps (slug, name, api_key, vapid_public_key, vapid_private_key, vapid_subject, webhook_url)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		RETURNING `+appColumns, slug, name, apiKey, pub, priv, subject, webhookURL)
	return scanApp(row)
}

// GetAppByAPIKey looks up a tenant by the API key its backend presents when calling /send*.
func GetAppByAPIKey(ctx context.Context, pool *pgxpool.Pool, apiKey string) (*App, error) {
	row := pool.QueryRow(ctx, `SELECT `+appColumns+` FROM apps WHERE api_key = $1`, apiKey)
	return scanApp(row)
}

// GetAppBySlug looks up a tenant by its slug, used for the public VAPID-public-key endpoint.
func GetAppBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*App, error) {
	row := pool.QueryRow(ctx, `SELECT `+appColumns+` FROM apps WHERE slug = $1`, slug)
	return scanApp(row)
}

// GetAppByID looks up a tenant by its primary key, e.g. when matching an incoming WA reply.
func GetAppByID(ctx context.Context, pool *pgxpool.Pool, id string) (*App, error) {
	row := pool.QueryRow(ctx, `SELECT `+appColumns+` FROM apps WHERE id = $1`, id)
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
		RETURNING `+appColumns, slug, webhookURL)
	a, err := scanApp(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// UpdateAppCS sets a tenant's CS Hub menu settings: whether it shows up in the bot's service
// menu, its display label there (falls back to Name if empty), and the transaction-reference
// prefix (e.g. "KLR-") that lets the bot auto-pick this tenant without asking.
func UpdateAppCS(ctx context.Context, pool *pgxpool.Pool, slug string, enabled bool, menuLabel, refPrefix string) (*App, error) {
	row := pool.QueryRow(ctx, `
		UPDATE apps SET cs_enabled = $2, cs_menu_label = NULLIF($3, ''), cs_ref_prefix = NULLIF($4, ''), updated_at = now()
		WHERE slug = $1
		RETURNING `+appColumns, slug, enabled, menuLabel, refPrefix)
	a, err := scanApp(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// ListCSEnabledApps returns every tenant that should appear in the CS bot's service menu,
// ordered by name — used both to render the menu and to detect a reference-prefix match.
func ListCSEnabledApps(ctx context.Context, pool *pgxpool.Pool) ([]App, error) {
	rows, err := pool.Query(ctx, `SELECT `+appColumns+` FROM apps WHERE cs_enabled = true ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		a, err := scanAppRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func scanApp(row pgx.Row) (*App, error) {
	var a App
	err := row.Scan(&a.ID, &a.Slug, &a.Name, &a.APIKey, &a.VAPIDPublicKey, &a.VAPIDPrivateKey, &a.VAPIDSubject, &a.WebhookURL,
		&a.CSEnabled, &a.CSMenuLabel, &a.CSRefPrefix, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func scanAppRows(rows pgx.Rows) (*App, error) {
	var a App
	err := rows.Scan(&a.ID, &a.Slug, &a.Name, &a.APIKey, &a.VAPIDPublicKey, &a.VAPIDPrivateKey, &a.VAPIDSubject, &a.WebhookURL,
		&a.CSEnabled, &a.CSMenuLabel, &a.CSRefPrefix, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}
