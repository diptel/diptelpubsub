#!/bin/sh
# Contoh kirim notifikasi lewat curl — berguna untuk testing manual atau dari n8n
# (HTTP Request node) tanpa perlu nulis kode backend dulu.

PUSHHUB_URL="https://push.briix.co.id"
API_KEY="ph_xxxxxxxxxxxxxxxxxxxx"   # didapat saat registrasi app lewat /admin/apps

# Kirim ke satu user:
curl -X POST "$PUSHHUB_URL/send" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "user_id": "customer-123",
    "title": "Pesananmu sedang diantar",
    "body": "Driver sedang menuju lokasimu",
    "url": "/orders/123"
  }'

# Broadcast ke semua subscriber app ini (mis. promo):
curl -X POST "$PUSHHUB_URL/send" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "broadcast": true,
    "title": "Promo Hari Ini",
    "body": "Gratis ongkir untuk semua pesanan F&B",
    "url": "/promo"
  }'
