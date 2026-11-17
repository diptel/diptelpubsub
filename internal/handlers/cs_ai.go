// CS Hub AI assistant ("AiSyah" by default). A new session is answered automatically by this
// assistant (cs_sessions.handled_by defaults to 'ai'); a staff member takes over from the panel
// (TakeOverCSSession in cs_admin.go), after which the assistant stays silent in that session —
// checked here on every incoming message — until the session is closed. The AI conversation
// history is never removed, so staff see exactly what the customer and AiSyah already discussed.
package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/aiassist"
	"pushhub/internal/db"
	"pushhub/internal/wa"
)

// AIClient, AIAssistantName and AIEnabled are set once from config in main.go (same pattern as
// db.SessionIdleTimeout) rather than threaded through every CS Hub function signature — this is
// a single process, config doesn't change at runtime. AIClient may be non-nil with an empty API
// key (Enabled() reports false in that case); the assistant simply never auto-replies and staff
// answer manually, which is a normal, supported state, not an error.
var (
	AIClient        *aiassist.Client
	AIAssistantName = "AiSyah"
	AIEnabled       = true
)

// aiActive reports whether the AI assistant should be answering right now at all (global
// kill-switch and API key configured) — a per-session check (handled_by, status) still applies
// on top of this in continueOpenSession.
func aiActive() bool {
	return AIEnabled && AIClient != nil && AIClient.Enabled()
}

// aiGreeting is what AiSyah says once a session connects to a tenant (either via the menu or an
// auto-detected reference number), replacing the old generic "Anda terhubung dengan CS X" text so
// the assistant introduces itself by name from the first message in the conversation.
func aiGreeting(appLabel string) string {
	return fmt.Sprintf(
		"Halo, saya %s, asisten CS %s. Ceritakan kendala Anda beserta nomor pesanan/transaksi (jika ada), saya bantu dulu ya. Ketik MENU untuk ganti layanan.",
		AIAssistantName, appLabel,
	)
}

// aiMenuHeader is the first line of the service-selection menu (buildMenu in cs_bot.go), so the
// assistant's name appears even before a customer has picked a tenant.
func aiMenuHeader() string {
	return fmt.Sprintf("Halo! Saya %s, asisten BRIIX. Anda ingin bantuan untuk layanan apa?\n", AIAssistantName)
}

// maybeReplyWithAI kicks off an async AI reply for an ordinary message in an open session, if the
// assistant is active globally and this particular session is still handled_by 'ai'. Fire-and-
// forget (like forwardToTenant) so a slow OpenAI call never delays the Fonnte webhook response.
func maybeReplyWithAI(pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, session *db.CSSession) {
	if !aiActive() || session.HandledBy != "ai" {
		return
	}
	go replyWithAI(pool, limiter, number, session)
}

func replyWithAI(pool *pgxpool.Pool, limiter *wa.Limiter, number *db.WANumber, session *db.CSSession) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Re-check right before calling OpenAI: a staff "Tangani" click can land while this goroutine
	// was merely scheduled (two customer messages arriving close together), and AiSyah must never
	// race a staff reply into the same conversation.
	fresh, err := db.GetCSSession(ctx, pool, session.ID)
	if err != nil {
		log.Printf("pushhub: cs hub ai: failed to re-check session %s: %v", session.ID, err)
		return
	}
	if fresh.HandledBy != "ai" || fresh.Status != "open" {
		return
	}

	appLabel := "BRIIX"
	refPrefix := ""
	if fresh.AppID != nil {
		if app, aerr := db.GetAppByID(ctx, pool, *fresh.AppID); aerr == nil {
			appLabel = csMenuLabel(app)
			if app.CSRefPrefix != nil {
				refPrefix = *app.CSRefPrefix
			}
		}
	}

	msgs, err := db.ListCSMessages(ctx, pool, session.ID)
	if err != nil {
		log.Printf("pushhub: cs hub ai: failed to load history for session %s: %v", session.ID, err)
		return
	}
	history := make([]aiassist.Message, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		if m.Direction == "out" {
			role = "assistant"
		}
		history = append(history, aiassist.Message{Role: role, Content: m.Message})
	}
	if len(history) == 0 {
		return // nothing to answer yet
	}

	reply, err := AIClient.Reply(ctx, aiSystemPrompt(appLabel, refPrefix), history)
	if err != nil {
		log.Printf("pushhub: cs hub ai: OpenAI call failed for session %s: %v", session.ID, err)
		return
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return
	}

	// Check again right before sending: the OpenAI call itself can take a few seconds, long
	// enough for a staff member to take over in the meantime.
	fresh2, err := db.GetCSSession(ctx, pool, session.ID)
	if err != nil || fresh2.HandledBy != "ai" || fresh2.Status != "open" {
		return
	}

	label := AIAssistantName
	sendFromHub(ctx, pool, limiter, number, session.Phone, reply, "cs")
	_ = db.AppendCSMessage(ctx, pool, session.ID, "out", &label, reply)
}

func aiSystemPrompt(appLabel, refPrefix string) string {
	refNote := ""
	if refPrefix != "" {
		refNote = fmt.Sprintf(" (biasanya berformat %s...)", refPrefix)
	}
	return fmt.Sprintf(
		"Kamu adalah %s, asisten CS AI berbasis WhatsApp untuk layanan %s, bagian dari ekosistem BRIIX. "+
			"Jawab singkat, sopan, dan dalam Bahasa Indonesia. "+
			"Jika pelanggan sudah menyebutkan nomor pesanan/transaksi%s, akui nomor tersebut dan jangan menanyakannya ulang. "+
			"Kamu TIDAK punya akses ke data pesanan/transaksi/pembayaran pelanggan — jangan berpura-pura mengecek atau memastikan status apa pun. "+
			"Jika pelanggan butuh pengecekan data, komplain yang serius, atau terlihat kesal/minta bicara dengan manusia, "+
			"katakan dengan sopan bahwa kamu akan meneruskan ke tim CS manusia untuk ditindaklanjuti, lalu berhenti — jangan mencoba menyelesaikannya sendiri.",
		AIAssistantName, appLabel, refNote,
	)
}
