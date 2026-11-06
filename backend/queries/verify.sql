-- name: InsertSmsCode :one
INSERT INTO sms_codes (phone_hmac, code_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, phone_hmac, code_hash, attempts, expires_at, consumed_at, created_at;

-- name: GetLatestSmsCode :one
SELECT id, phone_hmac, code_hash, attempts, expires_at, consumed_at, created_at
FROM sms_codes
WHERE phone_hmac = $1 AND consumed_at IS NULL
ORDER BY id DESC
LIMIT 1;

-- name: ConsumeSmsAttempt :one
-- 原子地占用一次尝试机会：仅当未被消费、未过期且尝试次数未达上限时才自增。
-- 未返回行表示验证码已过期、已消费或尝试次数已用尽。
UPDATE sms_codes
SET attempts = attempts + 1
WHERE id = $1 AND consumed_at IS NULL AND attempts < $2 AND expires_at > now()
RETURNING id, phone_hmac, code_hash, attempts, expires_at, consumed_at, created_at;

-- name: ConsumeSmsCode :execrows
-- 消费验证码：仅当尚未被消费时置为已消费，用影响行数保证同一验证码只能成功一次。
UPDATE sms_codes SET consumed_at = now() WHERE id = $1 AND consumed_at IS NULL;

-- name: UpsertVerifiedAccount :one
INSERT INTO verified_accounts (phone_hmac, phone_enc, phone_masked)
VALUES ($1, $2, $3)
ON CONFLICT (phone_hmac) DO UPDATE SET phone_masked = EXCLUDED.phone_masked
RETURNING id, phone_hmac, phone_enc, phone_masked, created_at;

-- name: SetIdentityAccount :exec
UPDATE identities SET account_id = $2 WHERE id = $1;
