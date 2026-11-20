package handlers

import (
	"encoding/json"
	"fmt"
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
	Subscription push.Subscription `json:"subscription"` // Web Push only; ignored when Kind is "fcm"

	// Android/FCM fields — see claude/riwariwi-android-fcm-kontrak-pushhub.md bagian 3 (BRIIX
	// project docs) for the contract this mirrors exactly. Kind "" (or "webpush") means the
	// request above is a normal browser Web Push subscription, unchanged from before FCM existed.
	Kind      string  `json:"kind,omitempty"` // "" | "webpush" | "fcm"
	FCMToken  string  `json:"fcm_token,omitempty"`
	DeviceID  *string `json:"device_id,omitempty"`
	ClientApp *string `json:"client_app,omitempty"` // "driver" | "mitra" | "app" | "kasir" | "admin" | omitted
}

// Subscribe stores a browser's Web Push subscription, or (kind:"fcm") an Android device's FCM
// token, for an app.
// POST /subscribe  { "app": "riwariwi", "user_id": "optional", "subscription": {...} }
// POST /subscribe  { "app": "riwariwi", "user_id": "...", "kind": "fcm", "fcm_token": "...", "device_id": "...", "client_app": "driver" }
func Subscribe(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.App == "" {
			writeError(w, http.StatusBadRequest, "app is required")
			return
		}

		app, err := db.GetAppBySlug(r.Context(), pool, req.App)
		if err != nil {
			writeError(w, http.StatusNotFound, "unknown app slug")
			return
		}

		if req.Kind == "fcm" {
			subscribeFCM(w, r, pool, app, req)
			return
		}

		if req.Subscription.Endpoint == "" {
			writeError(w, http.StatusBadRequest, "app and subscription.endpoint are required")
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

// subscribeFCM handles the kind:"fcm" branch of Subscribe — split out because it has its own
// required fields and its own precondition (the tenant must have FCM credentials configured;
// see db.UpdateAppFCM / POST /admin/apps/{slug}/fcm).
func subscribeFCM(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, app *db.App, req subscribeRequest) {
	if req.FCMToken == "" {
		writeError(w, http.StatusBadRequest, "fcm_token is required when kind is \"fcm\"")
		return
	}
	if app.FCMCredentialsPath == nil || *app.FCMCredentialsPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("tenant %q belum mengaktifkan kanal FCM (hubungi admin PushHub)", app.Slug))
		return
	}

	tok, err := db.UpsertFCMToken(r.Context(), pool, app.ID, req.UserID, req.FCMToken, req.DeviceID, req.ClientApp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store FCM token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": tok.ID, "status": "subscribed"})
}

type unsubscribeRequest struct {
	App      string `json:"app"`
	Endpoint string `json:"endpoint,omitempty"` // Web Push

	Kind     string `json:"kind,omitempty"`      // "" | "webpush" | "fcm"
	FCMToken string `json:"fcm_token,omitempty"` // required when Kind is "fcm"
}

// Unsubscribe removes a Web Push subscription or (kind:"fcm") an FCM token — e.g. when the user
// disables notifications or logs out.
// POST /unsubscribe  { "app": "riwariwi", "endpoint": "..." }
// POST /unsubscribe  { "app": "riwariwi", "kind": "fcm", "fcm_token": "..." }
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

		if req.Kind == "fcm" {
			if req.FCMToken == "" {
				writeError(w, http.StatusBadRequest, "fcm_token is required when kind is \"fcm\"")
				return
			}
			if err := db.DeleteFCMToken(r.Context(), pool, app.ID, req.FCMToken); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to remove FCM token")
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
			return
		}

		if err := db.DeleteSubscription(r.Context(), pool, app.ID, req.Endpoint); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove subscription")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
	}
}
