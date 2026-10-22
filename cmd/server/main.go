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

	"pushhub/internal/config"
	"pushhub/internal/db"
	"pushhub/internal/handlers"
	"pushhub/internal/middleware"
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

	// Admin endpoint for registering new tenant apps. Protected by a separate admin token.
	mux.Handle("POST /admin/apps", middleware.RequireAdminToken(cfg.AdminToken)(handlers.CreateApp(pool)))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("pushhub: listening on %s", cfg.ListenAddr)
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

// withCORS allows each app's frontend (on its own domain) to call the public endpoints
// directly from the browser. /send and /admin are never called from a browser, so this
// is safe even though it's permissive.
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
