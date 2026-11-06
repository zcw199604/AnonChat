-- +goose Up
-- 后台可管理的 bot 全局设置（how.md §15.1）。
-- 单行表：id 固定为 1；首次由服务启动时用环境变量初始化，之后以数据库为准。

CREATE TABLE bot_settings (
  id                     smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),   -- 单行表
  enabled                boolean NOT NULL DEFAULT false,                  -- 全局开关
  llm_base_url           text    NOT NULL DEFAULT '',
  llm_api_key_enc        bytea,                                           -- AES-256-GCM(nonce||ciphertext)，NULL 表示未设置
  llm_api_key_hint       text    NOT NULL DEFAULT '',                     -- 只存末 4 位，用于后台显示
  llm_api_key_base_url   text    NOT NULL DEFAULT '',                     -- key 绑定保存时的 Base URL（规范化），地址变了就不再使用该 key
  llm_model              text    NOT NULL DEFAULT '',
  llm_timeout_seconds    int     NOT NULL DEFAULT 60  CHECK (llm_timeout_seconds BETWEEN 5 AND 300),
  llm_send_cache_key     boolean NOT NULL DEFAULT true,
  match_fallback_seconds int     NOT NULL DEFAULT 10  CHECK (match_fallback_seconds BETWEEN 3 AND 600),
  bottle_min_age_seconds int     NOT NULL DEFAULT 1800 CHECK (bottle_min_age_seconds BETWEEN 0 AND 604800),
  timezone               text    NOT NULL DEFAULT 'Asia/Shanghai',
  updated_by             uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  updated_at             timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS bot_settings;
