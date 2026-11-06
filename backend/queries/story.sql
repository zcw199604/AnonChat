-- ---- 故事书 ----

-- name: CreateStory :one
INSERT INTO stories (host_identity_id, title, opening, tags, role_a, role_b, host_role, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, host_identity_id, title, opening, tags, role_a, role_b, host_role, status, conversation_id,
          guest_identity_id, active_at, ended_at, valid, host_consent, guest_consent, expires_at, created_at;

-- name: GetStory :one
SELECT id, host_identity_id, title, opening, tags, role_a, role_b, host_role, status, conversation_id,
       guest_identity_id, active_at, ended_at, valid, host_consent, guest_consent, expires_at, created_at
FROM stories
WHERE id = $1;

-- name: LockStory :one
SELECT id, host_identity_id, title, opening, tags, role_a, role_b, host_role, status, conversation_id,
       guest_identity_id, active_at, ended_at, valid, host_consent, guest_consent, expires_at, created_at
FROM stories
WHERE id = $1
FOR UPDATE;

-- name: ListWaitingStories :many
SELECT s.id, s.title, s.opening, s.tags, s.role_a, s.role_b, s.host_role, s.expires_at, s.created_at
FROM stories s
WHERE s.status = 'waiting' AND s.expires_at > now()
  AND (sqlc.narg(before_created)::timestamptz IS NULL OR s.created_at < sqlc.narg(before_created)::timestamptz)
ORDER BY s.created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: CountActiveStoriesByIdentity :one
SELECT count(*) FROM stories
WHERE (host_identity_id = $1 OR guest_identity_id = $1)
  AND status IN ('waiting', 'active');

-- name: JoinStory :execrows
UPDATE stories
SET status = 'active', guest_identity_id = $2, active_at = now()
WHERE id = $1 AND status = 'waiting' AND expires_at > now() AND host_identity_id <> $2;

-- name: SetStoryConversation :exec
UPDATE stories SET conversation_id = $2 WHERE id = $1;

-- name: EndStory :execrows
UPDATE stories
SET status = 'ended', ended_at = now(), valid = $2
WHERE id = $1 AND status = 'active';

-- name: CancelStory :execrows
UPDATE stories
SET status = 'expired', ended_at = now()
WHERE id = $1 AND status = 'waiting';

-- name: SetStoryConsent :one
UPDATE stories
SET host_consent = CASE WHEN sqlc.arg(is_host)::boolean THEN sqlc.arg(agree)::boolean ELSE host_consent END,
    guest_consent = CASE WHEN sqlc.arg(is_host)::boolean THEN guest_consent ELSE sqlc.arg(agree)::boolean END
WHERE id = sqlc.arg(id)
RETURNING id, host_identity_id, title, opening, tags, role_a, role_b, host_role, status, conversation_id,
          guest_identity_id, active_at, ended_at, valid, host_consent, guest_consent, expires_at, created_at;

-- name: CountStoryMessages :one
SELECT COALESCE(SUM(CASE WHEN m.sender_member_id = 1 THEN 1 ELSE 0 END), 0)::bigint AS member1,
       COALESCE(SUM(CASE WHEN m.sender_member_id = 2 THEN 1 ELSE 0 END), 0)::bigint AS member2
FROM messages m
WHERE m.conversation_id = $1 AND m.kind <> 'system';

-- name: ExpireWaitingStories :execrows
UPDATE stories SET status = 'expired', ended_at = now()
WHERE status = 'waiting' AND expires_at <= now();

-- name: DeleteOldEndedStories :execrows
WITH batch AS (
    SELECT s.id FROM stories s
    WHERE s.status IN ('expired', 'removed')
      AND s.ended_at IS NOT NULL AND s.ended_at < $1
      AND NOT EXISTS (SELECT 1 FROM story_works w WHERE w.story_id = s.id)
    LIMIT 5000
)
DELETE FROM stories WHERE id IN (SELECT id FROM batch);

-- ---- 作品 ----

-- name: CreateStoryWork :one
INSERT INTO story_works (story_id, title, opening, tags, role_a, role_b, lines)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, story_id, title, opening, tags, role_a, role_b, lines, status, like_count, reviewed_by,
          reviewed_at, reject_reason, created_at;

-- name: GetStoryWork :one
SELECT id, story_id, title, opening, tags, role_a, role_b, lines, status, like_count, reviewed_by,
       reviewed_at, reject_reason, created_at
FROM story_works
WHERE id = $1;

-- name: GetStoryWorkByStory :one
SELECT id, story_id, title, opening, tags, role_a, role_b, lines, status, like_count, reviewed_by,
       reviewed_at, reject_reason, created_at
FROM story_works
WHERE story_id = $1;

-- name: ListApprovedWorks :many
SELECT id, title, opening, role_a, role_b, tags, like_count, COALESCE(reviewed_at, created_at) AS at
FROM story_works
WHERE status = 'approved'
  AND (sqlc.narg(before_created)::timestamptz IS NULL OR created_at < sqlc.narg(before_created)::timestamptz)
ORDER BY COALESCE(reviewed_at, created_at) DESC
LIMIT sqlc.arg(lim)::int;

-- name: ListAdminWorks :many
SELECT id, title, status, like_count, reject_reason, created_at
FROM story_works
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: ApproveWork :execrows
UPDATE story_works SET status = 'approved', reviewed_by = $2, reviewed_at = now()
WHERE id = $1 AND status = 'pending';

-- name: RejectWork :execrows
UPDATE story_works SET status = 'rejected', reviewed_by = $2, reviewed_at = now(), reject_reason = $3
WHERE id = $1 AND status = 'pending';

-- name: RemoveWork :execrows
UPDATE story_works SET status = 'removed', reviewed_by = $2, reviewed_at = now()
WHERE id = $1 AND status IN ('pending', 'approved');

-- name: InsertWorkLike :execrows
INSERT INTO story_work_likes (work_id, identity_id) VALUES ($1, $2)
ON CONFLICT (work_id, identity_id) DO NOTHING;

-- name: DeleteWorkLike :execrows
DELETE FROM story_work_likes WHERE work_id = $1 AND identity_id = $2;

-- name: CountWorkLikes :one
SELECT count(*) FROM story_work_likes WHERE work_id = $1;

-- name: SetWorkLikeCount :exec
UPDATE story_works SET like_count = $2 WHERE id = $1;

-- name: GetWorkLiked :one
SELECT EXISTS (SELECT 1 FROM story_work_likes WHERE work_id = $1 AND identity_id = $2);

-- name: ListStoryMessages :many
SELECT m.id, m.sender_member_id, m.body, m.created_at
FROM messages m
WHERE m.conversation_id = $1 AND m.burned_at IS NULL
ORDER BY m.id ASC;

-- name: GetStoryByConversation :one
SELECT id, host_identity_id, title, opening, tags, role_a, role_b, host_role, status, conversation_id,
       guest_identity_id, active_at, ended_at, valid, host_consent, guest_consent, expires_at, created_at
FROM stories
WHERE conversation_id = $1;
