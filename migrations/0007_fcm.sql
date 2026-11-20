-- Kanal notifikasi Android (FCM) — umum untuk semua tenant PushHub, bukan cuma riwariwi, karena
-- Kliro/BRIIX Pay dan lini BRIIX lain juga akan punya aplikasi Android sendiri. Lihat kontrak
-- lengkap di project doc claude/riwariwi-android-fcm-kontrak-pushhub.md bagian 3, dan
-- internal/fcmassist untuk implementasinya.

-- Kredensial Firebase per tenant: path berkas service-account JSON di filesystem server (dalam
-- container: /app/secrets/..., lihat bind mount "secrets" di docker-compose.yml). Diisi lewat
-- POST /admin/apps/{slug}/fcm, bukan disimpan sebagai isi JSON di DB. Beberapa tenant boleh
-- menunjuk ke berkas yang sama kalau memakai proyek Firebase yang sama (mis. briix--project).
-- NULL/kosong berarti tenant itu belum punya FCM — pendaftaran kind "fcm" ditolak, Web Push tetap
-- jalan seperti biasa.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS fcm_credentials_path TEXT;

-- Aturan pemetaan channel_id + priority Android per tenant: array JSON, urutan menentukan (yang
-- pertama cocok dipakai), lihat internal/fcmassist/rules.go untuk bentuk & evaluasi aturan
-- lengkap. Default '[]' berarti semua notifikasi tenant itu jatuh ke channel "info"/priority
-- normal sampai diisi lewat POST /admin/apps/{slug}/fcm.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS fcm_channel_rules JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Token FCM per perangkat Android. Upsert berdasarkan (app_id, fcm_token) — bukan berdasarkan
-- user_id — karena token yang sama didaftarkan ulang setiap aplikasi dibuka, dan user_id bisa
-- berganti saat akun lain login di HP yang sama (kontrak riwariwi bagian 3).
CREATE TABLE IF NOT EXISTS fcm_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    user_id      TEXT,
    fcm_token    TEXT NOT NULL,
    device_id    TEXT,
    client_app   TEXT, -- "driver" | "mitra" | "app" | "kasir" | "admin" | NULL, lihat kontrak
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_id, fcm_token)
);
CREATE INDEX IF NOT EXISTS idx_fcm_tokens_app_user ON fcm_tokens (app_id, user_id);

-- Aturan channel riwariwi, dipasang sebagai aturan tenant riwariwi (bukan ditanam di kode) —
-- persis kontrak yang sudah dipakai riwariwi di produksi. No-op kalau tenant "riwariwi" belum ada
-- di instance ini (aman dijalankan ulang/di environment lain).
UPDATE apps SET fcm_channel_rules = '[
    {"url_prefix": "/driver", "title_prefix": "Order", "channel_id": "order", "priority": "high"},
    {"url_prefix": "/mitra", "title_prefix": "Pesanan baru", "channel_id": "order", "priority": "high"},
    {"url_contains": "/chat/", "channel_id": "chat"}
]'::jsonb
WHERE slug = 'riwariwi' AND fcm_channel_rules = '[]'::jsonb;
