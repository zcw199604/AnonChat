-- ---- 公告 ----

-- name: ListActiveAnnouncements :many
SELECT id, content, starts_at, ends_at, created_by, created_at
FROM announcements
WHERE deleted_at IS NULL
  AND starts_at <= now()
  AND (ends_at IS NULL OR ends_at > now())
ORDER BY created_at DESC
LIMIT $1;

-- name: ListAdminAnnouncements :many
SELECT id, content, starts_at, ends_at, created_by, created_at
FROM announcements
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: CreateAnnouncement :one
INSERT INTO announcements (content, ends_at, created_by)
VALUES ($1, $2, $3)
RETURNING id, content, starts_at, ends_at, created_by, created_at;

-- name: SoftDeleteAnnouncement :execrows
UPDATE announcements SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- ---- 广播 ----

-- name: CreateBroadcast :one
INSERT INTO broadcasts (identity_id, content)
VALUES ($1, $2)
RETURNING id, identity_id, content, status, reviewed_by, reviewed_at, reject_reason, show_until, created_at;

-- name: ListCurrentBroadcasts :many
SELECT id, content, show_until, created_at
FROM broadcasts
WHERE status = 'approved' AND show_until > now()
ORDER BY reviewed_at DESC
LIMIT $1;

-- name: ListMyBroadcasts :many
SELECT id, content, status, reject_reason, show_until, created_at
FROM broadcasts
WHERE identity_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetBroadcast :one
SELECT id, identity_id, content, status, reviewed_by, reviewed_at, reject_reason, show_until, created_at
FROM broadcasts
WHERE id = $1;

-- name: ListAdminBroadcasts :many
SELECT b.id, b.content, b.status, b.reject_reason, b.show_until, b.created_at, i.public_code
FROM broadcasts b
JOIN identities i ON i.id = b.identity_id
WHERE (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
ORDER BY b.created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: ApproveBroadcast :execrows
UPDATE broadcasts
SET status = 'approved', reviewed_by = $2, reviewed_at = now(), show_until = now() + interval '24 hours'
WHERE id = $1 AND status = 'pending';

-- name: RejectBroadcast :execrows
UPDATE broadcasts
SET status = 'rejected', reviewed_by = $2, reviewed_at = now(), reject_reason = $3
WHERE id = $1 AND status = 'pending';

-- name: RemoveBroadcast :execrows
UPDATE broadcasts
SET status = 'removed', reviewed_by = $2, reviewed_at = now()
WHERE id = $1 AND status IN ('pending', 'approved');

-- name: ListRecentApprovedBroadcasts :many
SELECT id, content, reviewed_at
FROM broadcasts
WHERE status = 'approved'
ORDER BY reviewed_at DESC NULLS LAST
LIMIT $1;

-- ---- 匿名动态聚合 ----

-- name: ListFeedAnnouncements :many
SELECT id, content, created_at
FROM announcements
WHERE deleted_at IS NULL
  AND starts_at <= now()
  AND (ends_at IS NULL OR ends_at > now())
ORDER BY created_at DESC
LIMIT $1;

-- name: ListFeedBroadcasts :many
SELECT id, content, COALESCE(reviewed_at, created_at) AS at
FROM broadcasts
WHERE status = 'approved' AND show_until > now()
ORDER BY COALESCE(reviewed_at, created_at) DESC
LIMIT $1;

-- name: ListFeedWorks :many
SELECT id, title, COALESCE(reviewed_at, created_at) AS at
FROM story_works
WHERE status = 'approved'
ORDER BY COALESCE(reviewed_at, created_at) DESC
LIMIT $1;
