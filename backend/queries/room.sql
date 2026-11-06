-- ---- 聊天室 ----

-- name: ListRooms :many
SELECT id, conversation_id, slug, name, description, emoji, max_members, sort_order
FROM rooms
WHERE archived_at IS NULL
ORDER BY sort_order;

-- name: GetRoomByID :one
SELECT id, conversation_id, slug, name, description, max_members, sort_order, archived_at
FROM rooms
WHERE id = $1;

-- name: ListRoomMessagesDesc :many
SELECT m.id, m.sender_member_id, i.nickname, i.avatar_seed, m.kind, m.body, m.created_at
FROM messages m
JOIN identities i ON i.id = m.sender_identity_id
WHERE m.conversation_id = sqlc.arg(conversation_id)
  AND m.burned_at IS NULL
  AND m.created_at >= sqlc.arg(since)::timestamptz
  AND (sqlc.narg(before_id)::bigint IS NULL OR m.id < sqlc.narg(before_id)::bigint)
  AND NOT EXISTS (
      SELECT 1 FROM blocks b
      WHERE (b.blocker_identity_id = sqlc.arg(requester)::uuid AND b.blocked_identity_id = m.sender_identity_id)
         OR (b.blocker_identity_id = m.sender_identity_id AND b.blocked_identity_id = sqlc.arg(requester)::uuid)
  )
ORDER BY m.id DESC
LIMIT sqlc.arg(lim)::int;
