# 数据模型与保留期

> 以代码为准，最后同步于 V1。表结构见 `backend/migrations/*.sql` 与 `backend/internal/store/models.go`。

## 表结构

### 身份与实名
- `verified_accounts`：`id uuid pk`、`phone_hmac bytea unique`、`phone_enc bytea`(AES-GCM nonce||ct)、`phone_masked text`、`created_at`。
- `identities`：`id uuid pk`、`token_hash bytea unique`、`nickname`、`avatar_seed`、`account_id → verified_accounts`、`created_ip inet`、`revoked_at`、`created_at`、`last_seen_at`。
- `sms_codes`：`id bigserial`、`phone_hmac`、`code_hash`、`attempts`、`expires_at`、`consumed_at`、`created_at`。

### 会话与消息
- `conversations`：`id uuid pk`、`kind conversation_kind(match|room|bottle|private)`、`burn_after_read`、`ended_at`、`end_reason`、`created_at`。
- `conversation_members`：PK `(conversation_id, member_id)`、`identity_id`、`role`、`last_read_message_id`、`muted_until`(V2)、`joined_at`、`left_at`；唯一 `(conversation_id, identity_id)`。
- `messages`：`id bigserial pk`、`conversation_id`、`sender_identity_id`、`sender_member_id`、`client_msg_id uuid`、`kind(text|system|image V2)`、`body`、`media_id`(V2)、`read_at`、`burned_at`、`created_at`；唯一 `(sender_identity_id, client_msg_id)`。

### 四种形态
- `rooms`：`id`、`conversation_id unique`、`slug unique`、`name`、`description`、`kind`、`visibility`、`max_members`、`sort_order`、`last_message_at`、`archived_at` 等。
- `bottles`：`id`、`identity_id`、`content`、`status(floating|held|removed)`、`held_by`、`held_until`、`expires_at`、`created_at`。
- `bottle_picks`：PK `(bottle_id, picker_identity_id)`、`conversation_id`(回复后)、`picked_at`。
- `private_links`：`id`、`token_hash unique`、`conversation_id unique`、`creator_identity_id`、`password_hash`(bcrypt)、`expires_at`、`destroyed_at`、`created_at`。

### 治理
- `blocks`：PK `(blocker_identity_id, blocked_identity_id)`。
- `reports`：`id`、`reporter_identity_id`、`reported_identity_id`、`conversation_id`(无外键)、`reason(spam|harassment|porn|illegal|other)`、`note`、`snapshot jsonb`、`status`、`handled_by`、`handled_at`。
- `bans`：`subject_type(identity|ip|account)`、`subject_value`、`reason`、`expires_at`(NULL 永久)、`created_by`、`revoked_at`。
- `access_logs`：`identity_id`、`event`、`ip`、`user_agent`、`created_at`（合规留存）。

### 管理后台
- `admin_users`：`id`、`username unique`、`password_hash`(bcrypt cost12)、`totp_secret_enc`(V2)。
- `admin_sessions`：PK `token_hash`、`admin_id`、`expires_at`、`created_at`。
- `admin_audit_logs`：`id bigserial`、`admin_id`(登录失败用全零 UUID)、`action`、`target`、`detail jsonb`、`created_at`。

## 索引（除主键/唯一约束外）

- `conversation_members (identity_id) WHERE left_at IS NULL`
- `messages (conversation_id, id)`、`messages (created_at)`
- `messages_burn_pending_idx ON messages(read_at) WHERE burned_at IS NULL AND read_at IS NOT NULL`（阅后即焚扫描）
- `bottles (status, expires_at)`
- `bans (subject_type, subject_value) WHERE revoked_at IS NULL`
- `access_logs (created_at)`

## 保留期（`jobs`，均可配置）

| 数据 | 默认保留 | 配置 |
|---|---|---|
| messages（含已焚毁） | 7 天 | `MESSAGE_RETENTION=168h` |
| 已结束且消息为空的 conversations | 结束后 7 天 | `MESSAGE_RETENTION` |
| bottles / bottle_picks | 过期/7 天 | `BOTTLE_TTL=168h` |
| private_links | 过期或销毁后 7 天 | `BOTTLE_TTL` |
| sms_codes | 1 天 | 固定 |
| access_logs | 180 天 | `AUDIT_RETENTION=4320h` |
| reports（快照） | 180 天 | `AUDIT_RETENTION` |
| admin_sessions | 过期即清 | — |
| 已撤销且无引用的 identities | 180 天 | `AUDIT_RETENTION` |

清理任务每 `RETENTION_INTERVAL`（默认 10m）执行，messages/reports 按每批 5000 行循环。

## 迁移列表（`backend/migrations/`）

1. `00001_init.sql`：V1 全部表、`conversation_kind` 枚举、索引。
2. `00002_burn_index.sql`：阅后即焚扫描的部分索引。
3. `00003_seed_rooms.sql`：6 个官方房间（lobby/late-night/gaming/study/emotion/work）。
