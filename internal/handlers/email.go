package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/emailsend"
	"pushhub/internal/middleware"
)

type sendEmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	HTML    bool   `json:"html,omitempty"`
}

// SendEmail delivers one email through the hub's shared SMTP account. Authenticated via
// X-API-Key (tenant-scoped), same as the push and WhatsApp send endpoints.
// POST /send/email  { "to": "user@example.com", "subject": "...", "body": "..." }
func SendEmail(pool *pgxpool.Pool, cfg emailsend.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := middleware.AppFromContext(r.Context())
		if app == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}

		var req sendEmailRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		req.To = strings.TrimSpace(req.To)
		if req.To == "" {
			writeError(w, http.StatusBadRequest, "to is required")
			return
		}
		if req.Subject == "" {
			writeError(w, http.StatusBadRequest, "subject is required")
			return
		}

		// Brand the subject, same reasoning as WhatsApp: one shared "From" address sends for
		// every tenant, so the recipient needs to see which business this email is from.
		subject := req.Subject
		tag := "[" + app.Name + "]"
		if !strings.HasPrefix(strings.TrimSpace(subject), tag) {
			subject = tag + " " + subject
		}

		sendErr := emailsend.Send(cfg, req.To, subject, req.Body, req.HTML)

		status := "sent"
		if sendErr != nil {
			status = "failed"
		}
		if logErr := db.LogEmailMessage(r.Context(), pool, app.ID, req.To, subject, status, sendErr); logErr != nil {
			_ = logErr
		}

		if sendErr != nil {
			writeError(w, http.StatusBadGateway, "failed to send email: "+sendErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
	}
}

type emailMessageResponse struct {
	AppID     string `json:"app_id"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ListEmailMessages returns the most recent email sends across all tenants, for the admin panel.
// GET /admin/email-messages
func ListEmailMessages(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msgs, err := db.ListEmailMessages(r.Context(), pool, 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list email messages")
			return
		}
		out := make([]emailMessageResponse, 0, len(msgs))
		for _, m := range msgs {
			item := emailMessageResponse{
				AppID: m.AppID, To: m.ToAddress, Subject: m.Subject, Status: m.Status,
				CreatedAt: m.CreatedAt.Format("2006-01-02 15:04"),
			}
			if m.Error != nil {
				item.Error = *m.Error
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
