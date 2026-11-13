-- ---- 媒体文件夹（后台管理，how.md §4）----

-- name: ListMediaFolders :many
-- 列表带每个文件夹的媒体数量。
SELECT f.id, f.name, f.created_at,
       (SELECT count(*) FROM media_items mi WHERE mi.folder_id = f.id)::bigint AS item_count
FROM media_folders f
ORDER BY f.created_at;

-- name: GetMediaFolder :one
SELECT id, name, created_at
FROM media_folders
WHERE id = $1;

-- name: InsertMediaFolder :one
INSERT INTO media_folders (name)
VALUES ($1)
RETURNING id, name, created_at;

-- name: RenameMediaFolder :one
UPDATE media_folders
SET name = $2
WHERE id = $1
RETURNING id, name, created_at;

-- name: DeleteMediaFolder :execrows
DELETE FROM media_folders WHERE id = $1;

-- name: ListFolderStorageKeys :many
-- 删除文件夹前取出磁盘文件路径（行由外键级联删除）。
SELECT storage_key
FROM media_items
WHERE folder_id = $1;

-- ---- 媒体条目 ----

-- name: ListMediaItemsByFolder :many
SELECT id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id
FROM media_items
WHERE folder_id = $1
ORDER BY created_at DESC, id;

-- name: GetMediaItem :one
SELECT id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id
FROM media_items
WHERE id = $1;

-- name: InsertMediaItem :one
INSERT INTO media_items (id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, source, owner_identity_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id;

-- name: UpdateMediaItem :one
UPDATE media_items
SET tags = $2, caption = $3
WHERE id = $1
RETURNING id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id;

-- name: DeleteMediaItem :execrows
DELETE FROM media_items WHERE id = $1;

-- name: ListFolderTags :many
-- 文件夹内所有标签去重后按字母序返回（提示词里的媒体词表要稳定）。
SELECT DISTINCT unnest(tags)::text AS tag
FROM media_items
WHERE folder_id = $1
ORDER BY tag;

-- ---- bot 挑图与用户访问 ----

-- name: PickMediaForBot :one
-- 在 bot 的文件夹里挑一个媒体：优先标签全命中，否则至少命中一个；
-- 排除本对话已经发过的媒体（依据 messages.media_id）。
SELECT id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id
FROM media_items mi
WHERE mi.folder_id = sqlc.arg(folder_id)
  AND mi.tags && sqlc.arg(tags)::text[]
  AND NOT EXISTS (
      SELECT 1 FROM messages m
      WHERE m.conversation_id = sqlc.arg(conversation_id) AND m.media_id = mi.id
  )
ORDER BY (mi.tags @> sqlc.arg(tags)::text[]) DESC, random()
LIMIT 1;

-- name: ListConversationSentMediaIDs :many
-- 对话中已经发送过的媒体，用于去重和 conv_media_limit 判断。
SELECT DISTINCT media_id
FROM messages
WHERE conversation_id = $1 AND media_id IS NOT NULL;

-- name: CanIdentityAccessMedia :one
-- 未发送时允许上传者预览；发送后只按会话中的未焚毁消息授权。
SELECT EXISTS (SELECT 1 WHERE EXISTS (
    SELECT 1
    FROM messages m
    JOIN conversation_members cm
      ON cm.conversation_id = m.conversation_id AND cm.identity_id = $2
    WHERE m.media_id = $1 AND m.burned_at IS NULL
) OR EXISTS (
    SELECT 1 FROM media_items mi
    WHERE mi.id = $1 AND mi.owner_identity_id = $2
      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.media_id = mi.id)
)) AS allowed;

-- name: GetUserMediaForSend :one
SELECT id, folder_id, kind, mime, size_bytes, storage_key, original_name, tags, caption, created_at, source, owner_identity_id
FROM media_items
WHERE id = $1 AND source = 'user' AND owner_identity_id = $2;

-- name: ListOrphanNonLibraryMedia :many
SELECT mi.id, mi.created_at FROM media_items mi
WHERE mi.source <> 'library' AND mi.created_at < sqlc.arg(cutoff)
  AND (mi.created_at, mi.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.media_id = mi.id AND m.burned_at IS NULL)
ORDER BY mi.created_at, mi.id
LIMIT 500
FOR UPDATE OF mi SKIP LOCKED;

-- name: DeleteOrphanNonLibraryMedia :many
DELETE FROM media_items mi
WHERE mi.id = ANY(sqlc.arg(ids)::uuid[])
  AND mi.source <> 'library'
  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.media_id = mi.id AND m.burned_at IS NULL)
RETURNING storage_key;
