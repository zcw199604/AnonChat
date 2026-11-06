-- +goose Up
-- 阅后即焚扫描每秒执行，为待焚毁消息建立部分索引避免全表扫描。
CREATE INDEX messages_burn_pending_idx
    ON messages (read_at)
    WHERE burned_at IS NULL AND read_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS messages_burn_pending_idx;
