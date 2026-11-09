-- ---- 身份 ----

-- name: GetIdentityByTokenHash :one
SELECT id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
       public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
       match_same_province, vip_until, broadcast_cards
FROM identities
WHERE token_hash = $1;

-- name: GetIdentityByID :one
SELECT id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
       public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
       match_same_province, vip_until, broadcast_cards
FROM identities
WHERE id = $1;

-- name: GetIdentityByPublicCode :one
SELECT id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
       public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
       match_same_province, vip_until, broadcast_cards
FROM identities
WHERE public_code = $1;

-- name: CreateIdentity :one
-- public_code 为空时由数据库生成一个随机的 10 位十六进制编号（兼容测试与其他调用方）。
INSERT INTO identities (token_hash, nickname, avatar_seed, created_ip, public_code)
VALUES ($1, $2, $3, $4,
        COALESCE(NULLIF(sqlc.arg(public_code)::text, ''), upper(substr(replace(gen_random_uuid()::text, '-', ''), 1, 10))))
ON CONFLICT DO NOTHING
RETURNING id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
          public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
          match_same_province, vip_until, broadcast_cards;

-- name: RevokeIdentity :execrows
UPDATE identities SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: TouchIdentityLastSeen :exec
UPDATE identities SET last_seen_at = now() WHERE id = $1;

-- name: InsertAccessLog :exec
INSERT INTO access_logs (identity_id, event, ip, user_agent)
VALUES ($1, $2, $3, $4);

-- ---- 资料与匹配偏好 ----

-- name: UpdateIdentityProfile :one
UPDATE identities
SET gender = $2,
    age = $3,
    province = $4,
    province_public = $5,
    tags = $6,
    clean_mode = $7,
    match_gender = $8,
    match_same_province = $9
WHERE id = $1
RETURNING id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
          public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
          match_same_province, vip_until, broadcast_cards;

-- name: UpdateIdentityNickname :one
UPDATE identities SET nickname = $2 WHERE id = $1
RETURNING id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
          public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
          match_same_province, vip_until, broadcast_cards;

-- ---- 权益 ----

-- name: ExtendIdentityVip :one
UPDATE identities
SET vip_until = GREATEST(COALESCE(vip_until, now()), now()) + make_interval(days => sqlc.arg(vip_days)::int),
    broadcast_cards = broadcast_cards + sqlc.arg(cards)::int
WHERE id = sqlc.arg(id)
RETURNING id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
          public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
          match_same_province, vip_until, broadcast_cards;

-- name: ConsumeBroadcastCard :execrows
UPDATE identities SET broadcast_cards = broadcast_cards - 1
WHERE id = $1 AND broadcast_cards > 0;

-- name: RefundBroadcastCard :exec
UPDATE identities SET broadcast_cards = broadcast_cards + 1 WHERE id = $1;

-- name: SetIdentityBenefits :exec
UPDATE identities
SET vip_until = sqlc.arg(vip_until), broadcast_cards = sqlc.arg(cards)
WHERE id = sqlc.arg(id);

-- ---- 匹配资料快照（入队预取用）----

-- name: GetMatchProfile :one
SELECT id, gender, age, province, province_public, tags, clean_mode, match_gender, match_same_province, vip_until
FROM identities
WHERE id = $1;

-- name: GetIdentityForUpdate :one
SELECT * FROM identities WHERE id = $1 FOR UPDATE;

-- name: LockIdentityCreateIP :exec
SELECT pg_advisory_xact_lock(hashtextextended('identity_create:' || sqlc.arg(ip)::text, 0));

-- name: CountCreatedIdentitiesByIP :one
SELECT count(*) FROM identities
WHERE created_ip = sqlc.arg(ip)::inet
  AND created_at > now() - make_interval(secs => sqlc.arg(window_seconds)::int);
