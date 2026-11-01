package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/wa"
)

// handleCSReply runs the CS Hub bot for one incoming message on the shared "cs" category number.
// See the "CS Hub" section of migrations/0004_cs_hub.sql and the project's PushHub status doc for
// the agreed flow: no open session -> ask which service (or auto-detect from a reference number
// / wa.me deep-link text) -> session open -> message goes straight into the conversation for a
// staff member to answer from the panel; "MENU" switches service; idle 24h -> treated as closed.
func handleCSReply(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, phone, message string) {
	session, err := db.GetActiveSession(ctx, pool, number.ID, phone)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		log.Printf("pushhub: cs hub: failed to look up session: %v", err)
		return
	}

	if errors.Is(err, db.ErrNotFound) || session == nil {
		startNewCSSession(ctx, pool, limiter, number, phone, message)
		return
	}

	switch session.Status {
	case "awaiting_service":
		continueServiceSelection(ctx, pool, limiter, number, session, message)
	case "open":
		continueOpenSession(ctx, pool, limiter, number, session, message)
	default:
		// Shouldn't happen (GetActiveSession excludes "closed"), but don't drop the message.
		startNewCSSession(ctx, pool, limiter, number, phone, message)
	}
}

func startNewCSSession(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, phone, message string) {
	if app, err := detectAppByRef(ctx, pool, message); err == nil && app != nil {
		s, cerr := db.CreateCSSession(ctx, pool, number.ID, phone, nil)
		if cerr != nil {
			log.Printf("pushhub: cs hub: failed to create session: %v", cerr)
			return
		}
		_ = db.AppendCSMessage(ctx, pool, s.ID, "in", nil, message)
		if _, serr := db.SetSessionApp(ctx, pool, s.ID, app.ID); serr != nil {
			log.Printf("pushhub: cs hub: failed to set session app: %v", serr)
			return
		}
		reply := fmt.Sprintf("Anda terhubung dengan CS %s. Tuliskan kendala Anda beserta nomor pesanan/transaksi (jika ada).", csMenuLabel(app))
		_ = db.AppendCSMessage(ctx, pool, s.ID, "out", nil, reply)
		sendFromHub(ctx, pool, limiter, number, phone, reply, "cs")
		return
	}

	apps, err := db.ListCSEnabledApps(ctx, pool)
	if err != nil || len(apps) == 0 {
		log.Printf("pushhub: cs hub: no cs_enabled apps configured (or lookup failed: %v)", err)
		sendFromHub(ctx, pool, limiter, number, phone, "Mohon tunggu, tim kami akan segera membalas.", "cs")
		return
	}
	ids, menuText := buildMenu(apps)
	s, cerr := db.CreateCSSession(ctx, pool, number.ID, phone, ids)
	if cerr != nil {
		log.Printf("pushhub: cs hub: failed to create session: %v", cerr)
		return
	}
	_ = db.AppendCSMessage(ctx, pool, s.ID, "in", nil, message)
	_ = db.AppendCSMessage(ctx, pool, s.ID, "out", nil, menuText)
	sendFromHub(ctx, pool, limiter, number, phone, menuText, "cs")
}

func continueServiceSelection(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, session *db.CSSession, message string) {
	_ = db.AppendCSMessage(ctx, pool, session.ID, "in", nil, message)

	if strings.EqualFold(strings.TrimSpace(message), "menu") {
		apps, _ := db.ListCSEnabledApps(ctx, pool)
		ids, menuText := buildMenu(apps)
		_, _ = db.ResetSessionToMenu(ctx, pool, session.ID, ids)
		_ = db.AppendCSMessage(ctx, pool, session.ID, "out", nil, menuText)
		sendFromHub(ctx, pool, limiter, number, session.Phone, menuText, "cs")
		return
	}

	var chosenAppID string
	if n := atoiOrZero(message); n > 0 && n <= len(session.PendingMenu) {
		chosenAppID = session.PendingMenu[n-1]
	} else if app, err := detectAppByRef(ctx, pool, message); err == nil && app != nil {
		chosenAppID = app.ID
	}

	if chosenAppID == "" {
		reply := "Mohon balas dengan nomor layanan yang sesuai, atau ketik MENU untuk melihat pilihan lagi."
		_ = db.AppendCSMessage(ctx, pool, session.ID, "out", nil, reply)
		sendFromHub(ctx, pool, limiter, number, session.Phone, reply, "cs")
		return
	}

	app, err := db.GetAppByID(ctx, pool, chosenAppID)
	if err != nil {
		log.Printf("pushhub: cs hub: chosen app %s not found: %v", chosenAppID, err)
		return
	}
	if _, serr := db.SetSessionApp(ctx, pool, session.ID, chosenAppID); serr != nil {
		log.Printf("pushhub: cs hub: failed to set session app: %v", serr)
		return
	}
	reply := fmt.Sprintf("Anda terhubung dengan CS %s. Tuliskan kendala Anda beserta nomor pesanan/transaksi (jika ada).", csMenuLabel(app))
	_ = db.AppendCSMessage(ctx, pool, session.ID, "out", nil, reply)
	sendFromHub(ctx, pool, limiter, number, session.Phone, reply, "cs")
}

func continueOpenSession(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, session *db.CSSession, message string) {
	if strings.EqualFold(strings.TrimSpace(message), "menu") {
		_ = db.AppendCSMessage(ctx, pool, session.ID, "in", nil, message)
		apps, _ := db.ListCSEnabledApps(ctx, pool)
		ids, menuText := buildMenu(apps)
		_, _ = db.ResetSessionToMenu(ctx, pool, session.ID, ids)
		_ = db.AppendCSMessage(ctx, pool, session.ID, "out", nil, menuText)
		sendFromHub(ctx, pool, limiter, number, session.Phone, menuText, "cs")
		return
	}

	// Ordinary message in an open session: just record it for the panel inbox and bump the idle
	// clock. A staff member answers from the panel — the bot doesn't auto-reply here.
	_ = db.AppendCSMessage(ctx, pool, session.ID, "in", nil, message)
	_ = db.TouchSession(ctx, pool, session.ID)

	// Optional: forward to the tenant's own webhook so their backend can attach order context.
	// Purely informational now that tenants no longer run their own reply bot on this channel.
	if session.AppID != nil {
		if app, err := db.GetAppByID(ctx, pool, *session.AppID); err == nil && app.WebhookURL != nil && *app.WebhookURL != "" {
			forwardToTenant(*app.WebhookURL, app.Slug, session.Phone, message)
		}
	}
}

// detectAppByRef looks for a cs_enabled tenant whose reference prefix (e.g. "KLR-") appears
// anywhere in the message, so a customer pasting "KLR-123 belum sukses" or a
// wa.me/<cs>?text=...KLR-123 deep link lands directly in that tenant's session without being
// asked to pick from the menu.
func detectAppByRef(ctx context.Context, pool *pgxpool.Pool, message string) (*db.App, error) {
	if strings.TrimSpace(message) == "" {
		return nil, nil
	}
	apps, err := db.ListCSEnabledApps(ctx, pool)
	if err != nil {
		return nil, err
	}
	upper := strings.ToUpper(message)
	for i := range apps {
		app := apps[i]
		if app.CSRefPrefix == nil || *app.CSRefPrefix == "" {
			continue
		}
		if strings.Contains(upper, strings.ToUpper(*app.CSRefPrefix)) {
			return &app, nil
		}
	}
	return nil, nil
}

func csMenuLabel(app *db.App) string {
	if app.CSMenuLabel != nil && *app.CSMenuLabel != "" {
		return *app.CSMenuLabel
	}
	return app.Name
}

// buildMenu renders the numbered service-selection text and the app-ID order it corresponds to
// (stored as the session's pending_menu so a later "2" can be resolved back to a tenant).
func buildMenu(apps []db.App) ([]string, string) {
	var b strings.Builder
	b.WriteString("Halo! Anda ingin bantuan untuk layanan apa?\n")
	ids := make([]string, 0, len(apps))
	for i, app := range apps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, csMenuLabel(&app))
		ids = append(ids, app.ID)
	}
	b.WriteString("\nBalas dengan nomor layanan, atau sertakan nomor pesanan/transaksi Anda.")
	return ids, b.String()
}
