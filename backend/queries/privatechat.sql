-- ---- 私密链接 ----

-- name: InsertPrivateLink :one
INSERT INTO private_links (token_hash, conversation_id, creator_identity_id, password_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, token_hash, conversation_id, creator_identity_id, password_hash, expires_at, destroyed_at, created_at;

-- name: GetPrivateLinkByTokenHash :one
SELECT id, token_hash, conversation_id, creator_identity_id, password_hash, expires_at, destroyed_at, created_at
FROM private_links
WHERE token_hash = $1;

-- name: LockPrivateLinkByTokenHash :one
SELECT id, token_hash, conversation_id, creator_identity_id, password_hash, expires_at, destroyed_at, created_at
FROM private_links
WHERE token_hash = $1
FOR UPDATE;

-- name: SetPrivateLinkDestroyed :execrows
UPDATE private_links SET destroyed_at = now() WHERE id = $1 AND destroyed_at IS NULL;

-- name: CountActiveMembers :one
SELECT count(*) FROM conversation_members
WHERE conversation_id = $1 AND left_at IS NULL;

-- name: GetPrivateLinkDestroyedByConversation :one
SELECT destroyed_at FROM private_links WHERE conversation_id = $1;

-- name: ListExpiredPrivateLinks :many
SELECT pl.id, pl.conversation_id
FROM private_links pl
JOIN conversations c ON c.id = pl.conversation_id
WHERE c.ended_at IS NULL AND pl.destroyed_at IS NULL AND pl.expires_at < $1;
