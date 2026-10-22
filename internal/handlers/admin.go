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
