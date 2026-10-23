package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateWANumber registers a hub-level WhatsApp sender number (Fonnte token + routing category).
func CreateWANumber(ctx context.Context, pool *pgxpool.Pool, label, phoneNumber, fonnteToken, category string, priority, minIntervalMs int) (*WANumber, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO wa_numbers (label, phone_number, fonnte_token, category, priority, min_interval_ms)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6)
		RETURNING id, label, phone_number, fonnte_token, category, priority, active, min_interval_ms, created_at, updated_at
	`, label, phoneNumber, fonnteToken, category, priority, minIntervalMs)
	return scanWANumber(row)
}

// ListWANumbers returns every registered WA number, for the admin panel.
func ListWANumbers(ctx context.Context, pool *pgxpool.Pool) ([]WANumber, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, label, phone_number, fonnte_token, category, priority, active, min_interval_ms, created_at, updated_at
		FROM wa_numbers ORDER BY category, priority, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WANumber
	for rows.Next() {
		n, err := scanWANumberRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// PickWANumber returns the best active number for a category: lowest priority value first.
// Falls back to any other active number if none match the category, so a send never hard-fails
// just because nobody configured a dedicated number for it yet.
func PickWANumber(ctx context.Context, pool *pgxpool.Pool, category string) (*WANumber, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, label, phone_number, fonnte_token, category, priority, active, min_interval_ms, created_at, updated_at
		FROM wa_numbers WHERE active = true AND category = $1
		ORDER BY priority ASC LIMIT 1
	`, category)
	n, err := scanWANumber(row)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	row = pool.QueryRow(ctx, `
		SELECT id, label, phone_number, fonnte_token, category, priority, active, min_interval_ms, created_at, updated_at
		FROM wa_numbers WHERE active = true
		ORDER BY priority ASC LIMIT 1
	`)
	n, err = scanWANumber(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// GetWANumberByID is used when matching an incoming webhook to its sending number.
func GetWANumberByID(ctx context.Context, pool *pgxpool.Pool, id string) (*WANumber, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, label, phone_number, fonnte_token, category, priority, active, min_interval_ms, created_at, updated_at
		FROM wa_numbers WHERE id = $1
	`, id)
	n, err := scanWANumber(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// SetWANumberActive toggles a number on/off without deleting its history.
func SetWANumberActive(ctx context.Context, pool *pgxpool.Pool, id string, active bool) error {
	tag, err := pool.Exec(ctx, `UPDATE wa_numbers SET active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteWANumber(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM wa_numbers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanWANumber(row pgx.Row) (*WANumber, error) {
	var n WANumber
	err := row.Scan(&n.ID, &n.Label, &n.PhoneNumber, &n.FonnteToken, &n.Category, &n.Priority, &n.Active, &n.MinIntervalMs, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func scanWANumberRows(rows pgx.Rows) (*WANumber, error) {
	var n WANumber
	err := rows.Scan(&n.ID, &n.Label, &n.PhoneNumber, &n.FonnteToken, &n.Category, &n.Priority, &n.Active, &n.MinIntervalMs, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// LogWAMessage records an outbound or inbound WhatsApp message. appID is "" when an incoming
// reply couldn't be matched to any tenant — it's still logged, just unattributed.
func LogWAMessage(ctx context.Context, pool *pgxpool.Pool, appID string, waNumberID *string, direction, phone, category, message, status string, sendErr error, providerResponse interface{}) error {
	var errText *string
	if sendErr != nil {
		s := sendErr.Error()
		errText = &s
	}
	var raw []byte
	if providerResponse != nil {
		raw, _ = json.Marshal(providerResponse)
	}
	var appIDParam *string
	if appID != "" {
		appIDParam = &appID
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO wa_messages (app_id, wa_number_id, direction, phone, category, message, status, error, provider_response)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, appIDParam, waNumberID, direction, phone, category, message, status, errText, raw)
	return err
}

// LastAppForIncomingWA finds which tenant last sent an outbound message to this phone number
// on this WA number, so an incoming reply can be routed to the right tenant's webhook.
func LastAppForIncomingWA(ctx context.Context, pool *pgxpool.Pool, waNumberID, phone string) (string, error) {
	var appID string
	err := pool.QueryRow(ctx, `
		SELECT app_id FROM wa_messages
		WHERE wa_number_id = $1 AND phone = $2 AND direction = 'out'
		ORDER BY created_at DESC LIMIT 1
	`, waNumberID, phone).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return appID, err
}

// ListWAMessages returns the most recent WA messages (across all tenants) for the admin panel log view.
func ListWAMessages(ctx context.Context, pool *pgxpool.Pool, limit int) ([]WAMessage, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, wa_number_id, direction, phone, category, message, status, error, created_at
		FROM wa_messages ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WAMessage
	for rows.Next() {
		var m WAMessage
		if err := rows.Scan(&m.ID, &m.AppID, &m.WANumberID, &m.Direction, &m.Phone, &m.Category, &m.Message, &m.Status, &m.Error, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LogEmailMessage records an outbound email attempt.
func LogEmailMessage(ctx context.Context, pool *pgxpool.Pool, appID, toAddress, subject, status string, sendErr error) error {
	var errText *string
	if sendErr != nil {
		s := sendErr.Error()
		errText = &s
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO email_messages (app_id, to_address, subject, status, error)
		VALUES ($1, $2, $3, $4, $5)
	`, appID, toAddress, subject, status, errText)
	return err
}

// ListEmailMessages returns the most recent email sends (across all tenants) for the admin panel log view.
func ListEmailMessages(ctx context.Context, pool *pgxpool.Pool, limit int) ([]EmailMessage, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, to_address, subject, status, error, created_at
		FROM email_messages ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EmailMessage
	for rows.Next() {
		var m EmailMessage
		if err := rows.Scan(&m.ID, &m.AppID, &m.ToAddress, &m.Subject, &m.Status, &m.Error, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
