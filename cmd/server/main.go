// Command server runs PushHub: a multi-tenant Web Push (VAPID) notification
// service shared across Fi's apps (riwariwi, briixbroadband, briix-capital/mifos, kasir, ...).
package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/auth"
	"pushhub/internal/config"
	"pushhub/internal/db"
	"pushhub/internal/handlers"
	"pushhub/internal/middleware"
	"pushhub/internal/panel"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pushhub: failed to connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("pushhub: connected to database")

	bootstrapAdmin(ctx, pool, cfg)

	mux := http.NewServeMux()

	// Health check for Docker/load balancer.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Public endpoints, called directly from each app's frontend (PWA).
	mux.Handle("GET /vapid-public-key", handlers.VAPIDPublicKey(pool))
	mux.Handle("POST /subscribe", handlers.Subscribe(pool))
	mux.Handle("POST /unsubscribe", handlers.Unsubscribe(pool))

	// Tenant-authenticated endpoint, called from each app's own backend (never the browser)
	// using the X-API-Key issued when the app was registered.
	mux.Handle("POST /send", middleware.RequireAppAPIKey(pool)(handlers.Send(pool)))

	// Admin web panel auth (public: logging in IS how you authenticate).
	mux.Handle("POST /admin/login", handlers.Login(pool, cfg.TOTPIssuer, cfg.CookieSecure))
	mux.Handle("POST /admin/totp/confirm", handlers.TOTPConfirm(pool, cfg.CookieSecure))
	mux.Handle("POST /admin/logout", handlers.Logout(pool, cfg.CookieSecure))
	mux.Handle("GET /admin/whoami", middleware.RequireFullSession(pool)(http.HandlerFunc(handlers.Whoami)))

	// Tenant-management endpoints: reachable via the web panel session OR the static
	// ADMIN_TOKEN bearer token (for curl/CI/automation use).
	adminAuth := middleware.RequireAdminAuth(pool, cfg.AdminToken)
	mux.Handle("GET /admin/apps", adminAuth(handlers.ListApps(pool)))
	mux.Handle("POST /admin/apps", adminAuth(handlers.CreateApp(pool)))
	mux.Handle("POST /admin/apps/{slug}/regenerate-key", adminAuth(handlers.RegenerateAppKey(pool)))

	// The admin panel itself (static HTML/JS, embedded in the binary).
	mux.Handle("/panel/", http.StripPrefix("/panel", panel.Handler()))
	mux.Handle("GET /panel", http.RedirectHandler("/panel/", http.StatusMovedPermanently))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("pushhub: listening on %s (admin panel at /panel/)", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("pushhub: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("pushhub: shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("pushhub: graceful shutdown failed: %v", err)
	}
}

// bootstrapAdmin creates the first admin account from ADMIN_BOOTSTRAP_USERNAME/PASSWORD if
// no admin exists yet. TOTP is enrolled on that account's first login via the panel.
func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) {
	count, err := db.CountAdminUsers(ctx, pool)
	if err != nil {
		log.Printf("pushhub: could not check admin user count: %v", err)
		return
	}
	if count > 0 {
		return
	}

	if cfg.AdminBootstrapUsername == "" || cfg.AdminBootstrapPassword == "" {
		log.Println("pushhub: WARNING no admin account exists and ADMIN_BOOTSTRAP_USERNAME/ADMIN_BOOTSTRAP_PASSWORD are not set — the panel at /panel/ cannot be logged into yet")
		return
	}

	hash, err := auth.HashPassword(cfg.AdminBootstrapPassword)
	if err != nil {
		log.Printf("pushhub: failed to hash bootstrap admin password: %v", err)
		return
	}
	if _, err := db.CreateAdminUser(ctx, pool, cfg.AdminBootstrapUsername, hash); err != nil {
		log.Printf("pushhub: failed to create bootstrap admin user: %v", err)
		return
	}
	log.Printf("pushhub: created initial admin account %q — log in at /panel/ to set up TOTP", cfg.AdminBootstrapUsername)
}

// withCORS allows each app's frontend (on its own domain) to call the public endpoints
// directly from the browser. /send and /admin are never called from a browser except the
// panel itself (same-origin), so this is safe even though it's permissive.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
