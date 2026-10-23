// Package emailsend sends email via a hub-level SMTP account (shared across tenants, same
// model as the WhatsApp numbers). Uses net/smtp from the standard library — no extra dependency,
// which matters here since this project's go.sum is generated at Docker build time.
package emailsend

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Config holds the hub's single outbound SMTP account. Set via env vars (SMTP_*), not stored
// per-tenant — every tenant sends "from" the same hub mailbox, same as WhatsApp numbers.
type Config struct {
	Host     string
	Port     string // e.g. "587"
	Username string
	Password string
	From     string // envelope/header From address, e.g. "notify@diptel.id"
	FromName string // e.g. "Diptel Notification Hub"
}

// Enabled reports whether SMTP is configured at all, so handlers can fail fast with a clear
// error instead of a confusing SMTP dial failure.
func (c Config) Enabled() bool {
	return c.Host != "" && c.Username != "" && c.Password != ""
}

// Send delivers a plain-text or HTML email through the hub's SMTP account.
func Send(cfg Config, to, subject, body string, html bool) error {
	if !cfg.Enabled() {
		return fmt.Errorf("email channel not configured: set SMTP_HOST, SMTP_USERNAME, SMTP_PASSWORD")
	}

	contentType := "text/plain; charset=UTF-8"
	if html {
		contentType = "text/html; charset=UTF-8"
	}

	fromHeader := cfg.From
	if cfg.FromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.From)
	}

	var msg strings.Builder
	msg.WriteString("From: " + fromHeader + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: " + contentType + "\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	addr := cfg.Host + ":" + cfg.Port
	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg.String()))
}
