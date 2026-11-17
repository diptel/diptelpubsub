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
	"sync"
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

// aiPersona is a free-text instruction staff can edit from the panel ("Instruksi AI" tab) — tone,
// house rules, things to always/never say — appended to every aiSystemPrompt call, so adjusting
// AiSyah's personality no longer requires a code change and redeploy. Loaded from hub_settings
// (key "ai_persona") at startup by main.go, and kept in sync in memory whenever a staff member
// saves a new value via UpdateAISettings in cs_admin.go — a mutex because it's read from the
// per-message AI goroutines and written from an HTTP handler goroutine.
var (
	aiPersonaMu sync.RWMutex
	aiPersona   string
)

// SetAIPersonaCache updates the in-memory persona instruction, called once at startup (main.go,
// loaded from the database) and again every time it's saved from the panel (cs_admin.go) — so a
// save takes effect immediately, with no restart needed.
func SetAIPersonaCache(v string) {
	aiPersonaMu.Lock()
	aiPersona = v
	aiPersonaMu.Unlock()
}

// AIPersona returns the current persona instruction (possibly empty — nothing saved yet).
func AIPersona() string {
	aiPersonaMu.RLock()
	defer aiPersonaMu.RUnlock()
	return aiPersona
}

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
		"Halo, terima kasih telah menghubungi %s. Saya %s, asisten layanan pelanggan kami. "+
			"Silakan ceritakan kendala Anda beserta nomor pesanan/transaksi (jika ada), agar dapat segera kami bantu. "+
			"Ketik MENU untuk berpindah layanan.",
		appLabel, AIAssistantName,
	)
}

// aiMenuHeader is the first line of the service-selection menu (buildMenu in cs_bot.go), so the
// assistant's name appears even before a customer has picked a tenant.
func aiMenuHeader() string {
	return fmt.Sprintf("Halo, terima kasih telah menghubungi BRIIX. Saya %s, asisten layanan pelanggan kami. Ada yang bisa kami bantu? Silakan pilih layanan berikut:\n", AIAssistantName)
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
	prompt := fmt.Sprintf(
		"Kamu adalah %s, asisten layanan pelanggan resmi untuk %s, bagian dari ekosistem BRIIX. "+
			"Gunakan Bahasa Indonesia yang sopan, hangat, dan profesional, selayaknya staf customer service perusahaan — ringkas dan jelas, tanpa istilah teknis atau singkatan yang membingungkan pelanggan. "+
			"Jika pelanggan sudah menyebutkan nomor pesanan/transaksi%s, akui nomor tersebut dengan baik dan jangan menanyakannya ulang. "+
			"Kamu TIDAK memiliki akses ke data pesanan/transaksi/pembayaran pelanggan — jangan pernah berpura-pura sedang mengecek atau memastikan status apa pun. "+
			"Jika pelanggan memerlukan pengecekan data, menyampaikan komplain yang serius, atau tampak kurang puas, "+
			"sampaikan dengan sopan bahwa keluhannya akan diteruskan kepada tim kami untuk ditindaklanjuti lebih lanjut, lalu hentikan upaya penyelesaian sendiri — jangan menyebut dirimu sebagai AI/robot atau membedakan diri dari \"tim manusia\"; cukup sebut \"tim kami\" seolah bagian dari satu tim layanan pelanggan yang sama.",
		AIAssistantName, appLabel, refNote,
	)
	// Staff-editable addendum (panel tab "Instruksi AI") — house style, extra dos/don'ts, anything
	// that shouldn't require a code change. Appended last so it can refine but not be silently
	// dropped by the base prompt above; AIPersona() is "" until someone saves one from the panel.
	if persona := strings.TrimSpace(AIPersona()); persona != "" {
		prompt += "\n\nInstruksi tambahan dari admin (ikuti juga ini):\n" + persona
	}
	return prompt
}
