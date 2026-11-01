-- CS Hub: satu nomor WA khusus percakapan bantuan untuk semua tenant, terpisah dari nomor
-- notifikasi (otp/general) yang tidak lagi dibalas. Lihat catatan "Rencana disepakati 1 Nov 2026"
-- di dokumen status project untuk latar belakangnya.

-- Nomor dengan category = 'cs' adalah nomor percakapan bantuan (dibalas oleh bot + staf CS).
-- Nomor category 'otp'/'general' tetap murni kirim notifikasi (lihat wa_auto_reply_log di bawah).
-- (Tidak ada migrasi skema untuk ini — 'category' di wa_numbers sudah TEXT bebas, cukup pakai
-- nilai baru 'cs' saat menambah nomor lewat panel/admin API.)

-- Tenant mana saja yang muncul di menu CS, dan bagaimana bot mengenali nomor transaksi mereka
-- tanpa harus bertanya (misal "KLR-123" otomatis terdeteksi sebagai Kliro).
ALTER TABLE apps ADD COLUMN IF NOT EXISTS cs_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS cs_menu_label TEXT; -- nama tampil di menu bot, fallback ke apps.name
ALTER TABLE apps ADD COLUMN IF NOT EXISTS cs_ref_prefix TEXT; -- mis. 'KLR-', 'RW-' — dicocokkan case-insensitive di awal token pesan

-- Satu sesi percakapan CS per (nomor CS, nomor HP pelanggan). "awaiting_service" = belum pilih
-- layanan (bot menampilkan menu), "open" = sudah terhubung ke satu tenant, "closed" = selesai
-- (manual oleh staf, atau otomatis setelah tidak ada pesan >24 jam — dicek secara malas, tanpa
-- cron, lihat kode Go).
CREATE TABLE IF NOT EXISTS cs_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wa_number_id    UUID NOT NULL REFERENCES wa_numbers(id) ON DELETE CASCADE,
    phone           TEXT NOT NULL,
    app_id          UUID REFERENCES apps(id) ON DELETE SET NULL, -- NULL selama status 'awaiting_service'
    status          TEXT NOT NULL DEFAULT 'awaiting_service',    -- 'awaiting_service' | 'open' | 'closed'
    pending_menu    JSONB,                                        -- urutan app_id yang ditampilkan di menu terakhir
    rating          INT,                                          -- opsional, 1-5, diisi saat sesi ditutup
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_cs_sessions_number_phone ON cs_sessions (wa_number_id, phone, status);
CREATE INDEX IF NOT EXISTS idx_cs_sessions_app_status ON cs_sessions (app_id, status, last_message_at DESC);

-- Isi percakapan CS (terpisah dari wa_messages supaya log notifikasi & percakapan CS tidak
-- bercampur di tampilan admin, meski keduanya lewat Fonnte/nomor WA yang sama secara teknis).
CREATE TABLE IF NOT EXISTS cs_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES cs_sessions(id) ON DELETE CASCADE,
    direction       TEXT NOT NULL,          -- 'in' (dari pelanggan) | 'out' (bot/staf)
    sender_label    TEXT,                   -- NULL untuk bot otomatis; nama/username staf saat balas manual dari panel
    message         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cs_messages_session_created ON cs_messages (session_id, created_at);

-- Throttle balasan otomatis "nomor ini khusus notifikasi" di nomor otp/general, supaya tidak
-- membalas setiap pesan (misal pelanggan bilang "ok" / "makasih" berkali-kali).
CREATE TABLE IF NOT EXISTS wa_auto_reply_log (
    wa_number_id    UUID NOT NULL REFERENCES wa_numbers(id) ON DELETE CASCADE,
    phone           TEXT NOT NULL,
    last_sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (wa_number_id, phone)
);
