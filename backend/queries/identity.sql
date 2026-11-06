-- name: GetIdentityByTokenHash :one
SELECT id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at
FROM identities
WHERE token_hash = $1;

-- name: CreateIdentity :one
INSERT INTO identities (token_hash, nickname, avatar_seed, created_ip)
VALUES ($1, $2, $3, $4)
RETURNING id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at;

-- name: RevokeIdentity :exec
UPDATE identities SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: TouchIdentityLastSeen :exec
UPDATE identities SET last_seen_at = now() WHERE id = $1;

-- name: InsertAccessLog :exec
INSERT INTO access_logs (identity_id, event, ip, user_agent)
VALUES ($1, $2, $3, $4);
