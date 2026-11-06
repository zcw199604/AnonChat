-- ---- 角色卡（后台管理，how.md §10）----

-- name: ListCharacterCards :many
SELECT id, name, description, personality, scenario, first_message, example_dialogue, system_prompt,
       created_at, updated_at
FROM character_cards
ORDER BY created_at DESC;

-- name: GetCharacterCard :one
SELECT id, name, description, personality, scenario, first_message, example_dialogue, system_prompt,
       created_at, updated_at
FROM character_cards
WHERE id = $1;

-- name: InsertCharacterCard :one
INSERT INTO character_cards (name, description, personality, scenario, first_message, example_dialogue, system_prompt)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, description, personality, scenario, first_message, example_dialogue, system_prompt,
          created_at, updated_at;

-- name: UpdateCharacterCard :one
UPDATE character_cards
SET name = $2,
    description = $3,
    personality = $4,
    scenario = $5,
    first_message = $6,
    example_dialogue = $7,
    system_prompt = $8,
    updated_at = now()
WHERE id = $1
RETURNING id, name, description, personality, scenario, first_message, example_dialogue, system_prompt,
          created_at, updated_at;

-- name: DeleteCharacterCard :execrows
DELETE FROM character_cards WHERE id = $1;

-- ---- Bot 设置（后台管理 + 引擎读取）----

-- name: ListBots :many
-- 后台列表：全部设置 + 对外资料 + 角色卡名/文件夹名 + 窗口内用量 + 进行中的匹配数。
SELECT b.id, b.identity_id, b.label, b.card_id, b.media_folder_id, b.enabled, b.model, b.temperature,
       b.max_output_tokens, b.serve_match, b.serve_bottle, b.max_active_matches,
       b.total_token_limit, b.total_reply_limit, b.window_seconds, b.window_token_limit,
       b.window_reply_limit, b.conv_reply_limit, b.conv_min_interval_seconds, b.conv_media_limit,
       b.reply_delay_min_ms, b.reply_delay_max_ms, b.typing_ms_per_char, b.bottle_interval_seconds,
       b.used_tokens, b.used_replies, b.created_at, b.updated_at,
       i.nickname, i.avatar_seed, i.public_code, i.gender, i.age, i.province, i.province_public,
       i.tags, i.clean_mode, i.match_gender, i.match_same_province, i.vip_until,
       COALESCE(cc.name, '')::text AS card_name,
       COALESCE(mf.name, '')::text AS folder_name,
       COALESCE((
           SELECT sum(bu.prompt_tokens + bu.completion_tokens)
           FROM bot_usage bu
           WHERE bu.bot_id = b.id
             AND bu.created_at >= now() - make_interval(secs => b.window_seconds)
       ), 0)::bigint AS window_tokens,
       COALESCE((
           SELECT sum(bu.prompt_tokens)
           FROM bot_usage bu
           WHERE bu.bot_id = b.id
             AND bu.created_at >= now() - make_interval(secs => b.window_seconds)
       ), 0)::bigint AS window_prompt_tokens,
       COALESCE((
           SELECT sum(bu.cached_tokens)
           FROM bot_usage bu
           WHERE bu.bot_id = b.id
             AND bu.created_at >= now() - make_interval(secs => b.window_seconds)
       ), 0)::bigint AS window_cached_tokens,
       COALESCE((
           SELECT sum(bu.replies)
           FROM bot_usage bu
           WHERE bu.bot_id = b.id
             AND bu.created_at >= now() - make_interval(secs => b.window_seconds)
       ), 0)::bigint AS window_replies,
       COALESCE((
           SELECT count(*)
           FROM conversations c
           JOIN conversation_members cm ON cm.conversation_id = c.id AND cm.identity_id = b.identity_id AND cm.left_at IS NULL
           WHERE c.kind = 'match' AND c.ended_at IS NULL
       ), 0)::bigint AS active_matches
FROM bots b
JOIN identities i ON i.id = b.identity_id
LEFT JOIN character_cards cc ON cc.id = b.card_id
LEFT JOIN media_folders mf ON mf.id = b.media_folder_id
ORDER BY b.created_at DESC;

-- name: GetBot :one
SELECT id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
       max_output_tokens, serve_match, serve_bottle, max_active_matches,
       total_token_limit, total_reply_limit, window_seconds, window_token_limit,
       window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
       reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
       used_tokens, used_replies, created_at, updated_at
FROM bots
WHERE id = $1;

-- name: GetBotByIdentity :one
SELECT id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
       max_output_tokens, serve_match, serve_bottle, max_active_matches,
       total_token_limit, total_reply_limit, window_seconds, window_token_limit,
       window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
       reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
       used_tokens, used_replies, created_at, updated_at
FROM bots
WHERE identity_id = $1;

-- name: ListEnabledBots :many
SELECT id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
       max_output_tokens, serve_match, serve_bottle, max_active_matches,
       total_token_limit, total_reply_limit, window_seconds, window_token_limit,
       window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
       reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
       used_tokens, used_replies, created_at, updated_at
FROM bots
WHERE enabled
ORDER BY id;

-- name: ListBotIdentities :many
-- 引擎缓存用：一次取出所有已启用 bot 的身份行，避免逐个查询。
SELECT id, token_hash, nickname, avatar_seed, account_id, created_ip, revoked_at, created_at, last_seen_at,
       public_code, gender, age, province, province_public, tags, clean_mode, match_gender,
       match_same_province, vip_until, broadcast_cards
FROM identities
WHERE id IN (SELECT identity_id FROM bots WHERE enabled);

-- name: InsertBot :one
INSERT INTO bots (identity_id, label, card_id, media_folder_id, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
          max_output_tokens, serve_match, serve_bottle, max_active_matches,
          total_token_limit, total_reply_limit, window_seconds, window_token_limit,
          window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
          reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
          used_tokens, used_replies, created_at, updated_at;

-- name: UpdateBot :one
UPDATE bots
SET label = $2,
    card_id = $3,
    media_folder_id = $4,
    enabled = $5,
    model = $6,
    temperature = $7,
    max_output_tokens = $8,
    serve_match = $9,
    serve_bottle = $10,
    max_active_matches = $11,
    total_token_limit = $12,
    total_reply_limit = $13,
    window_seconds = $14,
    window_token_limit = $15,
    window_reply_limit = $16,
    conv_reply_limit = $17,
    conv_min_interval_seconds = $18,
    conv_media_limit = $19,
    reply_delay_min_ms = $20,
    reply_delay_max_ms = $21,
    typing_ms_per_char = $22,
    bottle_interval_seconds = $23,
    updated_at = now()
WHERE id = $1
RETURNING id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
          max_output_tokens, serve_match, serve_bottle, max_active_matches,
          total_token_limit, total_reply_limit, window_seconds, window_token_limit,
          window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
          reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
          used_tokens, used_replies, created_at, updated_at;

-- name: ResetBotUsage :one
UPDATE bots
SET used_tokens = 0, used_replies = 0
WHERE id = $1
RETURNING id, identity_id, label, card_id, media_folder_id, enabled, model, temperature,
          max_output_tokens, serve_match, serve_bottle, max_active_matches,
          total_token_limit, total_reply_limit, window_seconds, window_token_limit,
          window_reply_limit, conv_reply_limit, conv_min_interval_seconds, conv_media_limit,
          reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char, bottle_interval_seconds,
          used_tokens, used_replies, created_at, updated_at;

-- name: DeleteBot :execrows
DELETE FROM bots WHERE id = $1;

-- ---- 会话中的 bot 成员 ----

-- name: GetConversationBot :one
-- 会话中未离开的 bot 成员；一个会话最多一个 bot。
SELECT b.id, b.identity_id, b.label, b.card_id, b.media_folder_id, b.enabled, b.model, b.temperature,
       b.max_output_tokens, b.serve_match, b.serve_bottle, b.max_active_matches,
       b.total_token_limit, b.total_reply_limit, b.window_seconds, b.window_token_limit,
       b.window_reply_limit, b.conv_reply_limit, b.conv_min_interval_seconds, b.conv_media_limit,
       b.reply_delay_min_ms, b.reply_delay_max_ms, b.typing_ms_per_char, b.bottle_interval_seconds,
       b.used_tokens, b.used_replies, b.created_at, b.updated_at
FROM conversation_members cm
JOIN bots b ON b.identity_id = cm.identity_id
WHERE cm.conversation_id = $1 AND cm.left_at IS NULL
ORDER BY cm.member_id
LIMIT 1;

-- name: CountActiveMatchConversationsForIdentity :one
SELECT count(*)::bigint AS active_matches
FROM conversations c
JOIN conversation_members cm
  ON cm.conversation_id = c.id AND cm.identity_id = $1 AND cm.left_at IS NULL
WHERE c.kind = 'match' AND c.ended_at IS NULL;

-- name: ListOpenMatchConversationsForIdentity :many
-- 删除 bot 时用：先把这些会话以 peer_left 结束（走 conversation.Service.End 才能通知对端）。
SELECT c.id
FROM conversations c
JOIN conversation_members cm
  ON cm.conversation_id = c.id AND cm.identity_id = $1 AND cm.left_at IS NULL
WHERE c.kind = 'match' AND c.ended_at IS NULL;

-- name: CountConversationMessages :one
-- 开场判断用：会话里是否已经有消息。
SELECT count(*)::bigint AS messages
FROM messages
WHERE conversation_id = $1;

-- ---- 用量 ----

-- name: InsertBotUsage :one
INSERT INTO bot_usage (bot_id, conversation_id, prompt_tokens, completion_tokens, cached_tokens, replies)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, bot_id, conversation_id, prompt_tokens, completion_tokens, cached_tokens, replies, created_at;

-- name: AddBotUsed :exec
-- 累加累计用量（与 InsertBotUsage 放在同一个事务里）。
UPDATE bots
SET used_tokens = used_tokens + $2,
    used_replies = used_replies + $3
WHERE id = $1;

-- name: SumBotUsageSince :one
-- 滑动窗口内的用量（token = prompt + completion）。
SELECT COALESCE(sum(prompt_tokens + completion_tokens), 0)::bigint AS tokens,
       COALESCE(sum(replies), 0)::bigint AS replies
FROM bot_usage
WHERE bot_id = $1 AND created_at >= $2;

-- name: GetConversationBotUsage :one
-- 某个会话的机器人用量：回复条数与最近一次真正发出回复的时间。
SELECT COALESCE(sum(replies), 0)::bigint AS replies,
       (max(created_at) FILTER (WHERE replies > 0))::timestamptz AS last_reply_at,
       COALESCE(sum(prompt_tokens + completion_tokens), 0)::bigint AS tokens
FROM bot_usage
WHERE conversation_id = $1;

-- ---- 全局设置（单行表，how.md §15.1）----

-- name: GetBotSettings :one
SELECT id, enabled, llm_base_url, llm_api_key_enc, llm_api_key_hint, llm_api_key_base_url, llm_model,
       llm_timeout_seconds, llm_send_cache_key, match_fallback_seconds, bottle_min_age_seconds, timezone,
       updated_by, updated_at
FROM bot_settings
WHERE id = 1;

-- name: InsertBotSettings :exec
-- 首次初始化：表里没有行时用环境变量的值写入（之后以数据库为准）。
INSERT INTO bot_settings (id, enabled, llm_base_url, llm_model, llm_timeout_seconds, llm_send_cache_key,
                          match_fallback_seconds, bottle_min_age_seconds, timezone)
VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING;

-- name: UpdateBotSettings :one
UPDATE bot_settings
SET enabled = $1,
    llm_base_url = $2,
    llm_api_key_enc = $3,
    llm_api_key_hint = $4,
    llm_api_key_base_url = $5,
    llm_model = $6,
    llm_timeout_seconds = $7,
    llm_send_cache_key = $8,
    match_fallback_seconds = $9,
    bottle_min_age_seconds = $10,
    timezone = $11,
    updated_by = $12,
    updated_at = now()
WHERE id = 1
RETURNING id, enabled, llm_base_url, llm_api_key_enc, llm_api_key_hint, llm_api_key_base_url, llm_model,
          llm_timeout_seconds, llm_send_cache_key, match_fallback_seconds, bottle_min_age_seconds, timezone,
          updated_by, updated_at;
