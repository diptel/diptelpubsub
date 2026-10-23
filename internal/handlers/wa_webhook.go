package handlers

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
)

// fonnteIncoming covers the field names Fonnte's incoming-message webhook commonly sends.
// Fonnte's payload shape isn't strictly documented/stable, so this is intentionally loose:
// unrecognized fields are ignored rather than rejected.
type fonnteIncoming struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
	Text    string `json:"text"` // alternate field name seen in some Fonnte payload variants
}

// FonnteWebhook receives incoming WhatsApp replies for one hub-managed number and forwards
// them to whichever tenant last sent an outbound message to that phone number — so a reply to
// riwariwi's OTP, say, doesn't get forwarded to briixbroadband's n8n flow. Unmatched replies are
// still logged (for visibility) but not forwarded anywhere.
// POST /webhook/fonnte/{number_id}  (called by Fonnte itself, not authenticated — number_id in
// the path is effectively the secret, since it's a random UUID Fonnte must be configured with)
func FonnteWebhook(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		numberID := r.PathValue("number_id")
		if _, err := db.GetWANumberByID(r.Context(), pool, numberID); err != nil {
			// Still 200 so Fonnte doesn't retry forever on a misconfigured/deleted number.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored: unknown number"})
			return
		}

		in, ok := parseFonnteIncoming(r)
		if !ok || in.Sender == "" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored: no sender/message"})
			return
		}
		message := in.Message
		if message == "" {
			message = in.Text
		}

		appID, matchErr := db.LastAppForIncomingWA(r.Context(), pool, numberID, in.Sender)
		status := "received"
		if matchErr != nil {
			status = "unmatched"
		}

		if logErr := db.LogWAMessage(r.Context(), pool, appID, &numberID, "in", in.Sender, "", message, status, nil, nil); logErr != nil {
			log.Printf("pushhub: failed to log incoming WA message: %v", logErr)
		}

		if appID != "" {
			app, err := db.GetAppByID(r.Context(), pool, appID)
			if err == nil && app.WebhookURL != nil && *app.WebhookURL != "" {
				forwardToTenant(*app.WebhookURL, app.Slug, in.Sender, message)
			}
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func parseFonnteIncoming(r *http.Request) (fonnteIncoming, bool) {
	var in fonnteIncoming
	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return in, false
		}
		return in, true
	}

	if err := r.ParseForm(); err != nil {
		return in, false
	}
	in.Sender = r.FormValue("sender")
	in.Message = r.FormValue("message")
	in.Text = r.FormValue("text")
	return in, true
}

// forwardToTenant relays an incoming WhatsApp message to the tenant's configured webhook
// (e.g. an n8n webhook) so their own CS/auto-reply flow can handle it. Fire-and-forget with a
// short timeout — a slow/down tenant webhook must never block the Fonnte webhook response.
func forwardToTenant(webhookURL, appSlug, phone, message string) {
	payload, _ := json.Marshal(map[string]string{
		"app":     appSlug,
		"phone":   phone,
		"message": message,
	})
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			log.Printf("pushhub: failed to forward WA reply to %s webhook: %v", appSlug, err)
			return
		}
		resp.Body.Close()
	}()
}
