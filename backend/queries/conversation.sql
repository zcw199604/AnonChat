-- ---- 会话与成员 ----

-- name: CreateConversation :one
INSERT INTO conversations (kind, burn_after_read)
VALUES ($1, $2)
RETURNING id, kind, burn_after_read, ended_at, end_reason, created_at;

-- name: GetConversation :one
SELECT id, kind, burn_after_read, ended_at, end_reason, created_at
FROM conversations
WHERE id = $1;

-- name: LockConversation :one
SELECT id, kind, burn_after_read, ended_at, end_reason, created_at
FROM conversations
WHERE id = $1
FOR UPDATE;

-- name: EndConversation :execrows
UPDATE conversations
SET ended_at = now(), end_reason = $2
WHERE id = $1 AND ended_at IS NULL;

-- name: GetMember :one
SELECT conversation_id, member_id, identity_id, role, last_read_message_id, muted_until, joined_at, left_at
FROM conversation_members
WHERE conversation_id = $1 AND identity_id = $2;

-- name: NextMemberID :one
SELECT COALESCE(MAX(member_id), 0) + 1 AS next_id
FROM conversation_members
WHERE conversation_id = $1;

-- name: InsertMember :one
INSERT INTO conversation_members (conversation_id, member_id, identity_id)
VALUES ($1, $2, $3)
RETURNING conversation_id, member_id, identity_id, role, last_read_message_id, muted_until, joined_at, left_at;

-- name: RejoinMember :exec
UPDATE conversation_members
SET left_at = NULL
WHERE conversation_id = $1 AND identity_id = $2;

-- name: LeaveMember :exec
UPDATE conversation_members
SET left_at = now()
WHERE conversation_id = $1 AND identity_id = $2 AND left_at IS NULL;

-- name: ListActiveMemberIdentities :many
SELECT identity_id
FROM conversation_members
WHERE conversation_id = $1 AND left_at IS NULL;

-- ---- 消息 ----

-- name: InsertMessage :one
INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (sender_identity_id, client_msg_id) DO NOTHING
RETURNING id, conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, media_id, read_at, burned_at, created_at;

-- name: GetMessageByIdentityClientID :one
SELECT id, conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, media_id, read_at, burned_at, created_at
FROM messages
WHERE sender_identity_id = $1 AND client_msg_id = $2;

-- name: ListMessagesDesc :many
SELECT m.id, m.conversation_id, m.sender_identity_id, m.sender_member_id, m.client_msg_id, m.kind, m.body, m.media_id, m.read_at, m.burned_at, m.created_at,
       i.nickname, i.avatar_seed
FROM messages m
JOIN identities i ON i.id = m.sender_identity_id
WHERE m.conversation_id = sqlc.arg(conversation_id)
  AND m.burned_at IS NULL
  AND (sqlc.narg(before_id)::bigint IS NULL OR m.id < sqlc.narg(before_id)::bigint)
  AND NOT EXISTS (
      SELECT 1 FROM blocks b
      WHERE (b.blocker_identity_id = sqlc.arg(requester_identity_id)::uuid AND b.blocked_identity_id = m.sender_identity_id)
         OR (b.blocker_identity_id = m.sender_identity_id AND b.blocked_identity_id = sqlc.arg(requester_identity_id)::uuid)
  )
ORDER BY m.id DESC
LIMIT sqlc.arg(lim)::int;

-- name: ListMessagesAsc :many
SELECT m.id, m.conversation_id, m.sender_identity_id, m.sender_member_id, m.client_msg_id, m.kind, m.body, m.media_id, m.read_at, m.burned_at, m.created_at,
       i.nickname, i.avatar_seed
FROM messages m
JOIN identities i ON i.id = m.sender_identity_id
WHERE m.conversation_id = sqlc.arg(conversation_id)
  AND m.burned_at IS NULL
  AND m.id > sqlc.arg(after_id)::bigint
  AND NOT EXISTS (
      SELECT 1 FROM blocks b
      WHERE (b.blocker_identity_id = sqlc.arg(requester_identity_id)::uuid AND b.blocked_identity_id = m.sender_identity_id)
         OR (b.blocker_identity_id = m.sender_identity_id AND b.blocked_identity_id = sqlc.arg(requester_identity_id)::uuid)
  )
ORDER BY m.id ASC
LIMIT sqlc.arg(lim)::int;

-- name: UpdateLastRead :exec
UPDATE conversation_members
SET last_read_message_id = GREATEST(COALESCE(last_read_message_id, 0), $3)
WHERE conversation_id = $1 AND identity_id = $2;

-- name: MarkMessagesRead :execrows
UPDATE messages
SET read_at = now()
WHERE conversation_id = $1 AND id <= $2 AND read_at IS NULL AND sender_identity_id <> $3;

-- name: BurnExpiredMessages :many
UPDATE messages
SET burned_at = now()
WHERE burned_at IS NULL
  AND read_at IS NOT NULL
  AND read_at <= $1
  AND conversation_id IN (SELECT id FROM conversations WHERE burn_after_read)
RETURNING conversation_id, id;

-- name: ListConversations :many
SELECT c.id, c.kind, c.burn_after_read, c.ended_at, c.created_at,
       COALESCE((
           SELECT count(*)
           FROM messages m
           WHERE m.conversation_id = c.id
             AND m.burned_at IS NULL
             AND m.sender_identity_id <> sqlc.arg(identity_id)::uuid
             AND m.id > COALESCE(cm.last_read_message_id, 0)
             AND NOT EXISTS (
                 SELECT 1 FROM blocks b
                 WHERE (b.blocker_identity_id = sqlc.arg(identity_id)::uuid AND b.blocked_identity_id = m.sender_identity_id)
                    OR (b.blocker_identity_id = m.sender_identity_id AND b.blocked_identity_id = sqlc.arg(identity_id)::uuid)
             )
       ), 0)::bigint AS unread_count
FROM conversations c
JOIN conversation_members cm
  ON cm.conversation_id = c.id AND cm.identity_id = sqlc.arg(identity_id)::uuid AND cm.left_at IS NULL
WHERE c.ended_at IS NULL AND c.kind <> 'room'
ORDER BY c.created_at DESC;

-- ---- 屏蔽 ----

-- name: ListBlocksRelated :many
SELECT (CASE WHEN blocker_identity_id = $1 THEN blocked_identity_id ELSE blocker_identity_id END)::uuid AS other
FROM blocks
WHERE blocker_identity_id = $1 OR blocked_identity_id = $1;

-- name: InsertBlock :exec
INSERT INTO blocks (blocker_identity_id, blocked_identity_id)
VALUES ($1, $2)
ON CONFLICT (blocker_identity_id, blocked_identity_id) DO NOTHING;

-- name: GetMemberByMemberID :one
SELECT conversation_id, member_id, identity_id, role, last_read_message_id, muted_until, joined_at, left_at
FROM conversation_members
WHERE conversation_id = $1 AND member_id = $2;

-- name: ListOtherMemberIdentities :many
SELECT identity_id
FROM conversation_members
WHERE conversation_id = $1 AND identity_id <> $2 AND left_at IS NULL;

-- name: ListOtherMembersWithIdentity :many
SELECT cm.member_id, i.nickname, i.avatar_seed
FROM conversation_members cm
JOIN identities i ON i.id = cm.identity_id
WHERE cm.conversation_id = $1 AND cm.identity_id <> $2 AND cm.left_at IS NULL
ORDER BY cm.member_id;
