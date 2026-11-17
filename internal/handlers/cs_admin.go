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
	ID             string `json:"id"`
	Phone          string `json:"phone"`
	AppID          string `json:"app_id,omitempty"`
	AppName        string `json:"app_name,omitempty"`
	Status         string `json:"status"`
	OpenedAt       string `json:"opened_at"`
	LastMessageAt  string `json:"last_message_at"`
	ClosedAt       string `json:"closed_at,omitempty"`
	HandledBy      string `json:"handled_by"`                 // "ai" | "staff" — lihat internal/handlers/cs_ai.go
	HandledByLabel string `json:"handled_by_label,omitempty"` // username staf yang klik "Tangani"
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
				ID: s.ID, Phone: s.Phone, Status: s.Status, HandledBy: s.HandledBy,
				OpenedAt:      s.OpenedAt.Format("2006-01-02 15:04"),
				LastMessageAt: s.LastMessageAt.Format("2006-01-02 15:04"),
			}
			if s.HandledByLabel != nil {
				item.HandledByLabel = *s.HandledByLabel
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

// handoverText is what AiSyah tells the customer the moment a staff member clicks "Tangani" —
// so the handover is visible on their end too, not just a silent change in who answers next.
const handoverText = "Baik, saya panggilkan tim CS kami untuk membantu lebih lanjut ya. Mohon tunggu sebentar \U0001F64F"

// TakeOverCSSession lets a staff member take over an AI-handled ("AiSyah") session from the
// panel. The AI assistant stops auto-replying in this session immediately (continueOpenSession
// in cs_bot.go checks handled_by before calling it) — the rest of the conversation, including
// everything AiSyah already said, stays in the same thread for staff to continue from. Only an
// "open" session can be taken over; db.TakeOverSession returns ErrNotFound otherwise (e.g. the
// customer hasn't picked a service yet, or the session is already closed).
// POST /admin/cs-sessions/{id}/handle
func TakeOverCSSession(pool *pgxpool.Pool, limiter *wa.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		label := "admin"
		if u := middleware.AdminUserFromContext(r.Context()); u != nil {
			label = u.Username
		}

		session, err := db.TakeOverSession(r.Context(), pool, id, label)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "sesi tidak ditemukan atau belum bisa diambil alih (belum pilih layanan / sudah selesai)")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to take over session")
			return
		}

		// Best-effort notify, same reasoning as CloseCSSession below: staff already took over
		// either way, a WA send failure shouldn't turn into an error response.
		if number, numErr := db.GetWANumberByID(r.Context(), pool, session.WANumberID); numErr == nil {
			sendFromHub(r.Context(), pool, limiter, number, session.Phone, handoverText, "cs")
			aiLabel := AIAssistantName
			_ = db.AppendCSMessage(r.Context(), pool, session.ID, "out", &aiLabel, handoverText)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "handled", "handled_by": session.HandledBy})
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

// aiPersonaMaxLen caps the staff-editable "Instruksi AI" text so a mistakenly pasted huge document
// doesn't balloon every OpenAI request (and its cost) — plenty of room for real house-style notes.
const aiPersonaMaxLen = 4000

// GetAISettings returns AiSyah's current config for the panel's "Instruksi AI" tab: whether the
// assistant can answer at all right now (API key configured + not kill-switched off) and the
// staff-editable persona text appended to every system prompt (see cs_ai.go's AIPersona/aiSystemPrompt).
// GET /admin/ai-settings
func GetAISettings(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		persona, err := db.GetSetting(r.Context(), pool, "ai_persona")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load AI settings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"assistant_name": AIAssistantName,
			"active":         aiActive(),
			"persona":        persona,
		})
	}
}

// UpdateAISettings saves a new persona instruction — a free-text addendum to AiSyah's system
// prompt (tone, house rules, things to always/never mention) — so staff can tune how the
// assistant talks without asking for a code change. Takes effect immediately: saved to
// hub_settings AND pushed into the in-memory cache cs_ai.go reads from, no restart needed.
// POST /admin/ai-settings
func UpdateAISettings(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Persona string `json:"persona"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		persona := strings.TrimSpace(body.Persona)
		if len(persona) > aiPersonaMaxLen {
			writeError(w, http.StatusBadRequest, "instruksi terlalu panjang (maks 4000 karakter)")
			return
		}
		if err := db.SetSetting(r.Context(), pool, "ai_persona", persona); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save AI settings")
			return
		}
		SetAIPersonaCache(persona)
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}
