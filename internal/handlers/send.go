package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/fcmassist"
	"pushhub/internal/middleware"
	"pushhub/internal/push"
)

// FCMClient sends Android push notifications through FCM HTTP v1 (internal/fcmassist). Set once
// from main.go, same pattern as handlers.AIClient — a single process, one client is enough since
// fcmassist.Client already caches a token per tenant's credentials file internally.
var FCMClient *fcmassist.Client

type sendRequest struct {
	UserID    *string         `json:"user_id,omitempty"` // target one user's devices; omit + broadcast=true to send to all
	Broadcast bool            `json:"broadcast,omitempty"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Icon      string          `json:"icon,omitempty"`
	URL       string          `json:"url,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`

	// Android/FCM channel override — see claude/riwariwi-android-fcm-kontrak-pushhub.md bagian 3.
	// When Channel is set, it always wins over the tenant's fcm_channel_rules (fcmassist.ResolveChannel
	// is skipped entirely). Priority is optional alongside it ("high"/"normal"); defaults to normal
	// (or to whatever the matched tenant rule says, when Channel is empty).
	Channel  string `json:"channel,omitempty"`
	Priority string `json:"priority,omitempty"`
}

type sendResultItem struct {
	SubscriptionID string `json:"subscription_id"`
	Channel        string `json:"channel,omitempty"` // "webpush" | "fcm"
	Status         string `json:"status"`            // sent | failed | expired
	Error          string `json:"error,omitempty"`
}

type sendResponse struct {
	Sent    int              `json:"sent"`
	Failed  int              `json:"failed"`
	Expired int              `json:"expired"`
	Results []sendResultItem `json:"results"`
}

// Send delivers a push notification to one user's subscriptions, or broadcasts to every
// subscription of the calling app. Authenticated via X-API-Key (tenant-scoped).
// POST /send  { "user_id": "123", "title": "...", "body": "..." }
// POST /send  { "broadcast": true, "title": "...", "body": "..." }
//
// Android (FCM) and Web Push are both tried, per user (or per subscription, for subs with no
// known user_id): if a user has at least one active FCM token, only FCM is sent to them — Web
// Push is skipped — to avoid double-notifying the same person on the same phone (the riwariwi
// contract's rule, generalized to every tenant).
func Send(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := middleware.AppFromContext(r.Context())
		if app == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}

		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		if req.UserID == nil && !req.Broadcast {
			writeError(w, http.StatusBadRequest, "set user_id to target a user, or broadcast=true to send to all")
			return
		}

		var subs []db.Subscription
		var tokens []db.FCMToken
		var err error
		if req.Broadcast {
			subs, err = db.ListSubscriptionsForApp(r.Context(), pool, app.ID)
			if err == nil {
				tokens, err = db.ListFCMTokensForApp(r.Context(), pool, app.ID)
			}
		} else {
			subs, err = db.ListSubscriptionsForUser(r.Context(), pool, app.ID, *req.UserID)
			if err == nil {
				tokens, err = db.ListFCMTokensForUser(r.Context(), pool, app.ID, *req.UserID)
			}
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load subscriptions")
			return
		}

		// Users with at least one active FCM token get FCM only — Web Push for that same user_id
		// is skipped so the same phone doesn't get the notification twice. A subscription with no
		// user_id (older/anonymous Web Push subscriptions) can't be matched to any FCM token, so
		// it always gets sent.
		fcmUsers := make(map[string]bool, len(tokens))
		for _, t := range tokens {
			if t.UserID != nil {
				fcmUsers[*t.UserID] = true
			}
		}
		webpushSubs := subs[:0]
		for _, s := range subs {
			if s.UserID != nil && fcmUsers[*s.UserID] {
				continue
			}
			webpushSubs = append(webpushSubs, s)
		}

		payloadForLog, _ := json.Marshal(push.Message{Title: req.Title, Body: req.Body, Icon: req.Icon, URL: req.URL, Data: req.Data})

		resp := sendResponse{}
		sendFCM(r, pool, app, req, tokens, &resp, payloadForLog)
		sendWebPush(r, pool, app, req, webpushSubs, &resp, payloadForLog)

		writeJSON(w, http.StatusOK, resp)
	}
}

func sendWebPush(r *http.Request, pool *pgxpool.Pool, app *db.App, req sendRequest, subs []db.Subscription, resp *sendResponse, payloadForLog []byte) {
	msg := push.Message{Title: req.Title, Body: req.Body, Icon: req.Icon, URL: req.URL, Data: req.Data}

	for _, sub := range subs {
		pushSub := push.Subscription{Endpoint: sub.Endpoint}
		pushSub.Keys.P256dh = sub.P256dh
		pushSub.Keys.Auth = sub.Auth

		result, sendErr := push.Send(pushSub, msg, app.VAPIDPublicKey, app.VAPIDPrivateKey, app.VAPIDSubject)

		item := sendResultItem{SubscriptionID: sub.ID, Channel: "webpush"}
		switch {
		case sendErr != nil && result != nil && result.Gone:
			item.Status = "expired"
			item.Error = sendErr.Error()
			resp.Expired++
			if delErr := db.DeleteSubscriptionByID(r.Context(), pool, sub.ID); delErr != nil {
				log.Printf("pushhub: failed to delete stale subscription %s: %v", sub.ID, delErr)
			}
		case sendErr != nil:
			item.Status = "failed"
			item.Error = sendErr.Error()
			resp.Failed++
		default:
			item.Status = "sent"
			resp.Sent++
		}
		resp.Results = append(resp.Results, item)

		subID := sub.ID
		if logErr := db.LogNotification(r.Context(), pool, app.ID, &subID, req.Title, req.Body, payloadForLog, item.Status, item.Error); logErr != nil {
			log.Printf("pushhub: failed to write notification log: %v", logErr)
		}
	}
}

// sendFCM delivers to every given Android token. Channel/priority come from the explicit
// req.Channel (always wins) or, failing that, the tenant's fcm_channel_rules (fcmassist.ResolveChannel)
// evaluated once per request — not once per token, since every token here belongs to the same
// notification. A tenant with no FCM credentials configured simply can't have any tokens (Subscribe
// rejects kind:"fcm" for them), so this never needs its own "FCM not configured" branch.
func sendFCM(r *http.Request, pool *pgxpool.Pool, app *db.App, req sendRequest, tokens []db.FCMToken, resp *sendResponse, payloadForLog []byte) {
	if len(tokens) == 0 || FCMClient == nil {
		return
	}
	if app.FCMCredentialsPath == nil || *app.FCMCredentialsPath == "" {
		// Shouldn't happen (tokens imply FCM was enabled when they were registered), but a tenant
		// could have FCM turned back off since. Fail loudly in the log rather than silently drop.
		log.Printf("pushhub: app %q has FCM tokens but no fcm_credentials_path configured; skipping FCM send", app.Slug)
		return
	}

	var channelID string
	var priority fcmassist.Priority
	if req.Channel != "" {
		channelID = req.Channel
		priority = fcmassist.PriorityNormal
		if req.Priority == "high" {
			priority = fcmassist.PriorityHigh
		}
	} else {
		channelID, priority = fcmassist.ResolveChannel(fcmassist.ParseChannelRules(app.FCMChannelRules), req.URL, req.Title)
	}

	for _, tok := range tokens {
		fcmResult, sendErr := FCMClient.Send(r.Context(), *app.FCMCredentialsPath, fcmassist.Message{
			Token: tok.Token, Title: req.Title, Body: req.Body, URL: req.URL,
			ChannelID: channelID, Priority: priority,
		})

		item := sendResultItem{SubscriptionID: tok.ID, Channel: "fcm"}
		switch {
		case sendErr != nil && fcmResult != nil && fcmResult.Unregistered:
			item.Status = "expired"
			item.Error = sendErr.Error()
			resp.Expired++
			if delErr := db.DeleteFCMTokenByID(r.Context(), pool, tok.ID); delErr != nil {
				log.Printf("pushhub: failed to delete stale FCM token %s: %v", tok.ID, delErr)
			}
		case sendErr != nil:
			item.Status = "failed"
			item.Error = sendErr.Error()
			resp.Failed++
			log.Printf("pushhub: FCM send to token %s failed: %v", tok.ID, sendErr)
		default:
			item.Status = "sent"
			resp.Sent++
		}
		resp.Results = append(resp.Results, item)

		if logErr := db.LogNotification(r.Context(), pool, app.ID, nil, req.Title, req.Body, payloadForLog, item.Status, item.Error); logErr != nil {
			log.Printf("pushhub: failed to write notification log: %v", logErr)
		}
	}
}
