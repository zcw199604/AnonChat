-- 长期记忆基础查询；画像合并与事件内容校验由应用层负责。

-- name: GetPeerMemory :one
SELECT * FROM bot_peer_memories
WHERE bot_id = $1 AND peer_identity_id = $2;

-- name: UpsertPeerMemory :one
INSERT INTO bot_peer_memories (bot_id, peer_identity_id, profile, affinity, mood_label, mood_value, last_interaction_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (bot_id, peer_identity_id) DO UPDATE
SET profile = EXCLUDED.profile, affinity = EXCLUDED.affinity,
    mood_label = EXCLUDED.mood_label, mood_value = EXCLUDED.mood_value,
    last_interaction_at = EXCLUDED.last_interaction_at, updated_at = now()
RETURNING *;

-- name: TouchPeerMemory :exec
-- 成功回复后更新互动时间；没有新情绪时保留原情绪。
INSERT INTO bot_peer_memories (bot_id, peer_identity_id, affinity, mood_label, mood_value, last_interaction_at)
VALUES (sqlc.arg(bot_id), sqlc.arg(peer_identity_id), sqlc.arg(delta)::smallint,
        sqlc.arg(mood_label), sqlc.arg(mood_value)::smallint, sqlc.arg(interacted_at)::timestamptz)
ON CONFLICT (bot_id, peer_identity_id) DO UPDATE
SET affinity = LEAST(100, GREATEST(-100, bot_peer_memories.affinity + sqlc.arg(delta)::smallint)),
    mood_label = CASE WHEN sqlc.arg(mood_label)::text = '' THEN bot_peer_memories.mood_label ELSE EXCLUDED.mood_label END,
    mood_value = CASE WHEN sqlc.arg(mood_label)::text = '' THEN bot_peer_memories.mood_value ELSE EXCLUDED.mood_value END,
    last_interaction_at = GREATEST(bot_peer_memories.last_interaction_at, EXCLUDED.last_interaction_at),
    updated_at = now();

-- name: EnsurePeerMemory :exec
INSERT INTO bot_peer_memories (bot_id, peer_identity_id, last_interaction_at)
VALUES ($1, $2, $3)
ON CONFLICT (bot_id, peer_identity_id) DO NOTHING;

-- name: LockPeerMemory :one
SELECT * FROM bot_peer_memories
WHERE bot_id = $1 AND peer_identity_id = $2 FOR UPDATE;

-- name: DeletePeerMemory :execrows
DELETE FROM bot_peer_memories WHERE bot_id = $1 AND peer_identity_id = $2;

-- name: ListPeerEvents :many
SELECT * FROM bot_peer_events
WHERE bot_id = $1 AND peer_identity_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: InsertPeerEvent :one
INSERT INTO bot_peer_events (bot_id, peer_identity_id, conversation_id, gist, tags)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListMemoryCandidates :many
-- 使用可注入的当前时刻，便于重试和空闲窗口的确定性验证。
SELECT c.id AS conversation_id, b.id AS bot_id, cm_peer.identity_id AS peer_identity_id,
       COALESCE(mc.extracted_until, '-infinity'::timestamptz)::timestamptz AS extracted_until,
       COALESCE(mc.fail_count, 0)::int AS fail_count
FROM bots b
JOIN conversation_members cm_bot ON cm_bot.identity_id = b.identity_id
JOIN conversations c ON c.id = cm_bot.conversation_id
JOIN conversation_members cm_peer ON cm_peer.conversation_id = c.id AND cm_peer.identity_id <> b.identity_id
LEFT JOIN bot_memory_cursors mc ON mc.conversation_id = c.id
JOIN LATERAL (
  SELECT count(*) AS n, max(m.created_at) AS last_at,
         bool_or(m.sender_identity_id = cm_peer.identity_id) AS has_peer
  FROM messages m
  WHERE m.conversation_id = c.id
    AND m.created_at > COALESCE(mc.extracted_until, '-infinity'::timestamptz)
    AND m.kind IN ('text','image','video') AND m.burned_at IS NULL
) nm ON nm.n > 0 AND nm.has_peer
WHERE b.enabled AND b.memory_enabled
  AND c.kind IN ('match','bottle')
  AND (c.ended_at IS NULL OR c.ended_at > sqlc.arg(as_of)::timestamptz - interval '8 days')
  AND (mc.next_try_at IS NULL OR mc.next_try_at <= sqlc.arg(as_of)::timestamptz)
  AND (c.ended_at IS NOT NULL OR nm.n >= sqlc.arg(extract_every)::int OR nm.last_at < sqlc.arg(as_of)::timestamptz - interval '10 minutes')
ORDER BY nm.last_at, c.id
LIMIT 20;

-- name: ListMemoryMessages :many
-- 先取最新 80 条，再按时间升序交给抽取模型；已焚毁消息不进入输入。
SELECT recent.* FROM (
  SELECT m.id, m.sender_identity_id, m.kind, m.body, m.created_at,
         COALESCE(mi.tags, '{}'::text[])::text[] AS tags
  FROM messages m
  LEFT JOIN media_items mi ON mi.id = m.media_id
  WHERE m.conversation_id = sqlc.arg(conversation_id)
    AND m.created_at > sqlc.arg(extracted_until)::timestamptz
    AND m.kind IN ('text','image','video') AND m.burned_at IS NULL
  ORDER BY m.created_at DESC, m.id DESC
  LIMIT 80
) recent ORDER BY recent.created_at, recent.id;

-- name: TrimPeerEvents :execrows
-- 每对只保留最新的 50 条；同一时刻的事件按 ID 稳定排序。
DELETE FROM bot_peer_events AS target
WHERE target.bot_id = $1 AND target.peer_identity_id = $2 AND target.id IN (
    SELECT id FROM bot_peer_events
    WHERE bot_id = $1 AND peer_identity_id = $2
    ORDER BY created_at DESC, id DESC OFFSET 50
);

-- name: GetMemoryCursor :one
SELECT * FROM bot_memory_cursors WHERE conversation_id = $1;

-- name: UpsertMemoryCursor :one
INSERT INTO bot_memory_cursors (conversation_id, bot_id, extracted_until, fail_count, next_try_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (conversation_id) DO UPDATE
SET bot_id = EXCLUDED.bot_id, extracted_until = EXCLUDED.extracted_until,
    fail_count = EXCLUDED.fail_count, next_try_at = EXCLUDED.next_try_at, updated_at = now()
RETURNING *;

-- name: ListBotMemories :many
SELECT m.peer_identity_id, i.public_code, i.nickname, m.affinity, m.mood_label, m.mood_value,
       m.last_interaction_at,
       (SELECT count(*) FROM bot_peer_events e
        WHERE e.bot_id = m.bot_id AND e.peer_identity_id = m.peer_identity_id)::bigint AS event_count
FROM bot_peer_memories m
JOIN identities i ON i.id = m.peer_identity_id
WHERE m.bot_id = $1
ORDER BY m.last_interaction_at DESC, m.peer_identity_id
LIMIT $2 OFFSET $3;

-- name: ClearBotMemories :execrows
DELETE FROM bot_peer_memories WHERE bot_id = $1;
