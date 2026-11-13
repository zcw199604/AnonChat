-- name: EnsureExternalSettings :exec
INSERT INTO external_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- name: GetExternalSettings :one
SELECT * FROM external_settings WHERE id = 1;

-- name: UpdateExternalSettings :one
UPDATE external_settings SET
  enabled = $1, chat2019_enabled = $2, wanfeng_enabled = $3,
  online_threshold = $4, local_wait_seconds = $5, external_match_seconds = $6,
  chat2019_max_sessions = $7, wanfeng_max_sessions = $8,
  updated_by = $9, updated_at = now()
WHERE id = 1 RETURNING *;

-- name: GetExternalBinding :one
SELECT * FROM external_bindings WHERE identity_id = $1 AND provider = $2;

-- name: UpsertExternalBinding :one
INSERT INTO external_bindings (identity_id, provider, remote_uid, identity_token_enc, device_id, nickname)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (identity_id, provider) DO UPDATE SET
  remote_uid = EXCLUDED.remote_uid, identity_token_enc = EXCLUDED.identity_token_enc,
  device_id = EXCLUDED.device_id, nickname = EXCLUDED.nickname, updated_at = now()
RETURNING *;

-- name: DeleteExternalBinding :exec
DELETE FROM external_bindings WHERE identity_id = $1 AND provider = $2;

-- name: GetExternalPeer :one
SELECT * FROM external_peers WHERE provider = $1 AND remote_uid = $2;

-- name: UpsertExternalPeer :one
INSERT INTO external_peers (provider, remote_uid, identity_id, nickname)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, remote_uid) DO UPDATE SET nickname = EXCLUDED.nickname, last_seen_at = now()
RETURNING *;

-- name: InsertExternalConversation :exec
INSERT INTO external_conversations (conversation_id, provider, user_identity_id, peer_identity_id, remote_peer_uid)
VALUES ($1, $2, $3, $4, $5);
