-- ---- 封禁 ----

-- name: InsertBan :one
INSERT INTO bans (subject_type, subject_value, reason, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, subject_type, subject_value, reason, expires_at, created_by, revoked_at, created_at;

-- name: GetBan :one
SELECT id, subject_type, subject_value, reason, expires_at, created_by, revoked_at, created_at
FROM bans
WHERE id = $1;

-- name: RevokeBan :execrows
UPDATE bans SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: GetActiveBan :one
SELECT id, subject_type, subject_value, reason, expires_at, created_by, revoked_at, created_at
FROM bans
WHERE subject_type = $1 AND subject_value = $2 AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > $3)
ORDER BY created_at DESC
LIMIT 1;

-- ---- 举报 ----

-- name: InsertReport :one
INSERT INTO reports (reporter_identity_id, reported_identity_id, conversation_id, reason, note, snapshot)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: GetMessageForReport :one
SELECT id, conversation_id, sender_member_id, sender_identity_id, body, created_at, burned_at
FROM messages
WHERE id = $1 AND conversation_id = $2;

-- name: ListReportContextBefore :many
SELECT m.id, m.sender_member_id, i.nickname, m.body, m.created_at, m.burned_at
FROM messages m
JOIN identities i ON i.id = m.sender_identity_id
WHERE m.conversation_id = $1 AND m.id <= $2
ORDER BY m.id DESC
LIMIT 11;

-- name: ListReportContextAfter :many
SELECT m.id, m.sender_member_id, i.nickname, m.body, m.created_at, m.burned_at
FROM messages m
JOIN identities i ON i.id = m.sender_identity_id
WHERE m.conversation_id = $1 AND m.id > $2
ORDER BY m.id ASC
LIMIT 10;
