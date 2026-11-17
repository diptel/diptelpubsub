-- Pengaturan level-hub (bukan per-tenant) yang perlu bisa diubah staf dari panel tanpa redeploy,
-- misalnya instruksi tambahan kepribadian/gaya bicara AiSyah. Lihat internal/handlers/cs_ai.go
-- (ai_persona) dan GetAISettings/UpdateAISettings di internal/handlers/cs_admin.go.
CREATE TABLE IF NOT EXISTS hub_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
