-- ---- 管理员账号与会话 ----

-- name: CreateAdminUser :one
INSERT INTO admin_users (username, password_hash)
VALUES ($1, $2)
RETURNING id, username, password_hash, totp_secret_enc, created_at;

-- name: GetAdminUserByUsername :one
SELECT id, username, password_hash, totp_secret_enc, created_at
FROM admin_users
WHERE username = $1;

-- name: CreateAdminSession :one
INSERT INTO admin_sessions (token_hash, admin_id, expires_at)
VALUES ($1, $2, $3)
RETURNING token_hash, admin_id, expires_at, created_at;

-- name: GetAdminSession :one
SELECT token_hash, admin_id, expires_at, created_at
FROM admin_sessions
WHERE token_hash = $1 AND expires_at > $2;

-- name: DeleteAdminSession :exec
DELETE FROM admin_sessions WHERE token_hash = $1;

-- name: DeleteExpiredAdminSessions :execrows
DELETE FROM admin_sessions WHERE expires_at < $1;

-- name: InsertAdminAudit :exec
INSERT INTO admin_audit_logs (admin_id, action, target, detail)
VALUES ($1, $2, $3, $4);

-- name: ListAdminAuditLogs :many
SELECT id, admin_id, action, target, detail, created_at
FROM admin_audit_logs
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(lim)::int;

-- ---- 举报 ----

-- name: ListReports :many
SELECT id, reporter_identity_id, reported_identity_id, conversation_id, reason, note, snapshot, status, handled_by, handled_at, created_at, target_type, target_id
FROM reports
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (
      sqlc.narg(cursor_created)::timestamptz IS NULL
      OR created_at < sqlc.narg(cursor_created)::timestamptz
      OR (created_at = sqlc.narg(cursor_created)::timestamptz AND id < sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim)::int;

-- name: GetReportByID :one
SELECT id, reporter_identity_id, reported_identity_id, conversation_id, reason, note, snapshot, status, handled_by, handled_at, created_at, target_type, target_id
FROM reports
WHERE id = $1;

-- name: ResolveReport :execrows
UPDATE reports
SET status = 'resolved', handled_by = $2, handled_at = now()
WHERE id = $1 AND status = 'open';

-- name: GetIdentityNicknameCreatedIP :one
SELECT nickname, created_ip, account_id FROM identities WHERE id = $1;

-- name: GetLastWSConnectIP :one
SELECT ip FROM access_logs
WHERE identity_id = $1 AND event = 'ws_connect' AND ip IS NOT NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: GetVerifiedMasked :one
SELECT phone_masked FROM verified_accounts WHERE id = $1;

-- name: CountReportsAgainstIdentity :one
SELECT count(*) FROM reports WHERE reported_identity_id = $1;

-- name: ListActiveBansForSubjects :many
SELECT id, subject_type, subject_value, reason, expires_at, created_by, revoked_at, created_at
FROM bans
WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $1)
  AND (
      (subject_type = 'identity' AND subject_value = $2)
      OR (subject_type = 'account' AND subject_value = $3)
      OR (subject_type = 'ip' AND subject_value = $4)
  )
ORDER BY created_at DESC;

-- ---- 封禁管理 ----

-- name: ListBans :many
SELECT id, subject_type, subject_value, reason, expires_at, created_by, revoked_at, created_at
FROM bans
WHERE (
    sqlc.narg(active)::boolean IS NULL
    OR (sqlc.narg(active)::boolean = true AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()))
    OR (sqlc.narg(active)::boolean = false AND NOT (revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())))
  )
  AND (
      sqlc.narg(cursor_created)::timestamptz IS NULL
      OR created_at < sqlc.narg(cursor_created)::timestamptz
      OR (created_at = sqlc.narg(cursor_created)::timestamptz AND id < sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim)::int;

-- ---- 统计 ----

-- name: CountActiveConversations :one
SELECT count(*) FROM conversations WHERE ended_at IS NULL AND kind <> 'room';

-- name: CountMessagesSince :one
SELECT count(*) FROM messages WHERE created_at > $1;

-- name: CountOpenReports :one
SELECT count(*) FROM reports WHERE status = 'open';
