-- Admin web panel auth: username/password + TOTP (authenticator app), session cookies.

CREATE TABLE IF NOT EXISTS admin_users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    totp_secret     TEXT,                       -- base32 secret; set on first login, confirmed via /admin/totp/confirm
    totp_enabled    BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    token           TEXT PRIMARY KEY,            -- random session token, stored directly as the cookie value
    admin_user_id   UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    pending_totp    BOOLEAN NOT NULL DEFAULT false, -- true = awaiting TOTP enrollment confirmation, limited access
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_user ON admin_sessions (admin_user_id);
