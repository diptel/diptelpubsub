package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
)

type ctxKey string

const (
	appCtxKey       ctxKey = "app"
	adminUserCtxKey ctxKey = "adminUser"
)

// SessionCookieName is the cookie used for admin web panel logins.
const SessionCookieName = "ph_admin_session"

// AppFromContext retrieves the authenticated tenant app set by RequireAppAPIKey.
func AppFromContext(ctx context.Context) *db.App {
	a, _ := ctx.Value(appCtxKey).(*db.App)
	return a
}

// AdminUserFromContext retrieves the authenticated admin set by RequireAdminAuth
// when the request was authenticated via session cookie (nil if via bearer token).
func AdminUserFromContext(ctx context.Context) *db.AdminUser {
	u, _ := ctx.Value(adminUserCtxKey).(*db.AdminUser)
	return u
}

// RequireAppAPIKey authenticates a request using the "X-API-Key" header, which identifies
// which tenant (riwariwi, briixbroadband, briix-capital, kasir, ...) is calling.
func RequireAppAPIKey(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				http.Error(w, `{"error":"missing X-API-Key header"}`, http.StatusUnauthorized)
				return
			}
			app, err := db.GetAppByAPIKey(r.Context(), pool, apiKey)
			if err != nil {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), appCtxKey, app)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminToken protects tenant-management endpoints (creating new apps) with a
// separate admin-only bearer token, so tenant backends never need admin access.
func RequireAdminToken(adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if adminToken == "" {
				http.Error(w, `{"error":"admin endpoint disabled: ADMIN_TOKEN not set"}`, http.StatusForbidden)
				return
			}
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == "" || token != adminToken {
				http.Error(w, `{"error":"invalid admin token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sessionFromCookie looks up an active, non-pending session from the request's session cookie.
// Returns (nil, nil) when there's simply no valid session — callers decide whether that's fatal.
func sessionFromCookie(r *http.Request, pool *pgxpool.Pool) (*db.AdminUser, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil
	}
	sess, err := db.GetSession(r.Context(), pool, cookie.Value)
	if err != nil || sess.PendingTOTP {
		return nil, nil
	}
	user, err := db.GetAdminUserByID(r.Context(), pool, sess.AdminUserID)
	if err != nil {
		return nil, nil
	}
	return user, nil
}

// RequireAdminAuth protects the admin panel's API routes (/admin/apps and friends).
// It accepts EITHER a valid session cookie (web panel login) OR the static ADMIN_TOKEN
// bearer token (for curl/CI/automation use), so both paths keep working side by side.
func RequireAdminAuth(pool *pgxpool.Pool, adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, _ := sessionFromCookie(r, pool); user != nil {
				ctx := context.WithValue(r.Context(), adminUserCtxKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if adminToken != "" {
				auth := r.Header.Get("Authorization")
				token := strings.TrimPrefix(auth, "Bearer ")
				if token != "" && token == adminToken {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, `{"error":"unauthenticated: log in to the panel or provide a valid admin bearer token"}`, http.StatusUnauthorized)
		})
	}
}

// RequireFullSession protects panel-only routes that must never be reachable with the
// static ADMIN_TOKEN (none currently, but kept distinct from RequireAdminAuth for clarity
// and so logout/whoami always reflect a real logged-in browser session).
func RequireFullSession(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := sessionFromCookie(r, pool)
			if user == nil {
				http.Error(w, `{"error":"not logged in"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), adminUserCtxKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
