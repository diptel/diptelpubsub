# PushHub — Notification Hub

Notification hub multi-tenant, multi-channel (**Push / Web Push VAPID**, **WhatsApp via
Fonnte**, **Email**) yang dipakai bersama oleh beberapa project: **riwariwi**,
**briixbroadband**, **briix capital / mifos mobile PWA**, **kasir**, dan project lain di masa
depan.

Satu service, satu database Postgres, kredensial provider (Fonnte token, akun SMTP) di level
**hub** (bukan per-tenant) — tiap app cukup panggil API ini dengan API key-nya sendiri untuk
mengirim notifikasi lewat channel apapun, tanpa perlu provider/akun sendiri-sendiri.

## Arsitektur singkat

```
Browser (PWA riwariwi/briixbroadband/dll)
   │  subscribe via PushManager
   ▼
PushHub  ── /vapid-public-key     (public, by app slug)
         ── /subscribe            (public, by app slug)
         ── /unsubscribe          (public, by app slug)
         ── /send                 (tenant-auth via X-API-Key)      ◄── dipanggil backend tiap app
         ── /send/wa              (tenant-auth via X-API-Key)      ◄── kirim WhatsApp via Fonnte
         ── /send/email           (tenant-auth via X-API-Key)      ◄── kirim email via SMTP hub
         ── /webhook/fonnte/{id}  (public — dipanggil Fonnte saat ada balasan WA masuk)
         ── /admin/apps, /admin/wa-numbers, ...  (admin-auth: session atau ADMIN_TOKEN)
         ── /panel/                (web panel: login + TOTP, kelola app/nomor WA/log)
   │
   ▼
Postgres (apps, subscriptions, wa_numbers, wa_messages, email_messages, admin_users, admin_sessions)
```

- **Frontend** tiap app memanggil `/vapid-public-key`, `/subscribe`, `/unsubscribe` langsung —
  tidak butuh API key (publik, diidentifikasi lewat `app` slug).
- **Backend** tiap app memanggil `/send`, `/send/wa`, atau `/send/email` dengan header
  `X-API-Key` miliknya sendiri saat mau mengirim notifikasi (order berubah status, promo, dll),
  lewat channel manapun yang paling pas.
- **WhatsApp (Fonnte)**: hub menyimpan 1-2 nomor WA (token Fonnte) secara terpusat, dikategorikan
  `otp` (dipakai khusus OTP/transaksional, terisolasi dari resiko blokir) atau `general`
  (notifikasi/promo/CS). Semua tenant berbagi nomor yang sama — pesan otomatis diberi prefix
  `[Nama App]` supaya penerima tahu bisnis mana yang mengirim. Pengiriman di-rate-limit per
  nomor (bukan per tenant) karena Fonnte memakai WhatsApp biasa, bukan WhatsApp Business API
  resmi, dan nomor bisa diblokir kalau kirim terlalu cepat/banyak.
- **Balasan WhatsApp masuk**: tiap nomor WA punya satu webhook URL (`/webhook/fonnte/{number_id}`)
  yang didaftarkan di dashboard Fonnte. Hub mencocokkan balasan ke tenant yang **terakhir**
  mengirim ke nomor HP tersebut, lalu meneruskannya ke `webhook_url` tenant itu (misal flow n8n)
  — kalau tidak ada tenant yang cocok, balasan tetap tercatat di log tapi tidak diteruskan.
- **Email**: hub memakai satu akun SMTP bersama (`SMTP_*` di `.env`), sama seperti nomor WA — tiap
  tenant kirim "dari" mailbox yang sama, subjek email diberi prefix `[Nama App]`.
- **n8n** (kalau dipakai) berperan di logika bisnis (auto-reply, broadcast, routing CS) —
  antrian/rate-limit/isolasi nomor tetap ditangani hub ini, bukan n8n.
- **Admin** (kamu) daftarkan app baru dan kelola nomor WA lewat **web panel di `/panel/`** (login
  username/password + kode authenticator), atau lewat `/admin/*` pakai `ADMIN_TOKEN` untuk
  automasi/curl.

## Menjalankan (deploy ke server, mis. 1Panel Pro)

1. Copy `.env.example` jadi `.env`, isi `POSTGRES_PASSWORD`, `ADMIN_TOKEN`, dan
   `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD` (akun admin pertama untuk login ke
   panel) dengan nilai yang kuat.
2. Jalankan:
   ```sh
   docker compose up -d --build
   ```
3. Cek service hidup:
   ```sh
   curl http://localhost:8080/healthz
   ```

Migrasi SQL di folder `migrations/` otomatis dijalankan **hanya saat volume database masih
kosong** (lewat `docker-entrypoint-initdb.d` milik image Postgres resmi). Untuk deployment yang
sudah pernah jalan sebelum tabel admin (`admin_users`, `admin_sessions`) ditambahkan, migrasi
baru harus dijalankan manual sekali:

```sh
docker compose exec -T postgres psql -U pushhub -d pushhub < migrations/0002_admin_auth.sql
```

Begitu juga untuk migrasi WhatsApp + Email (`wa_numbers`, `wa_messages`, `email_messages`,
kolom `webhook_url` di `apps`) — kalau database sudah pernah jalan sebelumnya, jalankan manual
sekali:

```sh
docker compose exec -T postgres psql -U pushhub -d pushhub < migrations/0003_notification_channels.sql
```

## Web panel admin (login username/password + TOTP)

Setelah service jalan, buka `http://<host>:<port>/panel/` di browser.

**Login pertama kali:**
1. Masukkan username & password dari `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD`.
2. Karena TOTP belum aktif, panel langsung menampilkan **QR code** — scan pakai Google
   Authenticator / Authy / 1Password (app apa saja yang support TOTP standar).
3. Masukkan kode 6 digit dari app tersebut untuk konfirmasi. Setelah ini, TOTP wajib dipakai
   setiap login berikutnya.

**Login berikutnya:** username + password + kode 6 digit dari authenticator — sama seperti 2FA
pada umumnya.

**Dari dashboard panel, kamu bisa:**
- **Tab Apps**: tambah app/tenant baru (slug, nama, VAPID subject, webhook WA masuk opsional)
  — API key langsung disalin ke clipboard. Lihat semua app beserta jumlah subscriber-nya,
  regenerate API key, dan set/ubah webhook WA masuk per app kapan saja.
- **Tab Nomor WA**: tambah nomor WA (label, token Fonnte, kategori `otp`/`general`, prioritas,
  min. interval kirim), aktif/nonaktifkan, hapus, dan salin URL webhook (`/webhook/fonnte/{id}`)
  untuk didaftarkan di dashboard Fonnte masing-masing nomor.
- **Tab Log WhatsApp / Log Email**: lihat 200 pesan WA (masuk & keluar) atau email terbaru
  lintas semua tenant beserta status kirimnya.

**Catatan keamanan:**
- `ADMIN_BOOTSTRAP_USERNAME`/`ADMIN_BOOTSTRAP_PASSWORD` di `.env` hanya dipakai **sekali** saat
  tabel admin masih kosong — setelah akun pertama berhasil dibuat, dua baris ini boleh dihapus
  dari `.env` (tidak akan dipakai lagi, dan tidak akan membuat akun kedua).
- Kalau PushHub nanti diakses lewat domain + HTTPS (bukan cuma IP LAN), set `COOKIE_SECURE=true`
  di `.env`. Selama masih akses lewat IP + HTTP biasa (seperti setup kamu sekarang), biarkan
  `false` — kalau diset `true` di HTTP biasa, browser akan menolak kirim cookie session dan
  login akan terasa gagal terus padahal password benar.
- `/send` dan endpoint `/admin/*` tidak pernah dipanggil dari frontend app lain, jadi aman walau
  server ini diakses dari LAN tanpa HTTPS untuk sementara.

## Auto-deploy saat push ke GitHub

Repo ini pakai **self-hosted GitHub Actions runner**: tiap push ke `main`, job deploy jalan
**langsung di server 1Panel kamu sendiri** (bukan di server GitHub) — jadi tidak perlu expose
SSH ke internet, tidak perlu IP publik, aman walau server di belakang IP lokal/firewall.

**Setup sekali di awal, di server (lewat Terminal 1Panel):**

1. Clone & jalankan seperti biasa dulu (lihat bagian "Menjalankan" di atas) — pastikan
   `docker compose up -d --build` sudah pernah berhasil di `/opt/1panel/docker/compose/diptelpubsub`.

2. Install GitHub Actions runner. Di GitHub: buka repo **diptelpubsub** → **Settings → Actions
   → Runners → New self-hosted runner** → pilih **Linux**, **x64**. GitHub akan kasih 4 perintah
   persis seperti ini (token-nya unik, beda tiap kali, copy dari halaman itu):
   ```sh
   mkdir ~/actions-runner && cd ~/actions-runner
   curl -o actions-runner-linux-x64.tar.gz -L https://github.com/actions/runner/releases/download/vX.X.X/actions-runner-linux-x64-X.X.X.tar.gz
   tar xzf ./actions-runner-linux-x64.tar.gz
   ./config.sh --url https://github.com/diptel/diptelpubsub --token <TOKEN_DARI_GITHUB>
   ```
   Saat `./config.sh` tanya-tanya, tekan Enter saja untuk semua pertanyaan (pakai default) —
   nama runner dan label boleh default.

3. Install sebagai service, biar tetap jalan walau server restart:
   ```sh
   sudo ./svc.sh install
   sudo ./svc.sh start
   ```

4. Cek statusnya di GitHub: **Settings → Actions → Runners** — harus muncul status hijau "Idle".

Setelah ini, **tiap push ke `main`, server kamu sendiri yang otomatis `git pull` +
`docker compose up -d --build`** — bisa dipantau di tab **Actions** repo ini. Tidak ada secret
SSH yang perlu diisi sama sekali untuk jalur ini.

Kalau folder project-nya bukan di `/opt/1panel/docker/compose/diptelpubsub`, ubah nilai
`DEPLOY_PATH` di `.github/workflows/deploy.yml`.

## Mendaftarkan app baru (tenant)

**Cara termudah: lewat web panel** (`/panel/` → tombol "+ Tambah App") — lihat bagian di atas.

**Cara lewat API** (untuk automasi/CI, pakai `ADMIN_TOKEN`), tiap project (riwariwi,
briixbroadband, briix-capital, kasir, ...) didaftarkan sekali:

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
- `examples/backend-send-go.go` — contoh kirim push notification dari backend Go.
- `examples/backend-send-curl.sh` — contoh kirim push lewat curl / n8n HTTP Request node.
- `examples/backend-send-wa-go.go` — contoh kirim WhatsApp (`/send/wa`) dari backend Go, termasuk
  pembedaan kategori `otp` vs `general`.
- `examples/backend-send-wa-curl.sh` — contoh kirim WhatsApp lewat curl / n8n HTTP Request node.

Alur integrasi tiap app baru:

1. Daftar app lewat `/admin/apps` (atau panel) → dapat `api_key`. Satu `api_key` ini dipakai
   untuk ketiga channel (Push, WhatsApp, Email) — tidak perlu daftar/token terpisah per channel.
2. **Push (opsional)**: pasang `examples/service-worker.js` sebagai `/sw.js`, lalu panggil
   `enablePushNotifications()` dari `examples/frontend-subscribe.js` saat user mengaktifkan
   notifikasi (ganti `APP_SLUG` dan `PUSHHUB_URL`). Di backend, panggil `POST /send` dengan
   `X-API-Key` saat ada event yang perlu dikirim (lihat `examples/backend-send-go.go`).
3. **WhatsApp (opsional)**: langsung panggil `POST /send/wa` dengan `X-API-Key` dari backend
   app, tidak perlu setup frontend apapun (lihat `examples/backend-send-wa-go.go` /
   `backend-send-wa-curl.sh`). Pakai `"category": "otp"` untuk kode verifikasi/transaksional,
   `"category": "general"` untuk notifikasi order/promo/CS. Kalau mau balasan WA pelanggan
   diteruskan ke sistem app ini (misal flow n8n), set `webhook_url` app ini lewat tab "Apps" di
   panel atau `POST /admin/apps/<slug>/webhook`.
4. **Email (opsional)**: panggil `POST /send/email` dengan `X-API-Key`, sama seperti WhatsApp —
   tidak butuh setup SMTP sendiri, memakai akun SMTP hub.

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

### `POST /send/wa`
Butuh header `X-API-Key: <api_key milik app>`. Body:
```json
{ "phone": "6281234567890", "message": "...", "category": "otp" }
```
`category` opsional, default `"general"`. `"otp"` akan dirutekan ke nomor WA yang dikategorikan
khusus OTP (lihat tab "Nomor WA" di panel); kalau tidak ada nomor aktif untuk kategori tersebut,
jatuh ke nomor aktif manapun. Pesan otomatis diberi prefix `[Nama App]` kalau belum ada. Respons:
```json
{ "status": "sent", "wa_number": "WA Utama OTP" }
```

### `POST /send/email`
Butuh header `X-API-Key: <api_key milik app>`. Body:
```json
{ "to": "user@example.com", "subject": "...", "body": "...", "html": false }
```
Dikirim lewat akun SMTP hub (`SMTP_*` di `.env`); kalau belum dikonfigurasi, endpoint ini
mengembalikan error yang jelas. Subjek otomatis diberi prefix `[Nama App]`.

### `POST /webhook/fonnte/{number_id}`
Publik (tidak butuh auth — `number_id` di path adalah UUID acak yang berfungsi sebagai secret-nya
sendiri). Dipanggil oleh Fonnte sendiri saat ada balasan WhatsApp masuk ke salah satu nomor hub.
Hub mencocokkan ke tenant yang terakhir mengirim ke nomor HP tersebut lewat nomor WA ini, lalu
meneruskan `{ "app": "<slug>", "phone": "...", "message": "..." }` ke `webhook_url` tenant itu
kalau ada. Balasan yang tidak cocok ke tenant manapun tetap tercatat di log (tab "Log WhatsApp").

### Endpoint admin (butuh session panel yang sudah login, ATAU header `Authorization: Bearer <ADMIN_TOKEN>`)

- `GET /admin/apps` — daftar semua app terdaftar (termasuk API key, jumlah subscriber, webhook
  WA masuk).
- `POST /admin/apps` — daftarkan app/tenant baru, generate VAPID keypair dan API key baru. Body:
  ```json
  { "slug": "riwariwi", "name": "Riwariwi", "vapid_subject": "mailto:fika@briix.co.id", "webhook_url": "https://n8n.example.com/webhook/riwariwi-wa" }
  ```
  `webhook_url` opsional.
- `POST /admin/apps/<slug>/regenerate-key` — ganti API key suatu app (key lama langsung tidak
  berlaku).
- `POST /admin/apps/<slug>/webhook` — set/ubah/hapus webhook WA masuk suatu app. Body:
  `{ "webhook_url": "https://..." }` (string kosong untuk menghapus).
- `GET /admin/wa-numbers` — daftar nomor WA terdaftar (token Fonnte tidak pernah dikembalikan di
  response ini).
- `POST /admin/wa-numbers` — tambah nomor WA baru. Body:
  ```json
  { "label": "WA Utama OTP", "phone_number": "6281234567890", "fonnte_token": "...", "category": "otp", "priority": 100, "min_interval_ms": 2000 }
  ```
- `POST /admin/wa-numbers/<id>/toggle` — aktif/nonaktifkan nomor tanpa menghapus riwayatnya.
  Body: `{ "active": false }`.
- `DELETE /admin/wa-numbers/<id>` — hapus nomor WA.
- `GET /admin/wa-messages` — 200 pesan WhatsApp terbaru (masuk & keluar, lintas semua tenant).
- `GET /admin/email-messages` — 200 email terbaru (lintas semua tenant).

### Endpoint login panel (publik — ini memang cara otentikasi)

- `POST /admin/login` — body `{ "username", "password", "totp_code"? }`. Kalau TOTP belum
  diaktifkan, respons berisi QR code (`qr_png_base64`) untuk enrollment, bukan sesi penuh.
- `POST /admin/totp/confirm` — body `{ "code" }`. Konfirmasi kode pertama untuk mengaktifkan TOTP.
- `POST /admin/logout` — hapus sesi.
- `GET /admin/whoami` — butuh sesi aktif; dipakai panel untuk cek status login.

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

## Catatan teknis — channel WhatsApp & Email

- **Nomor WA & akun SMTP di level hub, bukan per-tenant** — ini keputusan arsitektur yang
  disengaja: hanya perlu 1-2 token Fonnte dan 1 akun SMTP untuk seluruh bisnis, dikelola sekali
  lewat panel, bukan tiap app bikin akun Fonnte/SMTP sendiri.
- **Rate limit per nomor WA**, bukan per tenant/app — kalau beberapa app kirim ke nomor yang
  sama bersamaan, kiriman diantre (serial) per nomor; nomor lain tidak terpengaruh.
- **Isolasi kategori OTP vs general** mencegah nomor OTP ikut kena resiko blokir akibat trafik
  promo/broadcast — kalau tidak ada nomor aktif untuk kategori yang diminta, fallback ke nomor
  aktif manapun (supaya kirim tidak gagal total hanya karena belum ada nomor khusus).
- **Routing balasan WA masuk** memakai "tenant terakhir yang mengirim ke nomor HP ini", bukan
  daftar statis — jadi tidak perlu mapping manual nomor HP pelanggan ke tenant.

## Struktur project

```
cmd/server/main.go           - entrypoint, routing, bootstrap akun admin pertama
internal/config/             - load env var
internal/db/                 - koneksi Postgres + query (apps, subscriptions, wa_numbers, wa_messages, email_messages, admin_users, admin_sessions)
internal/push/                - generate VAPID key, kirim Web Push
internal/wa/                   - kirim WhatsApp via Fonnte + rate limiter per nomor
internal/emailsend/             - kirim email via SMTP (net/smtp stdlib)
internal/auth/                - bcrypt password hashing, generate/validasi TOTP, render QR code
internal/middleware/         - auth (X-API-Key per tenant, admin token, session cookie panel)
internal/handlers/           - HTTP handlers (push, wa, email, wa_webhook, admin, wa_admin)
internal/panel/               - web panel admin (HTML/CSS/JS di-embed ke binary Go)
migrations/0001_init.sql     - skema database inti
migrations/0002_admin_auth.sql - skema admin_users & admin_sessions
migrations/0003_notification_channels.sql - skema wa_numbers, wa_messages, email_messages, apps.webhook_url
examples/                    - contoh integrasi frontend & backend
```
