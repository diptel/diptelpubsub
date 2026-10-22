// Contoh pemanggilan PushHub dari backend tiap app (Go), misalnya dari backend riwariwi
// saat status order berubah. Pola yang sama bisa dipakai dari backend bahasa lain
// (Node.js, PHP, dll) — cukup POST JSON biasa dengan header X-API-Key.
package examples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

const pushHubURL = "https://push.briix.co.id" // ganti dengan URL deploy PushHub

// apiKey didapat sekali saat app didaftarkan lewat POST /admin/apps,
// simpan sebagai env var di backend masing-masing app (JANGAN hardcode, JANGAN expose ke frontend).
func sendOrderNotification(apiKey, userID, title, body, url string) error {
	payload := map[string]interface{}{
		"user_id": userID,
		"title":   title,
		"body":    body,
		"url":     url, // dibuka saat notifikasi diklik, mis. "/orders/123"
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, pushHubURL+"/send", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("pushhub: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Contoh pemakaian saat order driver riwariwi berubah status:
//
//	sendOrderNotification(os.Getenv("PUSHHUB_API_KEY"), order.CustomerID,
//	    "Pesananmu sedang diantar", "Driver sedang menuju lokasimu", "/orders/"+order.ID)
