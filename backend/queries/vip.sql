-- ---- 激活码 ----

-- name: CreateActivationBatch :one
INSERT INTO activation_batches (label, vip_days, broadcast_cards, code_count, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, label, vip_days, broadcast_cards, code_count, expires_at, created_by, created_at;

-- name: InsertActivationCode :exec
INSERT INTO activation_codes (code_hash, batch_id) VALUES ($1, $2);

-- name: ListActivationBatches :many
SELECT b.id, b.label, b.vip_days, b.broadcast_cards, b.code_count, b.expires_at, b.created_by, b.created_at,
       COALESCE((SELECT count(*) FROM activation_codes c WHERE c.batch_id = b.id), 0)::bigint AS total,
       COALESCE((SELECT count(*) FROM activation_codes c WHERE c.batch_id = b.id AND c.redeemed_at IS NOT NULL), 0)::bigint AS redeemed,
       COALESCE((SELECT count(*) FROM activation_codes c WHERE c.batch_id = b.id AND c.revoked_at IS NOT NULL), 0)::bigint AS revoked
FROM activation_batches b
ORDER BY b.created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: GetActivationBatch :one
SELECT id, label, vip_days, broadcast_cards, code_count, expires_at, created_by, created_at
FROM activation_batches
WHERE id = $1;

-- name: LockActivationCode :one
SELECT code_hash, batch_id, redeemed_by, redeemed_at, revoked_at
FROM activation_codes
WHERE code_hash = $1
FOR UPDATE;

-- name: RedeemActivationCode :execrows
UPDATE activation_codes
SET redeemed_by = $2, redeemed_at = now()
WHERE code_hash = $1 AND redeemed_at IS NULL AND revoked_at IS NULL;

-- name: RevokeBatchCodes :execrows
UPDATE activation_codes SET revoked_at = now()
WHERE batch_id = $1 AND redeemed_at IS NULL AND revoked_at IS NULL;
