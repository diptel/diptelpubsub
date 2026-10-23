package config

import (
	"os"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	DatabaseURL string // e.g. postgres://user:pass@host:5432/pushhub?sslmode=disable
	ListenAddr  string // e.g. :8080
	AdminToken  string // bearer token required to create/manage apps (tenants) via API/curl

	// Admin web panel (username/password + TOTP login).
	AdminBootstrapUsername string // only used once, to create the first admin account if none exists
	AdminBootstrapPassword string
	TOTPIssuer             string // shown in the authenticator app, e.g. "PushHub"
	CookieSecure           bool   // set true once served over HTTPS; false is required for plain-HTTP LAN testing

	// Email channel: one hub-level SMTP account shared across all tenants (SMTP_* env vars).
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string
}

func Load() Config {
	return Config{
		DatabaseURL:            getEnv("DATABASE_URL", "postgres://pushhub:pushhub@localhost:5432/pushhub?sslmode=disable"),
		ListenAddr:             getEnv("LISTEN_ADDR", ":8080"),
		AdminToken:             getEnv("ADMIN_TOKEN", ""),
		AdminBootstrapUsername: getEnv("ADMIN_BOOTSTRAP_USERNAME", ""),
		AdminBootstrapPassword: getEnv("ADMIN_BOOTSTRAP_PASSWORD", ""),
		TOTPIssuer:             getEnv("TOTP_ISSUER", "PushHub"),
		CookieSecure:           getEnv("COOKIE_SECURE", "false") == "true",

		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUsername: getEnv("SMTP_USERNAME", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		SMTPFromName: getEnv("SMTP_FROM_NAME", "Notification Hub"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
