#!/bin/sh
# Contoh kirim WhatsApp lewat curl — berguna untuk testing manual atau dari n8n
# (HTTP Request node) tanpa perlu nulis kode backend dulu.
#
# Nomor WA & token Fonnte dikelola terpusat di hub (tab "Nomor WA" di /panel/) — backend
# tiap app TIDAK perlu tahu/punya token Fonnte sendiri, cukup panggil endpoint ini dengan
# API key app masing-masing.

PUSHHUB_URL="https://pushhub.diptel.id"
API_KEY="ph_xxxxxxxxxxxxxxxxxxxx"   # API key app ini, didapat saat registrasi lewat /admin/apps atau panel

# Kirim notifikasi WhatsApp biasa (kategori "general" — notifikasi order, promo, CS, dll):
curl -X POST "$PUSHHUB_URL/send/wa" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "phone": "6281234567890",
    "message": "Pesananmu sedang diantar driver. Estimasi tiba 15 menit.",
    "category": "general"
  }'

# Kirim OTP / kode verifikasi (kategori "otp" — dirutekan ke nomor WA yang dikategorikan
# khusus OTP di panel, supaya terisolasi dari resiko blokir akibat trafik promo/broadcast):
curl -X POST "$PUSHHUB_URL/send/wa" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "phone": "6281234567890",
    "message": "Kode verifikasi riwariwi kamu: 482913. Jangan bagikan kode ini ke siapapun.",
    "category": "otp"
  }'

# Respons sukses: {"status":"sent","wa_number":"<label nomor WA yang dipakai>"}
# Respons gagal (misal nomor diblokir/token invalid): {"status":"failed","error":"..."}
# Pesan otomatis diberi prefix "[Nama App]" oleh hub kalau belum ada, supaya penerima tahu
# bisnis mana yang mengirim (nomor WA dipakai bersama beberapa app).
