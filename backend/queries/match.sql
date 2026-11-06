-- ---- 匹配 ----

-- name: ExistsBlockBetween :one
SELECT EXISTS (
    SELECT 1 FROM blocks
    WHERE (blocker_identity_id = $1 AND blocked_identity_id = $2)
       OR (blocker_identity_id = $2 AND blocked_identity_id = $1)
);

-- name: ListRecentMatchOpponents :many
SELECT other FROM (
    SELECT cm2.identity_id AS other, max(c.created_at) AS last_at
    FROM conversation_members cm1
    JOIN conversations c ON c.id = cm1.conversation_id AND c.kind = 'match'
    JOIN conversation_members cm2 ON cm2.conversation_id = cm1.conversation_id AND cm2.identity_id <> cm1.identity_id
    WHERE cm1.identity_id = $1
    GROUP BY cm2.identity_id
) t
ORDER BY last_at DESC
LIMIT $2;

-- name: GetActiveMatchConversation :one
SELECT c.id, c.kind, c.burn_after_read, c.ended_at, c.end_reason, c.created_at
FROM conversations c
JOIN conversation_members cm ON cm.conversation_id = c.id AND cm.identity_id = $1 AND cm.left_at IS NULL
WHERE c.kind = 'match' AND c.ended_at IS NULL
ORDER BY c.created_at DESC
LIMIT 1;

-- name: EndAllOpenMatchConversations :many
UPDATE conversations SET ended_at = now(), end_reason = 'expired'
WHERE kind = 'match' AND ended_at IS NULL
RETURNING id;
