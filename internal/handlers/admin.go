package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/push"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$`)

type createAppRequest struct {
	Slug         string `json:"slug"`          // e.g. "riwariwi", "briixbroadband", "briix-capital", "kasir"
	Name         string `json:"name"`          // display name
	VAPIDSubject string `json:"vapid_subject"` // e.g. "mailto:fika@briix.co.id"
}

type createAppResponse struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	APIKey         string `json:"api_key"`
	VAPIDPublicKey string `json:"vapid_public_key"`
}

// CreateApp registers a new tenant: generates its own VAPID keypair and API key.
// POST /admin/apps  (requires admin bearer token)
func CreateApp(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAppRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
		if !slugRe.MatchString(req.Slug) {
			writeError(w, http.StatusBadRequest, "slug must be lowercase letters/numbers/hyphens, 3-50 chars")
			return
		}
		if req.Name == "" {
			req.Name = req.Slug
		}
		if req.VAPIDSubject == "" {
			writeError(w, http.StatusBadRequest, "vapid_subject is required, e.g. mailto:you@example.com")
			return
		}

		pub, priv, err := push.GenerateVAPIDKeys()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate VAPID keys")
			return
		}

		apiKey, err := randomAPIKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate API key")
			return
		}

		app, err := db.CreateApp(r.Context(), pool, req.Slug, req.Name, apiKey, pub, priv, req.VAPIDSubject)
		if err != nil {
			writeError(w, http.StatusConflict, "app with this slug may already exist: "+err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, createAppResponse{
			Slug:           app.Slug,
			Name:           app.Name,
			APIKey:         app.APIKey,
			VAPIDPublicKey: app.VAPIDPublicKey,
		})
	}
}

func randomAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ph_" + hex.EncodeToString(b), nil
}

type appListItem struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	APIKey          string `json:"api_key"`
	VAPIDPublicKey  string `json:"vapid_public_key"`
	VAPIDSubject    string `json:"vapid_subject"`
	SubscriberCount int    `json:"subscriber_count"`
	CreatedAt       string `json:"created_at"`
}

// ListApps returns every registered tenant for the panel dashboard, including each app's
// API key — this endpoint is only reachable with a real admin session or the admin token,
// so returning the key again (rather than showing it once at creation) is an acceptable
// trade-off for a usable admin UI.
// GET /admin/apps
func ListApps(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apps, err := db.ListApps(r.Context(), pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list apps")
			return
		}
		counts, err := db.CountSubscriptionsByApp(r.Context(), pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count subscriptions")
			return
		}

		out := make([]appListItem, 0, len(apps))
		for _, a := range apps {
			out = append(out, appListItem{
				Slug:            a.Slug,
				Name:            a.Name,
				APIKey:          a.APIKey,
				VAPIDPublicKey:  a.VAPIDPublicKey,
				VAPIDSubject:    a.VAPIDSubject,
				SubscriberCount: counts[a.ID],
				CreatedAt:       a.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// RegenerateAppKey issues a new API key for an app, e.g. if the old one leaked. The app's
// VAPID keys and existing subscriptions are untouched.
// POST /admin/apps/{slug}/regenerate-key
func RegenerateAppKey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if slug == "" {
			writeError(w, http.StatusBadRequest, "missing slug")
			return
		}
		newKey, err := randomAPIKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate API key")
			return
		}
		app, err := db.RegenerateAppAPIKey(r.Context(), pool, slug, newKey)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "unknown app slug")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to regenerate key")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"slug": app.Slug, "api_key": app.APIKey})
	}
}
