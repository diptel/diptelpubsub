package handlers

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"pushhub/internal/db"
	"pushhub/internal/wa"
)

// idleSessionClosedText tells the customer their session was auto-closed from inactivity, so a
// silent close (nobody explicitly said "selesai") still doesn't leave them wondering whether
// anyone is still there — and tells them how to pick things back up if they need to.
const idleSessionClosedText = "Percakapan ini otomatis ditutup karena sudah lama tidak ada balasan. Kalau masih butuh bantuan, silakan kirim pesan baru ya."

// CloseIdleSessions auto-closes CS Hub sessions that have sat open (or awaiting_service) past
// db.SessionIdleTimeout with no new message from either side, and notifies the customer — same
// notice CloseCSSession sends on a manual "selesai", just worded for an automatic close. Called
// periodically from a ticker in cmd/server/main.go (runIdleSessionCloser).
//
// The lazy check in db.GetActiveSession only catches a session when the customer messages again;
// most idle conversations never get another message, so this sweep is what actually closes (and
// notifies) the rest instead of leaving them stuck "open" in the panel forever.
func CloseIdleSessions(ctx context.Context, pool *pgxpool.Pool, limiter *wa.Limiter) {
	sessions, err := db.ListIdleSessions(ctx, pool, 200)
	if err != nil {
		log.Printf("pushhub: cs hub: failed to list idle sessions: %v", err)
		return
	}

	closed := 0
	for _, session := range sessions {
		if err := db.CloseSession(ctx, pool, session.ID); err != nil {
			if err != db.ErrNotFound {
				log.Printf("pushhub: cs hub: failed to auto-close idle session %s: %v", session.ID, err)
			}
			continue
		}
		closed++

		// Best-effort notify: the session is already closed either way, so a failure here (e.g.
		// the CS number got deleted) shouldn't stop the sweep from moving to the next session.
		number, numErr := db.GetWANumberByID(ctx, pool, session.WANumberID)
		if numErr != nil {
			log.Printf("pushhub: cs hub: idle session %s closed but its WA number is gone: %v", session.ID, numErr)
			continue
		}
		sendFromHub(ctx, pool, limiter, number, session.Phone, idleSessionClosedText, "cs")
		_ = db.AppendCSMessage(ctx, pool, session.ID, "out", nil, idleSessionClosedText)
	}

	if closed > 0 {
		log.Printf("pushhub: cs hub: auto-closed %d idle session(s)", closed)
	}
}
