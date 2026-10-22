package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/middleware"
	"pushhub/internal/push"
)

type sendRequest struct {
	UserID    *string         `json:"user_id,omitempty"` // target one user's devices; omit + broadcast=true to send to all
	Broadcast bool            `json:"broadcast,omitempty"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Icon      string          `json:"icon,omitempty"`
	URL       string          `json:"url,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type sendResultItem struct {
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"` // sent | failed | expired
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
		var err error
		if req.Broadcast {
			subs, err = db.ListSubscriptionsForApp(r.Context(), pool, app.ID)
		} else {
			subs, err = db.ListSubscriptionsForUser(r.Context(), pool, app.ID, *req.UserID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load subscriptions")
			return
		}

		msg := push.Message{Title: req.Title, Body: req.Body, Icon: req.Icon, URL: req.URL, Data: req.Data}
		payloadForLog, _ := json.Marshal(msg)

		resp := sendResponse{}
		for _, sub := range subs {
			pushSub := push.Subscription{Endpoint: sub.Endpoint}
			pushSub.Keys.P256dh = sub.P256dh
			pushSub.Keys.Auth = sub.Auth

			result, sendErr := push.Send(pushSub, msg, app.VAPIDPublicKey, app.VAPIDPrivateKey, app.VAPIDSubject)

			item := sendResultItem{SubscriptionID: sub.ID}
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
			logStatus := item.Status
			if logErr := db.LogNotification(r.Context(), pool, app.ID, &subID, req.Title, req.Body, payloadForLog, logStatus, item.Error); logErr != nil {
				log.Printf("pushhub: failed to write notification log: %v", logErr)
			}
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
