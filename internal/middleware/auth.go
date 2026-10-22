package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
)

type ctxKey string

const appCtxKey ctxKey = "app"

// AppFromContext retrieves the authenticated tenant app set by RequireAppAPIKey.
func AppFromContext(ctx context.Context) *db.App {
	a, _ := ctx.Value(appCtxKey).(*db.App)
	return a
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
