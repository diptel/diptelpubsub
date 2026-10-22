# PushHub

Push notification service (Web Push / VAPID) multi-tenant yang dipakai bersama oleh beberapa
project: **riwariwi**, **briixbroadband**, **briix capital / mifos mobile PWA**, **kasir**, dan
project lain di masa depan.

Satu service, satu set VAPID key **per app** (tenant), satu database Postgres. Tiap app cukup
panggil API ini untuk subscribe dan kirim notifikasi — tidak perlu implement Web Push sendiri-sendiri.

## Arsitektur singkat

```
Browser (PWA riwariwi/briixbroadband/dll)
   │  subscribe via PushManager
   ▼
PushHub  ── /vapid-public-key  (public, by app slug)
         ── /subscribe         (public, by app slug)
         ── /unsubscribe       (public, by app slug)
         ── /send              (tenant-auth via X-API-Key)   ◄── dipanggil backend tiap app
         ── /admin/apps        (admin-auth via ADMIN_TOKEN)  ◄── daftarkan app baru
   │
   ▼
Postgres (apps, subscriptions, notifications_log)
```

- **Frontend** tiap app memanggil `/vapid-public-key`, `/subscribe`, `/unsubscribe` langsung —
  tidak butuh API key (publik, diidentifikasi lewat `app` slug).
- **Backend** tiap app memanggil `/send` dengan header `X-API-Key` miliknya sendiri saat mau
  mengirim notifikasi (order berubah status, promo, dll).
- **Admin** (kamu) memanggil `/admin/apps` sekali untuk mendaftarkan app baru dan mendapatkan
  API key + VAPID public key-nya.

## Menjalankan (deploy ke server, mis. 1Panel Pro)

1. Copy `.env.example` jadi `.env`, isi `POSTGRES_PASSWORD` dan `ADMIN_TOKEN` dengan nilai acak
   yang kuat.
2. Jalankan:
   ```sh
   docker compose up -d --build
   ```
3. Cek service hidup:
   ```sh
   curl http://localhost:8080/healthz
   ```

Migrasi SQL di `migrations/0001_init.sql` otomatis dijalankan oleh image Postgres resmi saat
volume database masih kosong (lewat `docker-entrypoint-initdb.d`).

## Mendaftarkan app baru (tenant)

Tiap project (riwariwi, briixbroadband, briix-capital, kasir, ...) didaftarkan sekali:

```sh
curl -X POST http://localhost:8080/admin/apps \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "slug": "riwariwi",
    "name": "Riwariwi",
    "vapid_subject": "mailto:fika@briix.co.id"
  }'
```

Respons:

```json
{
  "slug": "riwariwi",
  "name": "Riwariwi",
  "api_key": "ph_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "vapid_public_key": "Bxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

- **Simpan `api_key`** di backend riwariwi (env var, mis. `PUSHHUB_API_KEY`) — jangan pernah
  dikirim ke frontend.
- **`vapid_public_key`** aman dipakai di frontend, tapi tidak perlu disimpan manual: frontend
  bisa ambil sendiri lewat `GET /vapid-public-key?app=riwariwi`.

Ulangi untuk `briixbroadband`, `briix-capital`, `kasir`, dst — tiap app dapat `api_key` dan
VAPID keypair sendiri-sendiri (isolasi penuh antar project).

## Integrasi di tiap app

Lihat folder `examples/`:

- `examples/service-worker.js` — pasang sebagai `/sw.js` di tiap PWA.
- `examples/frontend-subscribe.js` — kode subscribe/unsubscribe di frontend.
- `examples/backend-send-go.go` — contoh kirim notifikasi dari backend Go.
- `examples/backend-send-curl.sh` — contoh kirim lewat curl / n8n HTTP Request node.

Alur integrasi tiap app baru:

1. Daftar app lewat `/admin/apps` → dapat `api_key`.
2. Pasang `examples/service-worker.js` sebagai `/sw.js`.
3. Panggil `enablePushNotifications()` dari `examples/frontend-subscribe.js` saat user
   mengaktifkan notifikasi (ganti `APP_SLUG` dan `PUSHHUB_URL`).
4. Di backend app tersebut, panggil `POST /send` dengan `X-API-Key` saat ada event yang perlu
   dikirim notifikasi (lihat `examples/backend-send-go.go`).

## API Reference

### `GET /vapid-public-key?app=<slug>`
Publik. Mengembalikan VAPID public key milik app tersebut.

### `POST /subscribe`
Publik. Body:
```json
{ "app": "riwariwi", "user_id": "optional-user-id", "subscription": { "endpoint": "...", "keys": { "p256dh": "...", "auth": "..." } } }
```

### `POST /unsubscribe`
Publik. Body: `{ "app": "riwariwi", "endpoint": "..." }`

### `POST /send`
Butuh header `X-API-Key: <api_key milik app>`. Body:
```json
{ "user_id": "customer-123", "title": "...", "body": "...", "icon": "...", "url": "..." }
```
atau untuk broadcast ke semua subscriber app ini:
```json
{ "broadcast": true, "title": "...", "body": "..." }
```
Subscription yang sudah kadaluarsa (endpoint dihapus browser) otomatis dibersihkan dari
database saat pengiriman gagal dengan status 404/410 dari push service.

### `POST /admin/apps`
Butuh header `Authorization: Bearer <ADMIN_TOKEN>`. Mendaftarkan app/tenant baru, generate
VAPID keypair dan API key baru.

## Catatan teknis

- **Isolasi per app**: tiap app punya VAPID keypair & API key sendiri — notifikasi riwariwi
  tidak bisa "bocor" terkirim pakai identitas briixbroadband.
- **PostgreSQL**: dibutuhkan ekstensi `pgcrypto` untuk `gen_random_uuid()` (sudah diaktifkan
  otomatis oleh `migrations/0001_init.sql`).
- **CORS**: endpoint publik (`/vapid-public-key`, `/subscribe`, `/unsubscribe`) mengizinkan
  origin manapun karena memang dipanggil langsung dari browser app manapun yang terdaftar.
  `/send` dan `/admin/apps` tidak pernah dipanggil dari browser, jadi aman walau CORS longgar.
- **Push notification ke iOS Safari**: perlu PWA sudah di-"Add to Home Screen" dan versi iOS
  cukup baru. Untuk use case kritikal (mis. notifikasi order ke driver riwariwi), pertimbangkan
  native app + FCM/APNs di masa depan — backend `/send` di sini bisa diperluas untuk itu tanpa
  mengubah cara tiap app memanggilnya.

## Struktur project

```
cmd/server/main.go        - entrypoint, routing
internal/config/          - load env var
internal/db/              - koneksi Postgres + query (apps, subscriptions, notifications_log)
internal/push/            - generate VAPID key, kirim Web Push
internal/middleware/      - auth (X-API-Key per tenant, admin token)
internal/handlers/        - HTTP handlers
migrations/0001_init.sql  - skema database
examples/                 - contoh integrasi frontend & backend
```
