package push

import (
	"encoding/json"
	"fmt"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// GenerateVAPIDKeys creates a new public/private VAPID keypair for a new tenant app.
func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	return webpush.GenerateVAPIDKeys()
}

// Subscription mirrors the PushSubscription object a browser gives the frontend.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Message is the notification payload delivered to the browser's service worker.
type Message struct {
	Title string          `json:"title"`
	Body  string          `json:"body"`
	Icon  string          `json:"icon,omitempty"`
	URL   string          `json:"url,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// SendResult tells the caller whether to keep the subscription or drop it.
type SendResult struct {
	StatusCode int
	// Gone is true when the push service says this endpoint no longer exists
	// (HTTP 404/410) — the caller should delete the subscription.
	Gone bool
}

// Send delivers one push message to one subscription using the app's VAPID keys.
func Send(sub Subscription, msg Message, vapidPublicKey, vapidPrivateKey, vapidSubject string) (*SendResult, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	resp, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.Keys.P256dh,
			Auth:   sub.Keys.Auth,
		},
	}, &webpush.Options{
		Subscriber:      vapidSubject,
		VAPIDPublicKey:  vapidPublicKey,
		VAPIDPrivateKey: vapidPrivateKey,
		TTL:             60,
	})
	if err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}
	defer resp.Body.Close()

	result := &SendResult{StatusCode: resp.StatusCode}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		result.Gone = true
	} else if resp.StatusCode >= 300 {
		return result, fmt.Errorf("push service returned status %d", resp.StatusCode)
	}
	return result, nil
}
