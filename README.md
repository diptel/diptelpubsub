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
PushHub  ── /vapid-public-key     (public, by app slug)
         ── /subscribe            (public, by app slug)
         ── /unsubscribe          (public, by app slug)
         ── /send                 (tenant-auth via X-API-Key)      ◄── dipanggil backend tiap app
         ── /admin/apps           (admin-auth: session atau ADMIN_TOKEN)
         ── /panel/                (web panel: login + TOTP, kelola app)
   │
   ▼
Postgres (apps, subscriptions, notifications_log, admin_users, admin_sessions)
```

- **Frontend** tiap app memanggil `/vapid-public-key`, `/subscribe`, `/unsubscribe` langsung —
  tidak butuh API key (publik, diidentifikasi lewat `app` slug).
- **Backend** tiap app memanggil `/send` dengan header `X-API-Key` miliknya sendiri saat mau
  mengirim notifikasi (order berubah status, promo, dll).
- **Admin** (kamu) daftarkan app baru lewat **web panel di `/panel/`** (login username/password
  + kode authenticator), atau lewat `/admin/apps` pakai `ADMIN_TOKEN` untuk automasi/curl.

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
- Tambah app/tenant baru (slug, nama, VAPID subject) — API key langsung disalin ke clipboard.
- Lihat semua app yang terdaftar beserta jumlah subscriber-nya.
- Regenerate API key suatu app (kalau key lama bocor/perlu diganti).

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

Repo ini sudah punya workflow `.github/workflows/deploy.yml` yang otomatis SSH ke server dan
menjalankan `git pull` + `docker compose up -d --build` tiap ada push ke branch `main`. Supaya
aktif, tambahkan 5 secrets ini di GitHub repo:
**Settings → Secrets and variables → Actions → New repository secret**

| Secret            | Isi                                                              |
|-------------------|-------------------------------------------------------------------|
| `DEPLOY_HOST`     | IP atau domain server 1Panel kamu                                  |
| `DEPLOY_USER`     | user SSH di server (mis. `root` atau user khusus deploy)           |
| `DEPLOY_SSH_KEY`  | private key SSH (format PEM) yang public key-nya sudah ditaruh di `~/.ssh/authorized_keys` server |
| `DEPLOY_PORT`     | port SSH server, biasanya `22`                                     |
| `DEPLOY_PATH`     | path folder project di server, mis. `/opt/diptelpubsub`             |

Langkah sekali di awal, di server:

```sh
mkdir -p /opt/diptelpubsub
cd /opt/diptelpubsub
git clone https://github.com/diptel/diptelpubsub.git .
cp .env.example .env   # isi POSTGRES_PASSWORD & ADMIN_TOKEN
docker compose up -d --build
```

Setelah itu, **tiap kali ada perubahan kode di-push ke `main`, GitHub Actions otomatis login ke
server dan redeploy** — tidak perlu SSH manual lagi. Status jalannya bisa dicek di tab
**Actions** repo ini.

Kalau mau generate SSH key khusus untuk deploy (dipisah dari key pribadi), di server:
```sh
ssh-keygen -t ed25519 -C "github-actions-deploy" -f ~/.ssh/github_deploy -N ""
cat ~/.ssh/github_deploy.pub >> ~/.ssh/authorized_keys
cat ~/.ssh/github_deploy          # isi ini ke secret DEPLOY_SSH_KEY
```

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

### Endpoint admin (butuh session panel yang sudah login, ATAU header `Authorization: Bearer <ADMIN_TOKEN>`)

- `GET /admin/apps` — daftar semua app terdaftar (termasuk API key & jumlah subscriber).
- `POST /admin/apps` — daftarkan app/tenant baru, generate VAPID keypair dan API key baru. Body:
  ```json
  { "slug": "riwariwi", "name": "Riwariwi", "vapid_subject": "mailto:fika@briix.co.id" }
  ```
- `POST /admin/apps/<slug>/regenerate-key` — ganti API key suatu app (key lama langsung tidak
  berlaku).

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

## Struktur project

```
cmd/server/main.go          - entrypoint, routing, bootstrap akun admin pertama
internal/config/            - load env var
internal/db/                - koneksi Postgres + query (apps, subscriptions, notifications_log, admin_users, admin_sessions)
internal/push/               - generate VAPID key, kirim Web Push
internal/auth/                - bcrypt password hashing, generate/validasi TOTP, render QR code
internal/middleware/         - auth (X-API-Key per tenant, admin token, session cookie panel)
internal/handlers/           - HTTP handlers
internal/panel/               - web panel admin (HTML/CSS/JS di-embed ke binary Go)
migrations/0001_init.sql     - skema database inti
migrations/0002_admin_auth.sql - skema admin_users & admin_sessions
examples/                    - contoh integrasi frontend & backend
```
