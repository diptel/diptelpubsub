-- CS Hub AI assistant ("AiSyah" by default, lihat AI_ASSISTANT_NAME): setiap sesi baru dimulai
-- dijawab AI (handled_by default 'ai'). Staf bisa mengambil alih dari panel ("Tangani"), setelah
-- itu AI berhenti menjawab di sesi tersebut (dicek di internal/handlers/cs_ai.go) sampai sesi
-- ditutup. Komplain baru selalu membuka sesi baru, yang otomatis kembali ke handled_by='ai' lewat
-- default kolom ini — tidak perlu logika tambahan untuk "AI aktif lagi".
ALTER TABLE cs_sessions ADD COLUMN IF NOT EXISTS handled_by TEXT NOT NULL DEFAULT 'ai'; -- 'ai' | 'staff'
ALTER TABLE cs_sessions ADD COLUMN IF NOT EXISTS handled_by_label TEXT; -- username staf yang klik "Tangani"
ALTER TABLE cs_sessions ADD COLUMN IF NOT EXISTS taken_over_at TIMESTAMPTZ;
