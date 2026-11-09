-- name: ListRateLimitRules :many
SELECT key, limit_count, window_seconds, ban_seconds, enabled, updated_by, updated_at
FROM rate_limit_rules ORDER BY key;

-- name: UpsertRateLimitRule :one
INSERT INTO rate_limit_rules (key, limit_count, window_seconds, ban_seconds, enabled, updated_by)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (key) DO UPDATE SET limit_count=EXCLUDED.limit_count,
 window_seconds=EXCLUDED.window_seconds,ban_seconds=EXCLUDED.ban_seconds,
 enabled=EXCLUDED.enabled,updated_by=EXCLUDED.updated_by,updated_at=now()
RETURNING key, limit_count, window_seconds, ban_seconds, enabled, updated_by, updated_at;
