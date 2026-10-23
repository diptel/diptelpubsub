// Package wa sends WhatsApp messages through Fonnte and rate-limits sends per number so a
// shared number doesn't get flagged/blocked by WhatsApp for sending too fast.
package wa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FonnteResponse is Fonnte's typical JSON reply shape. Fields are loosely typed since Fonnte's
// API isn't strictly documented/stable; callers should treat the raw body as the source of truth.
type FonnteResponse struct {
	Status bool   `json:"status"`
	Reason string `json:"reason"`
	Raw    json.RawMessage
}

// Limiter enforces a minimum gap between sends on the same WA number (keyed by number ID), so
// concurrent /send/wa calls targeting the same shared number queue up instead of bursting.
// Each number gets its own lock, so a slow/rate-limited wait on one number never blocks sends
// on a different number.
type Limiter struct {
	mu    sync.Mutex // guards the locks map itself, not the per-number waits
	locks map[string]*numberLock
}

type numberLock struct {
	mu       sync.Mutex
	lastSent time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{locks: map[string]*numberLock{}}
}

func (l *Limiter) lockFor(numberID string) *numberLock {
	l.mu.Lock()
	defer l.mu.Unlock()
	nl, ok := l.locks[numberID]
	if !ok {
		nl = &numberLock{}
		l.locks[numberID] = nl
	}
	return nl
}

// Wait blocks until it's safe to send on numberID again, given minInterval, then marks "sent
// now". It holds that number's lock for the whole wait so concurrent senders on the SAME number
// are serialized (not just rate-checked) — which is what actually prevents bursts — while sends
// to other numbers proceed independently.
func (l *Limiter) Wait(ctx context.Context, numberID string, minInterval time.Duration) error {
	nl := l.lockFor(numberID)
	nl.mu.Lock()
	defer nl.mu.Unlock()

	if wait := minInterval - time.Since(nl.lastSent); !nl.lastSent.IsZero() && wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	nl.lastSent = time.Now()
	return nil
}

// SendMessage sends one WhatsApp text message via Fonnte's HTTP API.
func SendMessage(ctx context.Context, fonnteToken, phone, message string) (*FonnteResponse, error) {
	form := url.Values{}
	form.Set("target", phone)
	form.Set("message", message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.fonnte.com/send", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fonnteToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed FonnteResponse
	parsed.Raw = json.RawMessage(body)
	_ = json.Unmarshal(body, &parsed) // best-effort; Fonnte's shape can vary, raw body is kept regardless

	if resp.StatusCode >= 300 {
		return &parsed, fmt.Errorf("fonnte: http %d: %s", resp.StatusCode, string(body))
	}
	if !parsed.Status && parsed.Reason != "" {
		return &parsed, fmt.Errorf("fonnte: %s", parsed.Reason)
	}
	return &parsed, nil
}
