package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/auth"
	"pushhub/internal/db"
	"pushhub/internal/middleware"
)

const (
	pendingSessionTTL = 10 * time.Minute
	fullSessionTTL    = 24 * time.Hour
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

// Login authenticates username+password, then either:
//   - if TOTP isn't enrolled yet: starts enrollment (generates/reuses a secret, returns a QR
//     code) and sets a pending session cookie that only /admin/totp/confirm will accept.
//   - if TOTP is enrolled: requires a valid totp_code in the same request, then sets a full
//     session cookie.
//
// POST /admin/login
func Login(pool *pgxpool.Pool, issuer string, cookieSecure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		user, err := db.GetAdminUserByUsername(r.Context(), pool, req.Username)
		if err != nil || !auth.CheckPassword(user.PasswordHash, req.Password) {
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}

		if !user.TOTPEnabled {
			secret := ""
			if user.TOTPSecret != nil && *user.TOTPSecret != "" {
				secret = *user.TOTPSecret
			} else {
				newSecret, _, genErr := auth.GenerateTOTPSecret(issuer, user.Username)
				if genErr != nil {
					writeError(w, http.StatusInternalServerError, "failed to generate TOTP secret")
					return
				}
				if err := db.SetAdminTOTPSecret(r.Context(), pool, user.ID, newSecret); err != nil {
					writeError(w, http.StatusInternalServerError, "failed to store TOTP secret")
					return
				}
				secret = newSecret
			}

			otpauthURL := auth.BuildOTPAuthURL(issuer, user.Username, secret)
			qrPNG, err := auth.QRCodePNGBase64(otpauthURL)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to render QR code")
				return
			}

			token, err := auth.NewSessionToken()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create session")
				return
			}
			if err := db.CreateSession(r.Context(), pool, token, user.ID, true, pendingSessionTTL); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create session")
				return
			}
			setSessionCookie(w, token, pendingSessionTTL, cookieSecure)

			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":        "totp_setup_required",
				"secret":        secret,
				"otpauth_url":   otpauthURL,
				"qr_png_base64": qrPNG,
			})
			return
		}

		if req.TOTPCode == "" {
			writeError(w, http.StatusBadRequest, "totp_code is required")
			return
		}
		if user.TOTPSecret == nil || !auth.ValidateTOTPCode(*user.TOTPSecret, req.TOTPCode) {
			writeError(w, http.StatusUnauthorized, "invalid TOTP code")
			return
		}

		token, err := auth.NewSessionToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session")
			return
		}
		if err := db.CreateSession(r.Context(), pool, token, user.ID, false, fullSessionTTL); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session")
			return
		}
		setSessionCookie(w, token, fullSessionTTL, cookieSecure)

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

type totpConfirmRequest struct {
	Code string `json:"code"`
}

// TOTPConfirm completes enrollment: verifies one code against the pending secret, marks TOTP
// enabled, and promotes the pending session into a full session.
// POST /admin/totp/confirm
func TOTPConfirm(pool *pgxpool.Pool, cookieSecure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(middleware.SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "no active login in progress")
			return
		}
		sess, err := db.GetSession(r.Context(), pool, cookie.Value)
		if err != nil || !sess.PendingTOTP {
			writeError(w, http.StatusUnauthorized, "no TOTP enrollment in progress; log in again")
			return
		}
		user, err := db.GetAdminUserByID(r.Context(), pool, sess.AdminUserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session user not found")
			return
		}

		var req totpConfirmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if user.TOTPSecret == nil || !auth.ValidateTOTPCode(*user.TOTPSecret, req.Code) {
			writeError(w, http.StatusUnauthorized, "invalid TOTP code")
			return
		}

		if err := db.EnableAdminTOTP(r.Context(), pool, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enable TOTP")
			return
		}
		if err := db.PromoteSession(r.Context(), pool, cookie.Value, fullSessionTTL); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to promote session")
			return
		}
		setSessionCookie(w, cookie.Value, fullSessionTTL, cookieSecure)

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Logout clears the session, server-side and via cookie.
// POST /admin/logout
func Logout(pool *pgxpool.Pool, cookieSecure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil && cookie.Value != "" {
			_ = db.DeleteSession(r.Context(), pool, cookie.Value)
		}
		clearSessionCookie(w, cookieSecure)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Whoami reports the logged-in admin's username, used by the panel on page load to decide
// whether to show the login screen or the dashboard.
// GET /admin/whoami
func Whoami(w http.ResponseWriter, r *http.Request) {
	user := middleware.AdminUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
