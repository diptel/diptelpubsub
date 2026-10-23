package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/middleware"
	"pushhub/internal/wa"
)

type sendWARequest struct {
	Phone    string `json:"phone"` // e.g. "6281234567890" or "+6281234567890"
	Message  string `json:"message"`
	Category string `json:"category,omitempty"` // "otp" | "general" (default "general"); "otp" routes to the OTP-dedicated number
}

type sendWAResponse struct {
	Status   string `json:"status"` // sent | failed
	WANumber string `json:"wa_number,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SendWA sends one WhatsApp message through the hub's shared Fonnte numbers, picking the
// number by category (OTP numbers stay reserved for OTP/transactional traffic) and rate-limited
// per number so a burst of sends across tenants can't get a shared number flagged by WhatsApp.
// Authenticated via X-API-Key (tenant-scoped), same as the push /send endpoint.
// POST /send/wa  { "phone": "6281...", "message": "...", "category": "otp" }
func SendWA(pool *pgxpool.Pool, limiter *wa.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := middleware.AppFromContext(r.Context())
		if app == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}

		var req sendWARequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		req.Phone = strings.TrimSpace(req.Phone)
		if req.Phone == "" {
			writeError(w, http.StatusBadRequest, "phone is required")
			return
		}
		if req.Message == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}
		if req.Category == "" {
			req.Category = "general"
		}

		number, err := db.PickWANumber(r.Context(), pool, req.Category)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no active WhatsApp number configured for this category")
			return
		}

		message := brandPrefix(app.Name, req.Message)

		if err := limiter.Wait(r.Context(), number.ID, time.Duration(number.MinIntervalMs)*time.Millisecond); err != nil {
			writeError(w, http.StatusRequestTimeout, "rate-limit wait cancelled")
			return
		}

		fonnteResp, sendErr := wa.SendMessage(r.Context(), number.FonnteToken, req.Phone, message)

		resp := sendWAResponse{WANumber: number.Label}
		status := "sent"
		if sendErr != nil {
			status = "failed"
			resp.Status = "failed"
			resp.Error = sendErr.Error()
		} else {
			resp.Status = "sent"
		}

		numberID := number.ID
		var providerRaw interface{}
		if fonnteResp != nil {
			providerRaw = fonnteResp.Raw
		}
		if logErr := db.LogWAMessage(r.Context(), pool, app.ID, &numberID, "out", req.Phone, req.Category, message, status, sendErr, providerRaw); logErr != nil {
			// Logging failure shouldn't hide a successful send from the caller; just note it server-side.
			_ = logErr
		}

		if sendErr != nil {
			writeJSON(w, http.StatusBadGateway, resp)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// brandPrefix ensures the tenant's name appears in the message, since a shared WA number sends
// on behalf of several businesses — the recipient must be able to tell which one is messaging.
func brandPrefix(appName, message string) string {
	tag := "[" + appName + "]"
	if strings.HasPrefix(strings.TrimSpace(message), tag) {
		return message
	}
	return tag + " " + message
}
