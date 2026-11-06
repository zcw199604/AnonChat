-- +goose Up
-- 本包（界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书）的库结构。
-- 一次性建好 U2/U3/U5 需要的全部表和字段（how.md 3.1）。

-- ---- 身份资料与权益 ----
ALTER TABLE identities
  ADD COLUMN public_code text,                 -- 匿名编号：10 位 Crockford Base32，大写
  ADD COLUMN gender text NOT NULL DEFAULT 'secret' CHECK (gender IN ('male','female','secret')),
  ADD COLUMN age smallint CHECK (age IS NULL OR age BETWEEN 18 AND 80),
  ADD COLUMN province text,                    -- 取值必须在 provinces 常量中，NULL 表示未设置
  ADD COLUMN province_public boolean NOT NULL DEFAULT false,
  ADD COLUMN tags text[] NOT NULL DEFAULT '{}',
  ADD COLUMN clean_mode boolean NOT NULL DEFAULT false,
  ADD COLUMN match_gender text NOT NULL DEFAULT 'any' CHECK (match_gender IN ('any','male','female')),
  ADD COLUMN match_same_province boolean NOT NULL DEFAULT false,
  ADD COLUMN vip_until timestamptz,
  ADD COLUMN broadcast_cards int NOT NULL DEFAULT 0 CHECK (broadcast_cards >= 0);

-- 存量身份回填匿名编号（十六进制大写，同样 10 位，只用于存量数据）。
UPDATE identities
SET public_code = upper(substr(replace(gen_random_uuid()::text, '-', ''), 1, 10))
WHERE public_code IS NULL;
ALTER TABLE identities ALTER COLUMN public_code SET NOT NULL;
CREATE UNIQUE INDEX identities_public_code_key ON identities (public_code);

-- ---- 房间 emoji ----
ALTER TABLE rooms ADD COLUMN emoji text NOT NULL DEFAULT '💬';
UPDATE rooms SET emoji = '💬' WHERE slug = 'lobby';
UPDATE rooms SET emoji = '🌙' WHERE slug = 'late-night';
UPDATE rooms SET emoji = '🎮' WHERE slug = 'gaming';
UPDATE rooms SET emoji = '📚' WHERE slug = 'study';
UPDATE rooms SET emoji = '💗' WHERE slug = 'emotion';
UPDATE rooms SET emoji = '💼' WHERE slug = 'work';

-- ---- 会话类型新增 story ----
ALTER TYPE conversation_kind ADD VALUE IF NOT EXISTS 'story';

-- ---- 公告（管理员发布）----
CREATE TABLE announcements (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  content    text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 120),
  starts_at  timestamptz NOT NULL DEFAULT now(),
  ends_at    timestamptz,
  created_by uuid NOT NULL REFERENCES admin_users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX announcements_active_idx ON announcements (created_at DESC) WHERE deleted_at IS NULL;

-- ---- 广播（用户使用广播卡提交，人工审核）----
CREATE TABLE broadcasts (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  identity_id   uuid NOT NULL REFERENCES identities(id),
  content       text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 60),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','removed')),
  reviewed_by   uuid REFERENCES admin_users(id),
  reviewed_at   timestamptz,
  reject_reason text,
  show_until    timestamptz,          -- 审核通过时设置为 now()+24h
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX broadcasts_status_show_until_idx ON broadcasts (status, show_until);

-- ---- 激活码 ----
CREATE TABLE activation_batches (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  label           text NOT NULL,
  vip_days        int  NOT NULL CHECK (vip_days BETWEEN 0 AND 366),
  broadcast_cards int  NOT NULL CHECK (broadcast_cards BETWEEN 0 AND 100),
  code_count      int  NOT NULL CHECK (code_count BETWEEN 1 AND 1000),
  expires_at      timestamptz NOT NULL,      -- 激活码的兑换截止时间
  created_by      uuid NOT NULL REFERENCES admin_users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (vip_days > 0 OR broadcast_cards > 0)
);
CREATE TABLE activation_codes (
  code_hash    bytea PRIMARY KEY,            -- sha256(规范化后的激活码)
  batch_id     uuid NOT NULL REFERENCES activation_batches(id),
  redeemed_by  uuid REFERENCES identities(id),
  redeemed_at  timestamptz,
  revoked_at   timestamptz
);
CREATE INDEX activation_codes_batch_idx ON activation_codes (batch_id);

-- ---- 故事书 ----
CREATE TABLE stories (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  host_identity_id  uuid NOT NULL REFERENCES identities(id),
  title             text NOT NULL CHECK (char_length(title) BETWEEN 2 AND 30),
  opening           text NOT NULL CHECK (char_length(opening) BETWEEN 10 AND 500),
  tags              text[] NOT NULL DEFAULT '{}',
  role_a            text NOT NULL CHECK (char_length(role_a) BETWEEN 1 AND 12),
  role_b            text NOT NULL CHECK (char_length(role_b) BETWEEN 1 AND 12),
  host_role         text NOT NULL CHECK (host_role IN ('a','b')),
  status            text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting','active','ended','expired','removed')),
  conversation_id   uuid UNIQUE REFERENCES conversations(id) ON DELETE SET NULL,
  guest_identity_id uuid REFERENCES identities(id),
  active_at         timestamptz,
  ended_at          timestamptz,
  valid             boolean,                  -- 结束时由服务端判定
  host_consent      boolean NOT NULL DEFAULT false,
  guest_consent     boolean NOT NULL DEFAULT false,
  expires_at        timestamptz NOT NULL,     -- 等待态 24h 后过期
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stories_status_created_idx ON stories (status, created_at DESC);
CREATE INDEX stories_host_idx ON stories (host_identity_id);

CREATE TABLE story_works (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  story_id      uuid NOT NULL UNIQUE REFERENCES stories(id),
  title         text NOT NULL,
  opening       text NOT NULL,
  tags          text[] NOT NULL DEFAULT '{}',
  role_a        text NOT NULL,
  role_b        text NOT NULL,
  lines         jsonb NOT NULL,              -- [{"role":"a|b","text":"...","ts":unix}]，不含任何身份字段
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','removed')),
  like_count    int NOT NULL DEFAULT 0,
  reviewed_by   uuid REFERENCES admin_users(id),
  reviewed_at   timestamptz,
  reject_reason text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX story_works_status_created_idx ON story_works (status, created_at DESC);

CREATE TABLE story_work_likes (
  work_id     uuid NOT NULL REFERENCES story_works(id) ON DELETE CASCADE,
  identity_id uuid NOT NULL REFERENCES identities(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (work_id, identity_id)
);

-- ---- 举报扩展：举报对象可以是作品或广播 ----
ALTER TABLE reports ADD COLUMN target_type text NOT NULL DEFAULT 'message'
  CHECK (target_type IN ('message','bottle','broadcast','work'));
ALTER TABLE reports ADD COLUMN target_id text;

-- +goose Down
-- 完整回滚：先删依赖表，再还原枚举与列。

-- 故事书
DROP TABLE IF EXISTS story_work_likes;
DROP TABLE IF EXISTS story_works;
DROP TABLE IF EXISTS stories;

-- 删除 story 类型的会话（枚举值无法单独删除，需要重建类型）
DELETE FROM conversation_members
WHERE conversation_id IN (SELECT id FROM conversations WHERE kind = 'story');
DELETE FROM messages
WHERE conversation_id IN (SELECT id FROM conversations WHERE kind = 'story');
DELETE FROM conversations WHERE kind = 'story';

-- 激活码
DROP TABLE IF EXISTS activation_codes;
DROP TABLE IF EXISTS activation_batches;

-- 广播与公告
DROP TABLE IF EXISTS broadcasts;
DROP TABLE IF EXISTS announcements;

-- 举报扩展字段
ALTER TABLE reports DROP COLUMN IF EXISTS target_id;
ALTER TABLE reports DROP COLUMN IF EXISTS target_type;

-- 房间 emoji
ALTER TABLE rooms DROP COLUMN IF EXISTS emoji;

-- 还原对话类型枚举（去掉 story）
-- +goose StatementBegin
ALTER TYPE conversation_kind RENAME TO conversation_kind_old;
CREATE TYPE conversation_kind AS ENUM ('match', 'room', 'bottle', 'private');
ALTER TABLE conversations
  ALTER COLUMN kind TYPE conversation_kind USING kind::text::conversation_kind;
DROP TYPE conversation_kind_old;
-- +goose StatementEnd

-- 身份资料与权益
DROP INDEX IF EXISTS identities_public_code_key;
ALTER TABLE identities
  DROP COLUMN IF EXISTS broadcast_cards,
  DROP COLUMN IF EXISTS vip_until,
  DROP COLUMN IF EXISTS match_same_province,
  DROP COLUMN IF EXISTS match_gender,
  DROP COLUMN IF EXISTS clean_mode,
  DROP COLUMN IF EXISTS tags,
  DROP COLUMN IF EXISTS province_public,
  DROP COLUMN IF EXISTS province,
  DROP COLUMN IF EXISTS age,
  DROP COLUMN IF EXISTS gender,
  DROP COLUMN IF EXISTS public_code;
