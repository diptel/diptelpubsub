package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
)

type createWANumberRequest struct {
	Label         string `json:"label"`
	PhoneNumber   string `json:"phone_number,omitempty"`
	FonnteToken   string `json:"fonnte_token"`
	Category      string `json:"category"`                  // "otp" | "general"
	Priority      int    `json:"priority,omitempty"`        // lower = tried first; default 100
	MinIntervalMs int    `json:"min_interval_ms,omitempty"` // default 2000
}

type waNumberResponse struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	PhoneNumber   string `json:"phone_number,omitempty"`
	Category      string `json:"category"`
	Priority      int    `json:"priority"`
	Active        bool   `json:"active"`
	MinIntervalMs int    `json:"min_interval_ms"`
	CreatedAt     string `json:"created_at"`
	// FonnteToken is intentionally omitted from list/read responses — the panel never needs to
	// display it again after creation, so it isn't leaked to anyone who can view the dashboard.
}

// ListWANumbers returns every registered WA sender number for the admin panel.
// GET /admin/wa-numbers
func ListWANumbers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		numbers, err := db.ListWANumbers(r.Context(), pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list WA numbers")
			return
		}
		out := make([]waNumberResponse, 0, len(numbers))
		for _, n := range numbers {
			item := waNumberResponse{
				ID: n.ID, Label: n.Label, Category: n.Category, Priority: n.Priority,
				Active: n.Active, MinIntervalMs: n.MinIntervalMs, CreatedAt: n.CreatedAt.Format("2006-01-02 15:04"),
			}
			if n.PhoneNumber != nil {
				item.PhoneNumber = *n.PhoneNumber
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateWANumber registers a new hub-level Fonnte-connected WA sender number.
// POST /admin/wa-numbers  { "label": "...", "fonnte_token": "...", "category": "otp" }
func CreateWANumber(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createWANumberRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Label == "" || req.FonnteToken == "" {
			writeError(w, http.StatusBadRequest, "label and fonnte_token are required")
			return
		}
		if req.Category != "otp" && req.Category != "general" {
			req.Category = "general"
		}
		if req.Priority == 0 {
			req.Priority = 100
		}
		if req.MinIntervalMs == 0 {
			req.MinIntervalMs = 2000
		}

		n, err := db.CreateWANumber(r.Context(), pool, req.Label, req.PhoneNumber, req.FonnteToken, req.Category, req.Priority, req.MinIntervalMs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create WA number: "+err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": n.ID})
	}
}

type toggleWANumberRequest struct {
	Active bool `json:"active"`
}

// ToggleWANumber enables/disables a number without losing its history — e.g. to pull a
// number out of rotation after it gets flagged, without deleting its send/receive log.
// POST /admin/wa-numbers/{id}/toggle  { "active": false }
func ToggleWANumber(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req toggleWANumberRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := db.SetWANumberActive(r.Context(), pool, id, req.Active); err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "unknown WA number")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to update WA number")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"active": req.Active})
	}
}

// DeleteWANumber removes a WA sender number entirely.
// DELETE /admin/wa-numbers/{id}
func DeleteWANumber(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := db.DeleteWANumber(r.Context(), pool, id); err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "unknown WA number")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to delete WA number")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type waMessageResponse struct {
	AppID     string `json:"app_id"`
	Direction string `json:"direction"`
	Phone     string `json:"phone"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ListWAMessages returns the most recent WhatsApp sends/receives across all tenants, for the
// admin panel's log view.
// GET /admin/wa-messages
func ListWAMessages(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msgs, err := db.ListWAMessages(r.Context(), pool, 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list WA messages")
			return
		}
		out := make([]waMessageResponse, 0, len(msgs))
		for _, m := range msgs {
			item := waMessageResponse{
				Direction: m.Direction, Phone: m.Phone, Category: m.Category,
				Message: m.Message, Status: m.Status, CreatedAt: m.CreatedAt.Format("2006-01-02 15:04"),
			}
			if m.AppID != nil {
				item.AppID = *m.AppID
			}
			if m.Error != nil {
				item.Error = *m.Error
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// UpdateAppWebhook sets or clears the tenant's incoming-WhatsApp forward URL (e.g. an n8n webhook).
// POST /admin/apps/{slug}/webhook  { "webhook_url": "https://..." }
func UpdateAppWebhook(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		var req struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		app, err := db.UpdateAppWebhook(r.Context(), pool, slug, req.WebhookURL)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "unknown app slug")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to update webhook")
			return
		}
		resp := map[string]string{"slug": app.Slug}
		if app.WebhookURL != nil {
			resp["webhook_url"] = *app.WebhookURL
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
