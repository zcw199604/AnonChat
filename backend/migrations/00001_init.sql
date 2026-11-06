-- +goose Up
-- V1 初始化 schema：包含 how.md「数据模型」中 V1 的全部表、类型和索引。
-- 不含 V2 新增的表（media / sensitive_words / push_subscriptions）。

-- 会话类型：match 随机匹配、room 聊天室、bottle 漂流瓶、private 私密链接。
CREATE TYPE conversation_kind AS ENUM ('match', 'room', 'bottle', 'private');

-- 实名账号：手机号以密文 + HMAC 形式存储。
CREATE TABLE verified_accounts (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  phone_hmac    bytea NOT NULL UNIQUE,
  phone_enc     bytea NOT NULL,          -- AES-256-GCM(nonce||ciphertext)
  phone_masked  text  NOT NULL,          -- 138****1234
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- 匿名身份：由 Cookie 中的不透明令牌标识，库里只存令牌哈希。
CREATE TABLE identities (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash    bytea NOT NULL UNIQUE,
  nickname      text  NOT NULL,
  avatar_seed   text  NOT NULL,
  account_id    uuid REFERENCES verified_accounts(id),
  created_ip    inet,
  revoked_at    timestamptz,             -- 换身份后置为非空
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now()
);

-- 短信验证码。
CREATE TABLE sms_codes (
  id           bigserial PRIMARY KEY,
  phone_hmac   bytea NOT NULL,
  code_hash    bytea NOT NULL,
  attempts     int NOT NULL DEFAULT 0,
  expires_at   timestamptz NOT NULL,
  consumed_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- 会话：四种聊天形态共用的消息容器。
CREATE TABLE conversations (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind        conversation_kind NOT NULL,
  burn_after_read boolean NOT NULL DEFAULT false,
  ended_at    timestamptz,
  end_reason  text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- 会话成员：对外只暴露会话内别名 member_id。
CREATE TABLE conversation_members (
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  member_id       int  NOT NULL,          -- 会话内的别名序号，对外只暴露这个值
  identity_id     uuid NOT NULL REFERENCES identities(id),
  role            text NOT NULL DEFAULT 'member',   -- member | owner(V2 房间)
  last_read_message_id bigint,
  muted_until     timestamptz,            -- V2 房间禁言
  joined_at       timestamptz NOT NULL DEFAULT now(),
  left_at         timestamptz,
  PRIMARY KEY (conversation_id, member_id),
  UNIQUE (conversation_id, identity_id)
);
CREATE INDEX ON conversation_members (identity_id) WHERE left_at IS NULL;

-- 消息：热点表，按会话 + id 及 created_at 建索引。
CREATE TABLE messages (
  id               bigserial PRIMARY KEY,
  conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  sender_identity_id uuid NOT NULL REFERENCES identities(id),
  sender_member_id int NOT NULL,
  client_msg_id    uuid NOT NULL,
  kind             text NOT NULL DEFAULT 'text',     -- text | system | image(V2)
  body             text NOT NULL,
  media_id         uuid,                             -- V2
  read_at          timestamptz,
  burned_at        timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (sender_identity_id, client_msg_id)
);
CREATE INDEX ON messages (conversation_id, id);
CREATE INDEX ON messages (created_at);

-- 聊天室：每个房间对应一个长期会话。
CREATE TABLE rooms (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id uuid NOT NULL UNIQUE REFERENCES conversations(id),
  slug            text NOT NULL UNIQUE,
  name            text NOT NULL,
  description     text NOT NULL DEFAULT '',
  kind            text NOT NULL DEFAULT 'official',  -- official | user(V2)
  visibility      text NOT NULL DEFAULT 'public',    -- public | private(V2)
  owner_identity_id uuid REFERENCES identities(id),  -- V2
  password_hash   text,                               -- V2
  invite_token_hash bytea UNIQUE,                     -- V2
  max_members     int  NOT NULL DEFAULT 500,
  sort_order      int  NOT NULL DEFAULT 0,
  last_message_at timestamptz,
  archived_at     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- 漂流瓶。
CREATE TABLE bottles (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  identity_id   uuid NOT NULL REFERENCES identities(id),
  content       text NOT NULL,
  status        text NOT NULL DEFAULT 'floating',  -- floating | held | removed
  held_by       uuid REFERENCES identities(id),
  held_until    timestamptz,                       -- 捞起后 10 分钟内不处理就自动回到池中
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON bottles (status, expires_at);

-- 漂流瓶捞取记录。
CREATE TABLE bottle_picks (
  bottle_id        uuid NOT NULL REFERENCES bottles(id) ON DELETE CASCADE,
  picker_identity_id uuid NOT NULL REFERENCES identities(id),
  conversation_id  uuid REFERENCES conversations(id),   -- 回复后才有
  picked_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (bottle_id, picker_identity_id)
);

-- 私密链接：链接对应的会话最多两人。
CREATE TABLE private_links (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash      bytea NOT NULL UNIQUE,
  conversation_id uuid NOT NULL UNIQUE REFERENCES conversations(id),
  creator_identity_id uuid NOT NULL REFERENCES identities(id),
  password_hash   text,
  expires_at      timestamptz NOT NULL,
  destroyed_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- 屏蔽关系。
CREATE TABLE blocks (
  blocker_identity_id uuid NOT NULL REFERENCES identities(id),
  blocked_identity_id uuid NOT NULL REFERENCES identities(id),
  created_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (blocker_identity_id, blocked_identity_id)
);

-- 举报：conversation_id 不加外键，会话清理后举报仍然保留。
CREATE TABLE reports (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_identity_id uuid NOT NULL REFERENCES identities(id),
  reported_identity_id uuid NOT NULL REFERENCES identities(id),
  conversation_id     uuid,              -- 不加外键：会话清理后举报仍然保留
  reason              text NOT NULL,     -- spam | harassment | porn | illegal | other
  note                text,
  snapshot            jsonb NOT NULL,    -- 被举报消息及前后各 10 条上下文
  status              text NOT NULL DEFAULT 'open',
  handled_by          uuid,
  handled_at          timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now()
);

-- 封禁：按身份、IP 或实名账号。
CREATE TABLE bans (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_type  text NOT NULL,           -- identity | ip | account
  subject_value text NOT NULL,
  reason        text NOT NULL,
  expires_at    timestamptz,             -- NULL 表示永久
  created_by    uuid,
  revoked_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON bans (subject_type, subject_value) WHERE revoked_at IS NULL;

-- 访问审计日志：合规留存，默认 180 天。
CREATE TABLE access_logs (
  id          bigserial PRIMARY KEY,
  identity_id uuid,
  event       text NOT NULL,           -- identity_created | ws_connect | ws_disconnect | verify | report ...
  ip          inet,
  user_agent  text,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON access_logs (created_at);

-- 管理员账号。
CREATE TABLE admin_users (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  username      text NOT NULL UNIQUE,
  password_hash text NOT NULL,          -- bcrypt cost 12
  totp_secret_enc bytea,                -- V2
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- 管理员会话。
CREATE TABLE admin_sessions (
  token_hash bytea PRIMARY KEY,
  admin_id   uuid NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- 管理员操作审计日志。
CREATE TABLE admin_audit_logs (
  id        bigserial PRIMARY KEY,
  admin_id  uuid NOT NULL,
  action    text NOT NULL,
  target    text,
  detail    jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS admin_sessions;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS access_logs;
DROP TABLE IF EXISTS bans;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS blocks;
DROP TABLE IF EXISTS private_links;
DROP TABLE IF EXISTS bottle_picks;
DROP TABLE IF EXISTS bottles;
DROP TABLE IF EXISTS rooms;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversation_members;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS sms_codes;
DROP TABLE IF EXISTS identities;
DROP TABLE IF EXISTS verified_accounts;
DROP TYPE IF EXISTS conversation_kind;
