-- +goose Up
-- 人设注入方式：部分 OpenAI 兼容网关会丢弃或改写开头的 system 消息，
-- user 方式把角色设定放进第一条 user 消息，兼容性最好（默认）；system 方式适合官方接口。
ALTER TABLE bot_settings
  ADD COLUMN persona_mode text NOT NULL DEFAULT 'user' CHECK (persona_mode IN ('user','system'));

-- 思考型模型会先消耗输出额度做推理，300 太小容易导致正文为空；新建 bot 的默认值改为 1024。
ALTER TABLE bots ALTER COLUMN max_output_tokens SET DEFAULT 1024;

-- +goose Down
ALTER TABLE bots ALTER COLUMN max_output_tokens SET DEFAULT 300;
ALTER TABLE bot_settings DROP COLUMN IF EXISTS persona_mode;
