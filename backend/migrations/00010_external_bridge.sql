-- +goose Up

-- 外站接入全局设置（单行表，id 固定为 1；首次启动按默认值插入）。
CREATE TABLE external_settings (
  id                      smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  enabled                 boolean NOT NULL DEFAULT false,           -- 总开关
  chat2019_enabled        boolean NOT NULL DEFAULT false,
  wanfeng_enabled         boolean NOT NULL DEFAULT false,
  online_threshold        int NOT NULL DEFAULT 5  CHECK (online_threshold BETWEEN 0 AND 100000),
  local_wait_seconds      int NOT NULL DEFAULT 10 CHECK (local_wait_seconds BETWEEN 0 AND 600),
  external_match_seconds  int NOT NULL DEFAULT 10 CHECK (external_match_seconds BETWEEN 3 AND 120),
  chat2019_max_sessions   int NOT NULL DEFAULT 20 CHECK (chat2019_max_sessions BETWEEN 1 AND 1000),
  wanfeng_max_sessions    int NOT NULL DEFAULT 20 CHECK (wanfeng_max_sessions BETWEEN 1 AND 1000),
  updated_by              uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  updated_at              timestamptz NOT NULL DEFAULT now()
);

-- 本站身份在外站的账号（每个身份每个站点一个，落库复用，避免反复注册触发风控）。
CREATE TABLE external_bindings (
  identity_id         uuid NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  provider            text NOT NULL CHECK (provider IN ('chat2019','wanfeng')),
  remote_uid          text NOT NULL,
  identity_token_enc  bytea,                     -- 晚风 identityToken，secretbox(SETTINGS_ENC_KEY)；chat2019 为 NULL
  device_id           text NOT NULL DEFAULT '',  -- 晚风每身份独立的 deviceId
  nickname            text NOT NULL DEFAULT '',  -- 绑定时随机生成，不使用本站昵称
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (identity_id, provider)
);

-- 外站对方 → 本站影子身份（同一外站用户复用同一影子身份，拉黑才能生效）。
CREATE TABLE external_peers (
  provider      text NOT NULL CHECK (provider IN ('chat2019','wanfeng')),
  remote_uid    text NOT NULL,
  identity_id   uuid NOT NULL UNIQUE REFERENCES identities(id),
  nickname      text NOT NULL DEFAULT '',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, remote_uid)
);

-- 外站会话：本站会话 ↔ 外站对方（排查和统计用；运行时映射在内存里）。
CREATE TABLE external_conversations (
  conversation_id   uuid PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
  provider          text NOT NULL CHECK (provider IN ('chat2019','wanfeng')),
  user_identity_id  uuid NOT NULL REFERENCES identities(id),
  peer_identity_id  uuid NOT NULL REFERENCES identities(id),
  remote_peer_uid   text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX external_conversations_user_idx ON external_conversations (user_identity_id, created_at DESC);

-- 媒体来源：library（后台媒体库，必须有 folder）/ user（用户上传）/ external（外站下载）。
ALTER TABLE media_items ALTER COLUMN folder_id DROP NOT NULL;
ALTER TABLE media_items
  ADD COLUMN source text NOT NULL DEFAULT 'library' CHECK (source IN ('library','user','external')),
  ADD COLUMN owner_identity_id uuid REFERENCES identities(id) ON DELETE SET NULL;
ALTER TABLE media_items
  ADD CONSTRAINT media_items_folder_source_chk CHECK ((source = 'library') = (folder_id IS NOT NULL));
CREATE INDEX media_items_nonlibrary_created_idx ON media_items (created_at) WHERE source <> 'library';

-- +goose Down
DROP INDEX IF EXISTS media_items_nonlibrary_created_idx;
-- 非媒体库的媒体行在回滚时删除（磁盘文件需人工清理 MEDIA_ROOT/user、MEDIA_ROOT/external，deploy 文档注明）。
DELETE FROM media_items WHERE source <> 'library';
ALTER TABLE media_items DROP CONSTRAINT IF EXISTS media_items_folder_source_chk;
ALTER TABLE media_items DROP COLUMN IF EXISTS owner_identity_id;
ALTER TABLE media_items DROP COLUMN IF EXISTS source;
ALTER TABLE media_items ALTER COLUMN folder_id SET NOT NULL;
DROP TABLE IF EXISTS external_conversations;
DROP TABLE IF EXISTS external_peers;
DROP TABLE IF EXISTS external_bindings;
DROP TABLE IF EXISTS external_settings;
