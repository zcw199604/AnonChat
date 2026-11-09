-- +goose Up
-- 角色卡扩展：心核/智核人设、ST V2 扩展字段、世界书、内置工具、表情文件夹、来源格式。
ALTER TABLE character_cards
  ADD COLUMN heart_prompt              text   NOT NULL DEFAULT '',   -- 心核人设（.krp heart.prompt）
  ADD COLUMN reason_prompt             text   NOT NULL DEFAULT '',   -- 智核人设（.krp reason.prompt）；为空时使用内置默认智核提示
  ADD COLUMN alternate_greetings       text[] NOT NULL DEFAULT '{}',  -- 备选开场白（ST V2）
  ADD COLUMN post_history_instructions text   NOT NULL DEFAULT '',   -- 历史后指令（ST V2），放在提示词末尾
  ADD COLUMN lorebook                  jsonb  NOT NULL DEFAULT '[]', -- 世界书条目数组，结构见 §3.1
  ADD COLUMN tools                     text[] NOT NULL DEFAULT '{}',  -- 启用的内置工具；空数组表示全部启用
  ADD COLUMN expression_folder_id      uuid REFERENCES media_folders(id) ON DELETE SET NULL,
  ADD COLUMN creator_notes             text   NOT NULL DEFAULT '',   -- 作者备注 / .krp intro，只在后台显示
  ADD COLUMN source_format             text   NOT NULL DEFAULT 'manual' CHECK (source_format IN ('manual','st','krp')),
  ADD COLUMN extensions                jsonb  NOT NULL DEFAULT '{}'; -- 导入时不认识的字段原样保留

-- 表情包：图片存在 expression_folder_id 指向的媒体文件夹里。
CREATE TABLE card_expressions (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  card_id       uuid NOT NULL REFERENCES character_cards(id) ON DELETE CASCADE,
  key           text NOT NULL CHECK (key ~ '^[A-Za-z0-9_\-一-鿿]{1,32}$'),
  description   text NOT NULL DEFAULT '' CHECK (char_length(description) <= 200),
  media_item_id uuid NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  sort          int  NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (card_id, key)
);

-- bot：驱动模式、智核模型、记忆开关。
ALTER TABLE bots
  ADD COLUMN core_mode      text    NOT NULL DEFAULT 'single' CHECK (core_mode IN ('single','dual')),
  ADD COLUMN reason_model   text    NOT NULL DEFAULT '',     -- 空表示用全局 reason_model，再空用心核模型
  ADD COLUMN memory_enabled boolean NOT NULL DEFAULT true;

-- 全局设置：智核/记忆模型、记忆保留期、抽取间隔、跑偏检测开关。
ALTER TABLE bot_settings
  ADD COLUMN reason_model          text    NOT NULL DEFAULT '',
  ADD COLUMN memory_model          text    NOT NULL DEFAULT '',
  ADD COLUMN memory_retention_days int     NOT NULL DEFAULT 30 CHECK (memory_retention_days BETWEEN 1 AND 365),
  ADD COLUMN memory_extract_every  int     NOT NULL DEFAULT 20 CHECK (memory_extract_every BETWEEN 5 AND 200),
  ADD COLUMN ooc_guard             boolean NOT NULL DEFAULT true;

-- 用量按调用类型区分：reply=心核回复，reason=智核，memory=记忆抽取，retry=跑偏重写。
ALTER TABLE bot_usage
  ADD COLUMN kind text NOT NULL DEFAULT 'reply' CHECK (kind IN ('reply','reason','memory','retry'));

-- 长期记忆：每个 bot 对每个对方身份一行。
CREATE TABLE bot_peer_memories (
  bot_id              uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  peer_identity_id    uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  profile             jsonb    NOT NULL DEFAULT '{}',  -- {topic: {subtopic: {"v": 值, "t": RFC3339 更新时间}}}
  affinity            smallint NOT NULL DEFAULT 0 CHECK (affinity BETWEEN -100 AND 100),
  mood_label          text     NOT NULL DEFAULT '' CHECK (char_length(mood_label) <= 10),
  mood_value          smallint NOT NULL DEFAULT 5 CHECK (mood_value BETWEEN 0 AND 10),
  last_interaction_at timestamptz NOT NULL DEFAULT now(),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (bot_id, peer_identity_id)
);
CREATE INDEX bot_peer_memories_last_idx ON bot_peer_memories (last_interaction_at);

-- 事件时间线：每对最多保留 50 条（应用层裁剪）。
CREATE TABLE bot_peer_events (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  bot_id           uuid NOT NULL,
  peer_identity_id uuid NOT NULL,
  conversation_id  uuid REFERENCES conversations(id) ON DELETE SET NULL,
  gist             text NOT NULL CHECK (char_length(gist) BETWEEN 1 AND 120),
  tags             text[] NOT NULL DEFAULT '{}',
  created_at       timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (bot_id, peer_identity_id) REFERENCES bot_peer_memories(bot_id, peer_identity_id) ON DELETE CASCADE
);
CREATE INDEX bot_peer_events_pair_idx ON bot_peer_events (bot_id, peer_identity_id, created_at DESC);

-- 记忆抽取游标：每个 bot 会话一行，记录已抽取到的消息时间。
CREATE TABLE bot_memory_cursors (
  conversation_id uuid PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
  bot_id          uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  extracted_until timestamptz NOT NULL,
  fail_count      int NOT NULL DEFAULT 0,
  next_try_at     timestamptz,
  updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS bot_memory_cursors;
DROP TABLE IF EXISTS bot_peer_events;
DROP TABLE IF EXISTS bot_peer_memories;
ALTER TABLE bot_usage DROP COLUMN IF EXISTS kind;
ALTER TABLE bot_settings
  DROP COLUMN IF EXISTS ooc_guard, DROP COLUMN IF EXISTS memory_extract_every,
  DROP COLUMN IF EXISTS memory_retention_days, DROP COLUMN IF EXISTS memory_model,
  DROP COLUMN IF EXISTS reason_model;
ALTER TABLE bots DROP COLUMN IF EXISTS memory_enabled, DROP COLUMN IF EXISTS reason_model, DROP COLUMN IF EXISTS core_mode;
DROP TABLE IF EXISTS card_expressions;
ALTER TABLE character_cards
  DROP COLUMN IF EXISTS extensions, DROP COLUMN IF EXISTS source_format, DROP COLUMN IF EXISTS creator_notes,
  DROP COLUMN IF EXISTS expression_folder_id, DROP COLUMN IF EXISTS tools, DROP COLUMN IF EXISTS lorebook,
  DROP COLUMN IF EXISTS post_history_instructions, DROP COLUMN IF EXISTS alternate_greetings,
  DROP COLUMN IF EXISTS reason_prompt, DROP COLUMN IF EXISTS heart_prompt;
