-- Adds WhatsApp (Fonnte) and Email channels, turning PushHub into a multi-channel
-- "Notification Hub": Push (existing) + WhatsApp + Email, shared across tenants.

-- Each app/tenant can optionally have a webhook URL that incoming WhatsApp replies
-- (matched to the tenant that last messaged that phone number) are forwarded to,
-- e.g. an n8n webhook that handles CS chat / auto-reply flows.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS webhook_url TEXT;

-- Hub-level WhatsApp sender numbers. Tokens live here (not per-tenant) — all tenants
-- share 1-2 Fonnte-connected numbers. "category" routes messages: 'otp' numbers are
-- used for OTP/transactional messages, 'general' for notifications/promo/CS, so a
-- block on the promo number never breaks login for any tenant.
CREATE TABLE IF NOT EXISTS wa_numbers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    label           TEXT NOT NULL,                 -- e.g. "OTP utama", "Notifikasi & CS"
    phone_number    TEXT,                           -- display only, e.g. "+62812xxxxxxx"
    fonnte_token    TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT 'general', -- 'otp' | 'general'
    priority        INT NOT NULL DEFAULT 100,        -- lower = tried first within a category
    active          BOOLEAN NOT NULL DEFAULT true,
    min_interval_ms INT NOT NULL DEFAULT 2000,        -- rate limit: minimum gap between sends on this number
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wa_numbers_category_active ON wa_numbers (category, active, priority);

-- Every outbound WhatsApp send, for logging, debugging, and to let incoming replies be
-- matched back to "which tenant last messaged this phone number".
CREATE TABLE IF NOT EXISTS wa_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID REFERENCES apps(id) ON DELETE CASCADE, -- nullable: an incoming reply we can't match to any tenant is still logged
    wa_number_id    UUID REFERENCES wa_numbers(id) ON DELETE SET NULL,
    direction       TEXT NOT NULL DEFAULT 'out',    -- 'out' | 'in'
    phone           TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT 'general',
    message         TEXT,
    status          TEXT NOT NULL,                  -- 'sent' | 'failed'
    error           TEXT,
    provider_response JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wa_messages_app_created ON wa_messages (app_id, created_at DESC);
-- Used to match an incoming reply (by phone + number) to the tenant that last texted it.
CREATE INDEX IF NOT EXISTS idx_wa_messages_number_phone_created ON wa_messages (wa_number_id, phone, created_at DESC);

-- Every outbound email, for logging/debugging. SMTP credentials are hub-level config
-- (env vars), not stored per-tenant — same shared-infrastructure model as WhatsApp.
CREATE TABLE IF NOT EXISTS email_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    to_address      TEXT NOT NULL,
    subject         TEXT,
    status          TEXT NOT NULL,                  -- 'sent' | 'failed'
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_email_messages_app_created ON email_messages (app_id, created_at DESC);
