package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
)

type pushMessageResponse struct {
	AppID     string `json:"app_id"`
	Channel   string `json:"channel"` // "webpush" | "fcm"
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ListPushMessages returns the most recent push sends (Web Push + FCM/Android) across all
// tenants, for the admin panel's "Log Push" tab — the push equivalent of ListWAMessages /
// ListEmailMessages.
// GET /admin/push-messages
func ListPushMessages(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := db.ListNotificationLog(r.Context(), pool, 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list push messages")
			return
		}
		out := make([]pushMessageResponse, 0, len(entries))
		for _, e := range entries {
			item := pushMessageResponse{
				AppID:     e.AppID,
				Channel:   e.Channel(),
				Status:    e.Status,
				CreatedAt: e.CreatedAt.Format("2006-01-02 15:04"),
			}
			if e.Title != nil {
				item.Title = *e.Title
			}
			if e.Body != nil {
				item.Body = *e.Body
			}
			if e.Error != nil {
				item.Error = *e.Error
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
