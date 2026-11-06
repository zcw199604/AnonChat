-- +goose Up
-- 本包（AI 机器人）的库结构：角色卡、媒体库、bot 及其用量记录（how.md §1）。
-- 只做加法：新表、新索引，以及给 messages.media_id 加外键。

-- 角色卡：只给模型看，不对外展示。
CREATE TABLE character_cards (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),  -- 角色名（只给模型看，不对外展示）
  description      text NOT NULL DEFAULT '',   -- 身份、外貌、背景
  personality      text NOT NULL DEFAULT '',   -- 性格与说话风格
  scenario         text NOT NULL DEFAULT '',   -- 场景设定
  first_message    text NOT NULL DEFAULT '',   -- 开场白；为空时由模型生成
  example_dialogue text NOT NULL DEFAULT '',   -- 示例对话
  system_prompt    text NOT NULL DEFAULT '',   -- 额外的系统指令
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- 媒体文件夹：bot 绑定一个文件夹，回复时按标签挑选。
CREATE TABLE media_folders (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 40),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media_items (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  folder_id     uuid NOT NULL REFERENCES media_folders(id) ON DELETE CASCADE,
  kind          text NOT NULL CHECK (kind IN ('image','video')),
  mime          text NOT NULL,
  size_bytes    bigint NOT NULL,
  storage_key   text NOT NULL UNIQUE,         -- 相对 MEDIA_ROOT 的路径：<folder_id>/<item_id><ext>
  original_name text NOT NULL DEFAULT '',
  tags          text[] NOT NULL DEFAULT '{}',
  caption       text NOT NULL DEFAULT '',     -- 内容描述，只给模型看
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_items_folder_idx ON media_items (folder_id, created_at DESC);

ALTER TABLE messages ADD CONSTRAINT messages_media_id_fkey
  FOREIGN KEY (media_id) REFERENCES media_items(id) ON DELETE SET NULL;
CREATE INDEX messages_media_id_idx ON messages (media_id) WHERE media_id IS NOT NULL;

-- bot：一个 bot 对应一个匿名身份，对外与真人完全一致。
CREATE TABLE bots (
  id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  identity_id               uuid NOT NULL UNIQUE REFERENCES identities(id),
  label                     text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 40), -- 后台备注名
  card_id                   uuid REFERENCES character_cards(id) ON DELETE SET NULL,
  media_folder_id           uuid REFERENCES media_folders(id) ON DELETE SET NULL,
  enabled                   boolean NOT NULL DEFAULT false,
  model                     text NOT NULL DEFAULT '',          -- 空表示用 LLM_MODEL
  temperature               real NOT NULL DEFAULT 0.9 CHECK (temperature BETWEEN 0 AND 2),
  max_output_tokens         int  NOT NULL DEFAULT 300 CHECK (max_output_tokens BETWEEN 16 AND 4000),
  serve_match               boolean NOT NULL DEFAULT true,
  serve_bottle              boolean NOT NULL DEFAULT true,
  max_active_matches        int  NOT NULL DEFAULT 5 CHECK (max_active_matches >= 1),
  -- 限额：0 表示不限
  total_token_limit         bigint NOT NULL DEFAULT 0 CHECK (total_token_limit >= 0),
  total_reply_limit         bigint NOT NULL DEFAULT 0 CHECK (total_reply_limit >= 0),
  window_seconds            int    NOT NULL DEFAULT 3600 CHECK (window_seconds BETWEEN 60 AND 604800),
  window_token_limit        bigint NOT NULL DEFAULT 0 CHECK (window_token_limit >= 0),
  window_reply_limit        int    NOT NULL DEFAULT 0 CHECK (window_reply_limit >= 0),
  conv_reply_limit          int    NOT NULL DEFAULT 0 CHECK (conv_reply_limit >= 0),
  conv_min_interval_seconds int    NOT NULL DEFAULT 0 CHECK (conv_min_interval_seconds >= 0),
  conv_media_limit          int    NOT NULL DEFAULT 3 CHECK (conv_media_limit >= 0),
  -- 节奏
  reply_delay_min_ms        int NOT NULL DEFAULT 3000  CHECK (reply_delay_min_ms >= 0),
  reply_delay_max_ms        int NOT NULL DEFAULT 12000 CHECK (reply_delay_max_ms >= reply_delay_min_ms),
  typing_ms_per_char        int NOT NULL DEFAULT 180   CHECK (typing_ms_per_char >= 0),
  bottle_interval_seconds   int NOT NULL DEFAULT 1800  CHECK (bottle_interval_seconds >= 60),
  -- 累计用量（总上限按这两列判断，可在后台重置）
  used_tokens               bigint NOT NULL DEFAULT 0,
  used_replies              bigint NOT NULL DEFAULT 0,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now()
);

-- 每次 LLM 调用的用量；保留 30 天（jobs/cleaner.go）。
CREATE TABLE bot_usage (
  id                bigserial PRIMARY KEY,
  bot_id            uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  conversation_id   uuid,                 -- 不加外键：会话按保留期清理后，用量记录仍保留
  prompt_tokens     int NOT NULL DEFAULT 0,
  completion_tokens int NOT NULL DEFAULT 0,
  cached_tokens     int NOT NULL DEFAULT 0,  -- prompt 中命中缓存的部分
  replies           int NOT NULL DEFAULT 0,  -- 本次调用最终发出的回复轮次（0 或 1）
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bot_usage_bot_created_idx ON bot_usage (bot_id, created_at);
CREATE INDEX bot_usage_conv_idx ON bot_usage (conversation_id, created_at) WHERE conversation_id IS NOT NULL;

-- +goose Down
-- 按相反顺序删除：bot_usage、bots、外键约束和索引、media_items、media_folders、character_cards。
DROP TABLE IF EXISTS bot_usage;
DROP TABLE IF EXISTS bots;

DROP INDEX IF EXISTS messages_media_id_idx;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_media_id_fkey;

DROP TABLE IF EXISTS media_items;
DROP TABLE IF EXISTS media_folders;
DROP TABLE IF EXISTS character_cards;
