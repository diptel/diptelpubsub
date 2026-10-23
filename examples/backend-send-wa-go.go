// Contoh pemanggilan channel WhatsApp PushHub dari backend tiap app (Go), misalnya dari
// backend riwariwi saat ada order baru atau perlu kirim OTP. Pola yang sama bisa dipakai
// dari backend bahasa lain (Node.js, PHP, dll) — cukup POST JSON biasa dengan header
// X-API-Key, sama seperti channel Push.
//
// Nomor WA & token Fonnte dikelola terpusat di hub — backend app ini tidak perlu tahu
// token Fonnte sama sekali, cukup API key app sendiri.
package examples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

const pushHubURL = "https://pushhub.diptel.id" // ganti kalau beda domain di environment lain

type sendWARequest struct {
	Phone    string `json:"phone"`
	Message  string `json:"message"`
	Category string `json:"category,omitempty"` // "otp" | "general" (default "general")
}

type sendWAResponse struct {
	Status   string `json:"status"`
	WANumber string `json:"wa_number,omitempty"`
	Error    string `json:"error,omitempty"`
}

// apiKey didapat sekali saat app didaftarkan lewat POST /admin/apps (atau panel),
// simpan sebagai env var di backend masing-masing app (JANGAN hardcode, JANGAN expose ke frontend).
func sendWhatsApp(apiKey, phone, message, category string) (*sendWAResponse, error) {
	payload := sendWARequest{Phone: phone, Message: message, Category: category}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, pushHubURL+"/send/wa", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out sendWAResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && out.Error == "" {
		return &out, fmt.Errorf("pushhub: unexpected status %d", resp.StatusCode)
	}
	return &out, nil
}

// Contoh pemakaian saat order driver riwariwi berubah status (kategori "general"):
//
//	sendWhatsApp(os.Getenv("PUSHHUB_API_KEY"), customer.Phone,
//	    "Pesananmu sedang diantar driver. Estimasi tiba 15 menit.", "general")
//
// Contoh pemakaian saat kirim OTP (kategori "otp" — dirutekan ke nomor WA khusus OTP):
//
//	sendWhatsApp(os.Getenv("PUSHHUB_API_KEY"), user.Phone,
//	    fmt.Sprintf("Kode verifikasi riwariwi kamu: %s. Jangan bagikan ke siapapun.", otpCode), "otp")
