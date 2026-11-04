-- 5 Nov 2026: GenerateVAPIDKeys dulu menyimpan kunci tertukar (kunci rahasia 32 byte di kolom publik).
-- Tukar balik baris yang kunci publiknya 43 karakter (= 32 byte base64url; kunci publik yang benar 87 karakter).
-- Aman diulang: baris yang sudah benar tidak tersentuh.
UPDATE apps SET vapid_public_key = vapid_private_key, vapid_private_key = vapid_public_key
WHERE length(rtrim(vapid_public_key, '=')) = 43 AND length(rtrim(vapid_private_key, '=')) = 87;
