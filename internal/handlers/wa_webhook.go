package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/wa"
)

// notifAutoReplyGap is how often the "nomor ini khusus notifikasi" canned reply is allowed to
// repeat for the same phone number on the same notification number. Short enough that a customer
// who messages again hours later still gets pointed to the CS number; long enough that a string
// of "ok"/"makasih" replies doesn't get answered every single time.
const notifAutoReplyGap = 6 * time.Hour

// fonnteIncoming covers the field names Fonnte's incoming-message webhook commonly sends.
// Fonnte's payload shape isn't strictly documented/stable, so this is intentionally loose:
// unrecognized fields are ignored rather than rejected.
type fonnteIncoming struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
	Text    string `json:"text"` // alternate field name seen in some Fonnte payload variants
}

// FonnteWebhook receives incoming WhatsApp replies for one hub-managed number.
//   - category "otp"/"general" (pure notification numbers): never forwarded to any tenant.
//     Instead, at most once every notifAutoReplyGap, the sender gets a canned reply pointing
//     them at the CS number.
//   - category "cs" (the shared help-conversation number): runs the CS Hub bot — service menu,
//     session routing, and handing typed messages into the open session for the panel inbox.
//   - any other/legacy number (no "cs" number configured yet): falls back to the old behaviour —
//     forward to whichever tenant last sent an outbound message to this phone number.
//
// POST /webhook/fonnte/{number_id}  (called by Fonnte itself, not authenticated — number_id in
// the path is effectively the secret, since it's a random UUID Fonnte must be configured with)
func FonnteWebhook(pool *pgxpool.Pool, limiter *wa.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		numberID := r.PathValue("number_id")
		number, err := db.GetWANumberByID(r.Context(), pool, numberID)
		if err != nil {
			// Still 200 so Fonnte doesn't retry forever on a misconfigured/deleted number.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored: unknown number"})
			return
		}

		in, ok := parseFonnteIncoming(r)
		if !ok || in.Sender == "" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored: no sender/message"})
			return
		}
		message := strings.TrimSpace(in.Message)
		if message == "" {
			message = strings.TrimSpace(in.Text)
		}

		switch number.Category {
		case "otp", "general":
			handleNotificationReply(r.Context(), pool, limiter, number, in.Sender, message)
		case "cs":
			handleCSReply(r.Context(), pool, limiter, number, in.Sender, message)
		default:
			handleLegacyReply(r.Context(), pool, number, in.Sender, message)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleNotificationReply logs the reply (unattributed — notification numbers aren't tied to one
// tenant conversation). For "general", it also points the sender at the CS number, at most once
// per notifAutoReplyGap per phone. For "otp" the reply is fully ignored — no canned auto-reply at
// all, by Fi's explicit request (1 Nov 2026): the OTP number should never message back on its own.
func handleNotificationReply(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, phone, message string) {
	if logErr := db.LogWAMessage(ctx, pool, "", strPtr(number.ID), "in", phone, number.Category, message, "received", nil, nil); logErr != nil {
		log.Printf("pushhub: failed to log incoming WA message: %v", logErr)
	}

	if number.Category == "otp" {
		return
	}

	shouldReply, err := db.ShouldSendAutoReply(ctx, pool, number.ID, phone, notifAutoReplyGap)
	if err != nil {
		log.Printf("pushhub: auto-reply throttle check failed: %v", err)
		return
	}
	if !shouldReply {
		return
	}

	reply := notificationAutoReplyText(ctx, pool)
	sendFromHub(ctx, pool, limiter, number, phone, reply, number.Category)
}

func notificationAutoReplyText(ctx context.Context, pool *pgxpool.Pool) string {
	csNumber, err := db.PickWANumber(ctx, pool, "cs")
	if err == nil && csNumber.PhoneNumber != nil && *csNumber.PhoneNumber != "" {
		return fmt.Sprintf("Nomor ini khusus notifikasi (mohon tidak dibalas). Butuh bantuan? Hubungi: wa.me/%s", strings.TrimPrefix(*csNumber.PhoneNumber, "+"))
	}
	return "Nomor ini khusus notifikasi, mohon tidak dibalas. Untuk bantuan, hubungi tim CS kami."
}

// handleLegacyReply is the pre-CS-Hub behaviour, kept so a number nobody has re-categorized yet
// still works the old way: forward to whichever tenant last messaged this phone number.
func handleLegacyReply(ctx context.Context, pool *pgxpool.Pool, number *db.WANumber, phone, message string) {
	appID, matchErr := db.LastAppForIncomingWA(ctx, pool, number.ID, phone)
	status := "received"
	if matchErr != nil {
		status = "unmatched"
	}
	if logErr := db.LogWAMessage(ctx, pool, appID, strPtr(number.ID), "in", phone, "", message, status, nil, nil); logErr != nil {
		log.Printf("pushhub: failed to log incoming WA message: %v", logErr)
	}
	if appID != "" {
		app, err := db.GetAppByID(ctx, pool, appID)
		if err == nil && app.WebhookURL != nil && *app.WebhookURL != "" {
			forwardToTenant(*app.WebhookURL, app.Slug, phone, message)
		}
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
// (e.g. an n8n webhook) so their own flow can handle it. Fire-and-forget with a short timeout —
// a slow/down tenant webhook must never block the Fonnte webhook response.
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

// sendFromHub sends one message out through a given hub number (rate-limited same as a normal
// tenant send) and logs it — used for the hub's own automated replies (notification auto-reply,
// CS bot menu/confirmations), which aren't attributed to any one tenant.
func sendFromHub(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, phone, message, logCategory string) {
	if err := limiter.Wait(ctx, number.ID, time.Duration(number.MinIntervalMs)*time.Millisecond); err != nil {
		log.Printf("pushhub: rate-limit wait cancelled for hub send: %v", err)
		return
	}
	_, sendErr := wa.SendMessage(ctx, number.FonnteToken, phone, message)
	status := "sent"
	if sendErr != nil {
		status = "failed"
		log.Printf("pushhub: hub auto-send to %s failed: %v", phone, sendErr)
	}
	if logErr := db.LogWAMessage(ctx, pool, "", strPtr(number.ID), "out", phone, logCategory, message, status, sendErr, nil); logErr != nil {
		log.Printf("pushhub: failed to log hub auto-send: %v", logErr)
	}
}

func strPtr(s string) *string { return &s }

// atoiOrZero parses a short numeric menu choice like "1" or " 2 "; returns 0 (invalid) on anything
// else, including multi-word messages — a real question isn't mistaken for a menu pick.
func atoiOrZero(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 3 {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0
	}
	return n
}
