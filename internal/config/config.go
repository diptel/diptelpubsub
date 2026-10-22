package config

import (
	"os"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	DatabaseURL string // e.g. postgres://user:pass@host:5432/pushhub?sslmode=disable
	ListenAddr  string // e.g. :8080
	AdminToken  string // bearer token required to create/manage apps (tenants)
}

func Load() Config {
	return Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://pushhub:pushhub@localhost:5432/pushhub?sslmode=disable"),
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		AdminToken:  getEnv("ADMIN_TOKEN", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
