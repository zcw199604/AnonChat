-- ---- 漂流瓶 ----

-- name: InsertBottle :one
INSERT INTO bottles (identity_id, content, expires_at)
VALUES ($1, $2, $3)
RETURNING id, identity_id, content, status, held_by, held_until, expires_at, created_at;

-- name: ReleaseHeldBy :exec
UPDATE bottles
SET status = 'floating', held_by = NULL, held_until = NULL
WHERE held_by = $1 AND status = 'held';

-- name: PickCandidateFrom :one
SELECT id, identity_id, content, created_at
FROM bottles b
WHERE b.expires_at > $3
  AND (b.status = 'floating' OR (b.status = 'held' AND b.held_until < $3))
  AND b.identity_id <> $1
  AND b.id >= $2
  AND NOT EXISTS (SELECT 1 FROM bottle_picks bp WHERE bp.bottle_id = b.id AND bp.picker_identity_id = $1)
  AND NOT EXISTS (
      SELECT 1 FROM blocks bl
      WHERE (bl.blocker_identity_id = $1 AND bl.blocked_identity_id = b.identity_id)
         OR (bl.blocker_identity_id = b.identity_id AND bl.blocked_identity_id = $1)
  )
ORDER BY b.id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: PickCandidateAny :one
SELECT id, identity_id, content, created_at
FROM bottles b
WHERE b.expires_at > $2
  AND (b.status = 'floating' OR (b.status = 'held' AND b.held_until < $2))
  AND b.identity_id <> $1
  AND NOT EXISTS (SELECT 1 FROM bottle_picks bp WHERE bp.bottle_id = b.id AND bp.picker_identity_id = $1)
  AND NOT EXISTS (
      SELECT 1 FROM blocks bl
      WHERE (bl.blocker_identity_id = $1 AND bl.blocked_identity_id = b.identity_id)
         OR (bl.blocker_identity_id = b.identity_id AND bl.blocked_identity_id = $1)
  )
ORDER BY b.id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: MarkBottleHeld :exec
UPDATE bottles SET status = 'held', held_by = $1, held_until = $2 WHERE id = $3;

-- name: InsertBottlePick :exec
INSERT INTO bottle_picks (bottle_id, picker_identity_id)
VALUES ($1, $2)
ON CONFLICT (bottle_id, picker_identity_id) DO NOTHING;

-- name: LockBottleByID :one
SELECT id, identity_id, content, status, held_by, held_until, expires_at, created_at
FROM bottles
WHERE id = $1
FOR UPDATE;

-- name: ThrowBack :execrows
UPDATE bottles
SET status = 'floating', held_by = NULL, held_until = NULL
WHERE id = $1 AND held_by = $2 AND status = 'held';

-- name: GetBottleContentByConversation :one
-- bot 提示词需要漂流瓶原文：按回复产生的会话反查瓶子内容。
SELECT b.content
FROM bottles b
JOIN bottle_picks bp ON bp.bottle_id = b.id
WHERE bp.conversation_id = $1
LIMIT 1;

-- name: GetBottlePick :one
SELECT bottle_id, picker_identity_id, conversation_id, picked_at
FROM bottle_picks
WHERE bottle_id = $1 AND picker_identity_id = $2;

-- name: SetBottlePickConversation :execrows
UPDATE bottle_picks SET conversation_id = $3
WHERE bottle_id = $1 AND picker_identity_id = $2 AND conversation_id IS NULL;

-- name: ListMyBottles :many
SELECT id, content, created_at, expires_at
FROM bottles
WHERE identity_id = $1 AND expires_at > $2 AND status <> 'removed'
ORDER BY created_at DESC;

-- name: ListBottleReplies :many
SELECT bp.conversation_id,
       COALESCE((
           SELECT count(*) FROM messages m
           WHERE m.conversation_id = bp.conversation_id
             AND m.burned_at IS NULL
             AND m.sender_identity_id <> $2
             AND m.id > COALESCE(cm.last_read_message_id, 0)
       ), 0)::bigint AS unread
FROM bottle_picks bp
JOIN conversation_members cm
  ON cm.conversation_id = bp.conversation_id AND cm.identity_id = $2
WHERE bp.bottle_id = $1 AND bp.conversation_id IS NOT NULL;

-- name: ReleaseBottleByHolder :exec
UPDATE bottles
SET status = 'floating', held_by = NULL, held_until = NULL
WHERE id = $1 AND held_by = $2;

-- ---- Bot 兜底（how.md §9）----

-- name: PickBottleForBot :one
-- bot 挑一个还没人回复过的旧瓶子：未过期、已到最小年龄、投瓶人不是 bot、
-- 还没有任何回复、这个 bot 没捞过、双方没有屏蔽。FOR UPDATE SKIP LOCKED 避免并发抢同一个。
SELECT b.id, b.identity_id, b.content, b.created_at
FROM bottles b
WHERE b.expires_at > sqlc.arg(now)::timestamptz
  AND (b.status = 'floating' OR (b.status = 'held' AND b.held_until < sqlc.arg(now)::timestamptz))
  AND b.created_at <= sqlc.arg(created_before)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM bots bo WHERE bo.identity_id = b.identity_id)
  AND NOT EXISTS (
      SELECT 1 FROM bottle_picks bp
      WHERE bp.bottle_id = b.id AND bp.conversation_id IS NOT NULL
  )
  AND NOT EXISTS (
      SELECT 1 FROM bottle_picks bp
      WHERE bp.bottle_id = b.id AND bp.picker_identity_id = sqlc.arg(bot_identity_id)::uuid
  )
  AND NOT EXISTS (
      SELECT 1 FROM blocks bl
      WHERE (bl.blocker_identity_id = sqlc.arg(bot_identity_id)::uuid AND bl.blocked_identity_id = b.identity_id)
         OR (bl.blocker_identity_id = b.identity_id AND bl.blocked_identity_id = sqlc.arg(bot_identity_id)::uuid)
  )
ORDER BY b.created_at
LIMIT 1
FOR UPDATE SKIP LOCKED;
