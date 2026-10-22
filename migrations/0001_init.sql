-- PushHub: multi-tenant Web Push (VAPID) notification service
-- Tables: apps (tenants), subscriptions (browser push endpoints), notifications_log

CREATE TABLE IF NOT EXISTS apps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT NOT NULL UNIQUE,         -- e.g. 'riwariwi', 'briixbroadband', 'briix-capital', 'kasir'
    name            TEXT NOT NULL,
    api_key         TEXT NOT NULL UNIQUE,          -- used by the app's own backend to call /send
    vapid_public_key  TEXT NOT NULL,
    vapid_private_key TEXT NOT NULL,                -- stored as-is; service is meant to run on a trusted private server
    vapid_subject   TEXT NOT NULL,                   -- mailto: or https: contact required by Web Push spec
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    user_id         TEXT,                           -- optional external user id from the calling app, for targeted sends
    endpoint        TEXT NOT NULL,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_id, endpoint)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_app_user ON subscriptions (app_id, user_id);

CREATE TABLE IF NOT EXISTS notifications_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    subscription_id UUID REFERENCES subscriptions(id) ON DELETE SET NULL,
    title           TEXT,
    body            TEXT,
    payload         JSONB,
    status          TEXT NOT NULL,                  -- 'sent' | 'failed' | 'expired'
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notifications_log_app_created ON notifications_log (app_id, created_at DESC);

CREATE EXTENSION IF NOT EXISTS pgcrypto;
