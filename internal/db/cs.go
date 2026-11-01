// CS Hub: one WhatsApp help conversation per (CS number, customer phone), shared by every
// tenant. See migrations/0004_cs_hub.sql and the "Rencana disepakati 1 Nov 2026" note in the
// project's status doc for the background.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionIdleTimeout is how long a CS session can sit with no new message before it's treated
// as closed (checked lazily — there's no cron job). "ketik MENU untuk ganti layanan" resets the
// clock just like any other message.
const SessionIdleTimeout = 24 * time.Hour

const csSessionColumns = `id, wa_number_id, phone, app_id, status, pending_menu, rating, opened_at, last_message_at, closed_at`

// GetActiveSession returns the customer's current non-closed session on this CS number, or
// ErrNotFound if there isn't one — including when the only session found has gone idle past
// SessionIdleTimeout, in which case it's closed here first (lazy auto-close, no cron needed).
func GetActiveSession(ctx context.Context, pool *pgxpool.Pool, waNumberID, phone string) (*CSSession, error) {
	row := pool.QueryRow(ctx, `
		SELECT `+csSessionColumns+` FROM cs_sessions
		WHERE wa_number_id = $1 AND phone = $2 AND status != 'closed'
		ORDER BY last_message_at DESC LIMIT 1
	`, waNumberID, phone)
	s, err := scanCSSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Since(s.LastMessageAt) > SessionIdleTimeout {
		_ = CloseSession(ctx, pool, s.ID)
		return nil, ErrNotFound
	}
	return s, nil
}

// CreateSession opens a new "awaiting_service" session showing this menu (app IDs, in display
// order) to the customer.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, waNumberID, phone string, pendingMenu []string) (*CSSession, error) {
	menuJSON, _ := json.Marshal(pendingMenu)
	row := pool.QueryRow(ctx, `
		INSERT INTO cs_sessions (wa_number_id, phone, status, pending_menu)
		VALUES ($1, $2, 'awaiting_service', $3)
		RETURNING `+csSessionColumns, waNumberID, phone, menuJSON)
	return scanCSSession(row)
}

// SetSessionApp connects an awaiting_service session to the tenant the customer picked (or that
// was auto-detected from a reference number), moving it to "open".
func SetSessionApp(ctx context.Context, pool *pgxpool.Pool, sessionID, appID string) (*CSSession, error) {
	row := pool.QueryRow(ctx, `
		UPDATE cs_sessions SET app_id = $2, status = 'open', pending_menu = NULL, last_message_at = now()
		WHERE id = $1
		RETURNING `+csSessionColumns, sessionID, appID)
	s, err := scanCSSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// ResetSessionToMenu sends an open session back to the service menu (customer typed "MENU").
func ResetSessionToMenu(ctx context.Context, pool *pgxpool.Pool, sessionID string, pendingMenu []string) (*CSSession, error) {
	menuJSON, _ := json.Marshal(pendingMenu)
	row := pool.QueryRow(ctx, `
		UPDATE cs_sessions SET app_id = NULL, status = 'awaiting_service', pending_menu = $2, last_message_at = now()
		WHERE id = $1
		RETURNING `+csSessionColumns, sessionID, menuJSON)
	s, err := scanCSSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// TouchSession bumps last_message_at without changing status — called for every inbound/outbound
// message on an already-open session so the idle timeout keeps counting from the latest message.
func TouchSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) error {
	_, err := pool.Exec(ctx, `UPDATE cs_sessions SET last_message_at = now() WHERE id = $1`, sessionID)
	return err
}

// CloseSession marks a session done — by staff action, idle timeout, or the customer leaving.
func CloseSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE cs_sessions SET status = 'closed', closed_at = now() WHERE id = $1 AND status != 'closed'
	`, sessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetCSSession looks up one session by ID, e.g. for the panel's conversation view or a staff reply.
func GetCSSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) (*CSSession, error) {
	row := pool.QueryRow(ctx, `SELECT `+csSessionColumns+` FROM cs_sessions WHERE id = $1`, sessionID)
	s, err := scanCSSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// ListCSSessions returns sessions for the panel inbox, most recently active first. status ("" for
// all, or "open"/"awaiting_service"/"closed") and appID ("" for all tenants) narrow the list —
// used so a staff member can see only the sessions for the service(s) they handle.
func ListCSSessions(ctx context.Context, pool *pgxpool.Pool, status, appID string, limit int) ([]CSSession, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+csSessionColumns+` FROM cs_sessions
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR app_id = $2::uuid)
		ORDER BY last_message_at DESC LIMIT $3
	`, status, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CSSession
	for rows.Next() {
		s, err := scanCSSessionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func scanCSSession(row pgx.Row) (*CSSession, error) {
	var s CSSession
	var menuJSON []byte
	err := row.Scan(&s.ID, &s.WANumberID, &s.Phone, &s.AppID, &s.Status, &menuJSON, &s.Rating, &s.OpenedAt, &s.LastMessageAt, &s.ClosedAt)
	if err != nil {
		return nil, err
	}
	if len(menuJSON) > 0 {
		_ = json.Unmarshal(menuJSON, &s.PendingMenu)
	}
	return &s, nil
}

func scanCSSessionRows(rows pgx.Rows) (*CSSession, error) {
	var s CSSession
	var menuJSON []byte
	err := rows.Scan(&s.ID, &s.WANumberID, &s.Phone, &s.AppID, &s.Status, &menuJSON, &s.Rating, &s.OpenedAt, &s.LastMessageAt, &s.ClosedAt)
	if err != nil {
		return nil, err
	}
	if len(menuJSON) > 0 {
		_ = json.Unmarshal(menuJSON, &s.PendingMenu)
	}
	return &s, nil
}

// AppendCSMessage records one message (bot, staff, or customer) in a session's conversation.
// senderLabel is nil for the bot/automated messages, or a staff username for a manual panel reply.
func AppendCSMessage(ctx context.Context, pool *pgxpool.Pool, sessionID, direction string, senderLabel *string, message string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO cs_messages (session_id, direction, sender_label, message)
		VALUES ($1, $2, $3, $4)
	`, sessionID, direction, senderLabel, message)
	return err
}

// ListCSMessages returns a session's full conversation, oldest first, for the panel's chat view.
func ListCSMessages(ctx context.Context, pool *pgxpool.Pool, sessionID string) ([]CSMessage, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, session_id, direction, sender_label, message, created_at
		FROM cs_messages WHERE session_id = $1 ORDER BY created_at
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CSMessage
	for rows.Next() {
		var m CSMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Direction, &m.SenderLabel, &m.Message, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Notification-number auto-reply throttle (wa_otp / wa_utama — "nomor ini khusus
// notifikasi"). Separate from the CS Hub above: these numbers are never meant to carry a
// conversation, just to stop re-sending the same canned reply on every "ok"/"makasih". ---

// ShouldSendAutoReply reports whether enough time (minGap) has passed since the last auto-reply
// to this phone on this number, and — if so — immediately records "sent now" so a burst of
// concurrent incoming messages can't both pass the check (acts like a claim, not just a read).
func ShouldSendAutoReply(ctx context.Context, pool *pgxpool.Pool, waNumberID, phone string, minGap time.Duration) (bool, error) {
	tag, err := pool.Exec(ctx, `
		INSERT INTO wa_auto_reply_log (wa_number_id, phone, last_sent_at) VALUES ($1, $2, now())
		ON CONFLICT (wa_number_id, phone) DO UPDATE
			SET last_sent_at = now()
			WHERE wa_auto_reply_log.last_sent_at < now() - $3::interval
	`, waNumberID, phone, minGap.String())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
