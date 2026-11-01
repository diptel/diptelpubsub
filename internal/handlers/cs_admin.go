package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/middleware"
	"pushhub/internal/wa"
)

type csSessionItem struct {
	ID            string `json:"id"`
	Phone         string `json:"phone"`
	AppID         string `json:"app_id,omitempty"`
	AppName       string `json:"app_name,omitempty"`
	Status        string `json:"status"`
	OpenedAt      string `json:"opened_at"`
	LastMessageAt string `json:"last_message_at"`
	ClosedAt      string `json:"closed_at,omitempty"`
}

// ListCSSessions returns CS Hub sessions for the panel inbox, newest activity first.
// GET /admin/cs-sessions?status=open&app=<app_id>
func ListCSSessions(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		appID := r.URL.Query().Get("app")

		sessions, err := db.ListCSSessions(r.Context(), pool, status, appID, 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list CS sessions")
			return
		}

		// Resolve app names in one pass; a handful of tenants, not worth a JOIN query here.
		appNames := map[string]string{}
		out := make([]csSessionItem, 0, len(sessions))
		for _, s := range sessions {
			item := csSessionItem{
				ID: s.ID, Phone: s.Phone, Status: s.Status,
				OpenedAt:      s.OpenedAt.Format("2006-01-02 15:04"),
				LastMessageAt: s.LastMessageAt.Format("2006-01-02 15:04"),
			}
			if s.ClosedAt != nil {
				item.ClosedAt = s.ClosedAt.Format("2006-01-02 15:04")
			}
			if s.AppID != nil {
				item.AppID = *s.AppID
				name, ok := appNames[*s.AppID]
				if !ok {
					if app, err := db.GetAppByID(r.Context(), pool, *s.AppID); err == nil {
						name = app.Name
						appNames[*s.AppID] = name
					}
				}
				item.AppName = name
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type csMessageItem struct {
	Direction   string `json:"direction"`
	SenderLabel string `json:"sender_label,omitempty"`
	Message     string `json:"message"`
	CreatedAt   string `json:"created_at"`
}

// GetCSSessionMessages returns one session's full conversation for the panel's chat view.
// GET /admin/cs-sessions/{id}/messages
func GetCSSessionMessages(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		msgs, err := db.ListCSMessages(r.Context(), pool, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load messages")
			return
		}
		out := make([]csMessageItem, 0, len(msgs))
		for _, m := range msgs {
			item := csMessageItem{Direction: m.Direction, Message: m.Message, CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05")}
			if m.SenderLabel != nil {
				item.SenderLabel = *m.SenderLabel
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// ReplyCSSession sends a staff reply from the panel through the session's CS number, and
// records it in the conversation labeled with the admin username who sent it.
// POST /admin/cs-sessions/{id}/reply  { "message": "..." }
func ReplyCSSession(pool *pgxpool.Pool, limiter *wa.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		req.Message = strings.TrimSpace(req.Message)
		if req.Message == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}

		session, err := db.GetCSSession(r.Context(), pool, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown session")
			return
		}
		number, err := db.GetWANumberByID(r.Context(), pool, session.WANumberID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "CS number no longer exists")
			return
		}

		label := "admin"
		if u := middleware.AdminUserFromContext(r.Context()); u != nil {
			label = u.Username
		}

		sendFromHub(r.Context(), pool, limiter, number, session.Phone, req.Message, "cs")
		_ = db.AppendCSMessage(r.Context(), pool, session.ID, "out", &label, req.Message)
		_ = db.TouchSession(r.Context(), pool, session.ID)

		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
	}
}

// sessionClosedText is sent to the customer when a staff member marks their session done, so
// they know the conversation is finished rather than being left wondering whether anyone's still
// there — and know to send a new message (which reopens a fresh session) if they need more help.
const sessionClosedText = "Percakapan ini sudah ditandai selesai oleh tim kami. Kalau masih butuh bantuan, silakan kirim pesan baru ya."

// CloseCSSession marks a session done from the panel (staff finished helping the customer) and
// lets the customer know, so "selesai" isn't silent on their end — they'd otherwise have no way
// to tell whether staff just stopped replying or actually wrapped up the conversation.
// POST /admin/cs-sessions/{id}/close
func CloseCSSession(pool *pgxpool.Pool, limiter *wa.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		session, err := db.GetCSSession(r.Context(), pool, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown session")
			return
		}

		if err := db.CloseSession(r.Context(), pool, id); err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "unknown or already-closed session")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to close session")
			return
		}

		// Best-effort: the session is already closed at this point either way, so a notify
		// failure (e.g. the CS number got deleted) shouldn't turn into an error response — staff
		// already did their part by marking it done.
		if number, numErr := db.GetWANumberByID(r.Context(), pool, session.WANumberID); numErr == nil {
			sendFromHub(r.Context(), pool, limiter, number, session.Phone, sessionClosedText, "cs")
			_ = db.AppendCSMessage(r.Context(), pool, session.ID, "out", nil, sessionClosedText)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
	}
}
