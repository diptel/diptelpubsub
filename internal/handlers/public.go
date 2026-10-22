package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/push"
)

// These endpoints are called directly from the browser (frontend), so they identify the
// tenant by its public slug, never by the secret API key (that stays server-side only).

// VAPIDPublicKey returns the public key a frontend needs to call PushManager.subscribe().
// GET /vapid-public-key?app=<slug>
func VAPIDPublicKey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.URL.Query().Get("app")
		if slug == "" {
			writeError(w, http.StatusBadRequest, "missing ?app=<slug> query param")
			return
		}
		app, err := db.GetAppBySlug(r.Context(), pool, slug)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown app slug")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"vapid_public_key": app.VAPIDPublicKey})
	}
}

type subscribeRequest struct {
	App          string            `json:"app"` // tenant slug
	UserID       *string           `json:"user_id,omitempty"`
	Subscription push.Subscription `json:"subscription"`
}

// Subscribe stores a browser's push subscription for an app.
// POST /subscribe  { "app": "riwariwi", "user_id": "optional", "subscription": {...} }
func Subscribe(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.App == "" || req.Subscription.Endpoint == "" {
			writeError(w, http.StatusBadRequest, "app and subscription.endpoint are required")
			return
		}

		app, err := db.GetAppBySlug(r.Context(), pool, req.App)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown app slug")
			return
		}

		var ua *string
		if h := r.Header.Get("User-Agent"); h != "" {
			ua = &h
		}

		sub, err := db.UpsertSubscription(r.Context(), pool, app.ID, req.UserID,
			req.Subscription.Endpoint, req.Subscription.Keys.P256dh, req.Subscription.Keys.Auth, ua)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store subscription")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"id": sub.ID, "status": "subscribed"})
	}
}

type unsubscribeRequest struct {
	App      string `json:"app"`
	Endpoint string `json:"endpoint"`
}

// Unsubscribe removes a subscription, e.g. when the user disables notifications or logs out.
// POST /unsubscribe  { "app": "riwariwi", "endpoint": "..." }
func Unsubscribe(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req unsubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		app, err := db.GetAppBySlug(r.Context(), pool, req.App)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown app slug")
			return
		}
		if err := db.DeleteSubscription(r.Context(), pool, app.ID, req.Endpoint); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove subscription")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
	}
}
