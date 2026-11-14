# 数据模型与保留期

> 以代码为准。表结构见 `backend/migrations/*.sql` 与 `backend/internal/store/models.go`。
> 最后同步：外站匹配接入与用户媒体（202611131925）。来源：[实施方案](../plan/202611131925_external-match-bridge/how.md)。

## 表结构

### 身份与实名
- `verified_accounts`：`id uuid pk`、`phone_hmac bytea unique`、`phone_enc bytea`(AES-GCM nonce||ct)、`phone_masked text`、`created_at`。
- `identities`：`id uuid pk`、`token_hash bytea unique`、`nickname`、`avatar_seed`、`account_id → verified_accounts`、`created_ip inet`、`revoked_at`、`created_at`、`last_seen_at`；
  资料与权益字段（迁移 00004）：`public_code text`（匿名编号，10 位 Crockford Base32，唯一索引）、`gender(male|female|secret)`、`age smallint`(18–80，可空)、`province text`(可空)、`province_public boolean`、`tags text[]`(≤5)、`clean_mode boolean`、`match_gender(any|male|female)`、`match_same_province boolean`、`vip_until timestamptz`、`broadcast_cards int`(≥0)。
- `sms_codes`：`id bigserial`、`phone_hmac`、`code_hash`、`attempts`、`expires_at`、`consumed_at`、`created_at`。

### 会话与消息
- `conversations`：`id uuid pk`、`kind conversation_kind(match|room|bottle|private|story)`、`burn_after_read`、`ended_at`、`end_reason`、`created_at`。
- `conversation_members`：PK `(conversation_id, member_id)`、`identity_id`、`role`、`last_read_message_id`、`muted_until`(V2)、`joined_at`、`left_at`；唯一 `(conversation_id, identity_id)`。
- `messages`：`id bigserial pk`、`conversation_id`、`sender_identity_id`、`sender_member_id`、`client_msg_id uuid`、`kind(text|system|image|video)`、`body`、`media_id`、`read_at`、`burned_at`、`created_at`；唯一 `(sender_identity_id, client_msg_id)`。用户媒体仅允许 `match/bottle/private`，外站入站也经 `SendAs` 落库。

### 四种形态
- `rooms`：`id`、`conversation_id unique`、`slug unique`、`name`、`description`、`emoji`(默认 💬，迁移 00004)、`kind`、`visibility`、`max_members`、`sort_order`、`last_message_at`、`archived_at` 等。
- `bottles`：`id`、`identity_id`、`content`、`status(floating|held|removed)`、`held_by`、`held_until`、`expires_at`、`created_at`。
- `bottle_picks`：PK `(bottle_id, picker_identity_id)`、`conversation_id`(回复后)、`picked_at`。
- `private_links`：`id`、`token_hash unique`、`conversation_id unique`、`creator_identity_id`、`password_hash`(bcrypt)、`expires_at`、`destroyed_at`、`created_at`。

### 治理
- `blocks`：PK `(blocker_identity_id, blocked_identity_id)`。
- `reports`：`id`、`reporter_identity_id`、`reported_identity_id`、`conversation_id`(无外键)、`reason(spam|harassment|porn|illegal|other)`、`note`、`snapshot jsonb`、`status`、`handled_by`、`handled_at`、`target_type(message|bottle|broadcast|work)`、`target_id text`。
  消息快照保留既有 `id/memberId/nickname/body/createdAt/burnedAt/reported`，新增可选 `kind/mediaId`；仅保存媒体引用，不复制图片/视频字节，旧快照无需回填。
- `bans`：`subject_type(identity|ip|account)`、`subject_value`、`reason`、`expires_at`(NULL 永久)、`created_by`、`revoked_at`。
- `access_logs`：`identity_id`、`event`、`ip`、`user_agent`、`created_at`（合规留存）。

### 公告、广播与激活码（迁移 00004）
- `announcements`：`id`、`content`(1–120)、`starts_at`、`ends_at`、`created_by → admin_users`、`created_at`、`deleted_at`（软删除）。
- `broadcasts`：`id`、`identity_id`、`content`(1–60)、`status(pending|approved|rejected|removed)`、`reviewed_by → admin_users`、`reviewed_at`、`reject_reason`、`show_until`（通过时 now+24h）、`created_at`。
- `activation_batches`：`id`、`label`、`vip_days`(0–366)、`broadcast_cards`(0–100)、`code_count`(1–1000)、`expires_at`、`created_by → admin_users`、`created_at`（vip_days 与 broadcast_cards 不能同时为 0）。
- `activation_codes`：PK `code_hash bytea`(sha256(规范化后的码))、`batch_id`、`redeemed_by → identities`、`redeemed_at`、`revoked_at`。库里只存哈希，明文只在创建时返回一次。

### 故事书（迁移 00004）
- `stories`：`id`、`host_identity_id`、`title`(2–30)、`opening`(10–500)、`tags`、`role_a`(1–12)、`role_b`(1–12)、`host_role(a|b)`、`status(waiting|active|ended|expired|removed)`、`conversation_id unique → conversations`(ON DELETE SET NULL)、`guest_identity_id`、`active_at`、`ended_at`、`valid boolean`、`host_consent`、`guest_consent`、`expires_at`(等待态 24h)、`created_at`。
- `story_works`：`id`、`story_id unique → stories`、`title`、`opening`、`tags`、`role_a`、`role_b`、`lines jsonb`（`[{role,text,ts}]`，不含任何身份字段）、`status(pending|approved|rejected|removed)`、`like_count`、`reviewed_by`、`reviewed_at`、`reject_reason`、`created_at`。
- `story_work_likes`：PK `(work_id, identity_id)`、`created_at`。

### AI 机器人（迁移 00005）
- `character_cards`：`id`、`name`(1–40，只给模型看)、`description`、`personality`、`scenario`、`first_message`(为空时由模型生成开场)、`example_dialogue`、`system_prompt`、`created_at`、`updated_at`。
- `bots`：`id`、`identity_id unique → identities`(bot 以匿名身份参与，对外与真人完全一致)、`label`(后台备注名 1–40)、`card_id → character_cards`(SET NULL)、`media_folder_id → media_folders`(SET NULL)、`enabled`、`model`(空=用 `LLM_MODEL`)、`temperature`(0–2)、`max_output_tokens`(16–4000)、`serve_match`、`serve_bottle`、`max_active_matches`(≥1)；
  限额：`total_token_limit`、`total_reply_limit`、`window_seconds`(60–604800)、`window_token_limit`、`window_reply_limit`、`conv_reply_limit`、`conv_min_interval_seconds`、`conv_media_limit`（0 表示不限）；
  节奏：`reply_delay_min_ms`、`reply_delay_max_ms`、`typing_ms_per_char`、`bottle_interval_seconds`(≥60)；
  累计用量：`used_tokens`、`used_replies`（后台可重置）；`created_at`、`updated_at`。
- `bot_usage`：`id bigserial`、`bot_id → bots`、`conversation_id uuid`（**不加外键**：会话清理后用量仍保留）、`prompt_tokens`、`completion_tokens`、`cached_tokens`、`replies`(0/1)、`created_at`。
  token 用量 = `prompt_tokens + completion_tokens`；固定保留 30 天（`jobs`）。

### Bot 全局设置（迁移 00006）
- `bot_settings`：单行表（`id smallint pk CHECK (id=1)`）——`enabled`(全局开关)、`llm_base_url`、`llm_api_key_enc bytea`(AES-256-GCM(nonce||ciphertext)，NULL 表示未设置)、`llm_api_key_hint`(只存末 4 位)、`llm_api_key_base_url`(key 绑定保存时的 Base URL，地址变了该 key 不再使用)、`llm_model`、`llm_timeout_seconds`(5–300)、`llm_send_cache_key`、`match_fallback_seconds`(3–600)、`bottle_min_age_seconds`(0–604800)、`timezone`、`updated_by → admin_users`(SET NULL)、`updated_at`。
  首次初始化：服务启动时若表为空，用环境变量写入一次；之后以数据库为准（后台可热切换，无需重启）。

### 媒体（迁移 00005、00010）
- `media_folders`：`id`、`name unique`(1–40)、`created_at`。
- `media_items`：`id`、可空 `folder_id → media_folders`(CASCADE)、`kind(image|video)`、`mime`、`size_bytes`、`storage_key unique`、`original_name`、`tags text[]`(≤20 个，每个 ≤20 字)、`caption`(≤200，只给模型看)、`created_at`；00010 新增 `source(library|user|external)`（默认 library）与可空 `owner_identity_id → identities`（SET NULL）。CHECK 约束要求 `source=library` 当且仅当 folder_id 非空。
- 存储键相对 `MEDIA_ROOT`：媒体库保持 `<folder_id>/<item_id><ext>`；用户和外站媒体为 `user/YYYYMM/<item_id><ext>`、`external/YYYYMM/<item_id><ext>`（UTC 月份）。保存统一先写临时文件、嗅探 MIME、校验白名单后提交；用户大小上限由 `USER_MEDIA_MAX_UPLOAD_MB` 单独控制。
- 用户上传的 owner 为上传身份，library/external 来源的 owner 为空；外站媒体访问依靠消息引用和会话成员关系。用户发媒体必须匹配 source=user、owner 与 kind；尚未被任何消息引用的媒体允许 owner 预览，已发送媒体按成员/未焚毁消息规则访问。
- `messages.media_id` 在迁移 00005 加上外键 `→ media_items(id) ON DELETE SET NULL`；后台删除媒体仍会使历史引用置空（前端显示「媒体已失效」）。孤儿自动清理另用下述加锁协议防止并发发送产生空引用。

### 外站（迁移 00010）

- `external_settings`：单行表（id=1），`enabled/chat2019_enabled/wanfeng_enabled` 默认 false；`online_threshold` 默认5（0–100000）、`local_wait_seconds` 默认10（0–600）、`external_match_seconds` 默认10（3–120）、`chat2019_max_sessions/wanfeng_max_sessions` 默认20（1–1000）；`updated_by → admin_users`（SET NULL）、`updated_at`。启动时确保默认行存在，后台更新后立即刷新内存与通知 Bridge。
- `external_bindings`：主键 `(identity_id,provider)`，identity 外键 CASCADE；`provider(chat2019|wanfeng)`、`remote_uid`、`identity_token_enc`、`device_id`、`nickname`、`created_at/updated_at`。晚风 token 用 `SETTINGS_ENC_KEY` 进行 secretbox 加密（无密钥时晚风不可用），chat2019 token 列为 NULL；外站账号与昵称跨轮复用（出站昵称优先用本站昵称，`nickname` 作为含数字时的回退；迁移 00011 去掉了历史昵称的 `#编号` 后缀）。
- `external_peers`：主键 `(provider,remote_uid)`；`identity_id unique → identities`、`nickname`、`first_seen_at/last_seen_at`。同一外站对象复用同一影子身份，保障屏蔽语义。
- `external_conversations`：`conversation_id` 主键且引用 conversations（CASCADE）；`provider`、`user_identity_id/peer_identity_id → identities`、`remote_peer_uid`、`created_at`。与建 match 会话同一事务插入；实时 link 映射在内存中。

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
- `identities_public_code_key ON identities(public_code)`（唯一）
- `announcements_active_idx ON announcements(created_at DESC) WHERE deleted_at IS NULL`
- `broadcasts_status_show_until_idx ON broadcasts(status, show_until)`
- `activation_codes_batch_idx ON activation_codes(batch_id)`
- `stories_status_created_idx ON stories(status, created_at DESC)`、`stories_host_idx ON stories(host_identity_id)`
- `story_works_status_created_idx ON story_works(status, created_at DESC)`
- `media_items_folder_idx ON media_items(folder_id, created_at DESC)`
- `messages_media_id_idx ON messages(media_id) WHERE media_id IS NOT NULL`
- `media_items_nonlibrary_created_idx ON media_items(created_at) WHERE source <> 'library'`
- `external_conversations_user_idx ON external_conversations(user_identity_id, created_at DESC)`
- `bot_usage_bot_created_idx ON bot_usage(bot_id, created_at)`、`bot_usage_conv_idx ON bot_usage(conversation_id, created_at) WHERE conversation_id IS NOT NULL`

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
| 等待超时的 stories | 24 小时后置为 `expired` | 固定（每分钟扫描） |
| 结束且未生成作品的 stories | 结束后 7 天删除 | `MESSAGE_RETENTION` |
| broadcasts | 展示结束 7 天后，或已驳回/下架 7 天后删除 | `MESSAGE_RETENTION` |
| activation_codes | 已兑换的保留（财务凭证，不删除） | — |
| story_works / story_work_likes | 不受 7 天留存约束（双方同意后生成的公开内容） | — |
| bot_usage | 30 天 | 固定 |
| character_cards / bots / media_folders / source=library 的 media_items / bot_settings | 不自动删除（运营数据，由后台手动删除/覆盖） | — |
| source=user/external 的 media_items | 创建超过24小时且无未焚毁消息引用时清理行与文件 | 固定；每轮最多20批，每批500行 |
| external_settings / external_peers | 不自动删除；保留设置与影子身份映射 | — |
| external_bindings / external_conversations | 分别随身份 / 会话删除级联清理 | 外键 CASCADE |

清理任务每 `RETENTION_INTERVAL`（默认 10m）执行，messages/reports 按每批 5000 行循环。

`orphan_media` 在消息保留期清理之前执行，以 `(created_at,id)` 游标扫描，避免500条仍被引用的旧媒体挡住后续孤儿。每批使用 READ COMMITTED 事务：第一语句 `SELECT … FOR UPDATE SKIP LOCKED` 锁候选；第二语句 DELETE 重新检查不存在未焚毁引用，提交后删除文件。消息插入的外键键锁与清理互斥：发送先锁则清理跳过；清理先锁则插入可能返回「媒体已失效，请重新上传」，不会留下 media_id 被置空的新消息。刚被保留期删除消息释放的媒体由下一轮回收。

## 迁移列表（`backend/migrations/`）

1. `00001_init.sql`：V1 全部表、`conversation_kind` 枚举、索引。
2. `00002_burn_index.sql`：阅后即焚扫描的部分索引。
3. `00003_seed_rooms.sql`：6 个官方房间（lobby/late-night/gaming/study/emotion/work）。
4. `00004_profile_vip_story.sql`：身份资料与权益字段、`public_code` 回填与唯一索引、房间 `emoji`、`conversation_kind` 新增 `story`、公告、广播、激活码批次与激活码、故事与作品、举报 `target_type/target_id`。Down 迁移可完整回滚（含重建 `conversation_kind` 枚举）。
5. `00005_bots.sql`：`character_cards`、`media_folders`、`media_items`、`bots`、`bot_usage`，以及 `messages.media_id` 外键与部分索引。Down 按相反顺序删除（先 `bot_usage`、`bots`，再删外键/索引，最后 `media_items`、`media_folders`、`character_cards`）。
6. `00006_bot_settings.sql`：`bot_settings` 单行表（全局开关与模型接入配置，后台可热切换）。
7. `00007_bot_persona_mode.sql`：历史 persona_mode 及 bot 输出上限默认值，当前运行时约定见下。
8. `00008_rate_limit_rules.sql`：可配置限流规则，见下。
9. `00009_bot_persona_memory.sql`：人设、表情与长期记忆，见下。
10. `00010_external_bridge.sql`：四张外站表、媒体 source/owner、folder 可空约束及索引。单步回滚到00009删除非媒体库媒体行，磁盘 `MEDIA_ROOT/user` 与 `MEDIA_ROOT/external` 需人工清理；回滚前关闭总开关并备份数据库和媒体卷。

- `bot_settings.persona_mode`（迁移 00007）：数据库保留 `user` / `system` 以兼容旧数据，运行时和保存时统一使用 `user`，后台不再提供切换选项。角色设定及额外系统指令每轮放进第一条 user 消息；旧 `system` 值无需手动迁移。`BuildMessages` 仍支持显式 `system`，仅供独立诊断脚本对比。`bots.max_output_tokens` 默认值为 1024（思考型模型需要更大的输出额度）。

### 限流规则（迁移 00008）

- `rate_limit_rules`：`key text pk`、`limit_count int >=1`、`window_seconds int >=0`、`ban_seconds int >=-1`、`enabled boolean`、`updated_by → admin_users`（SET NULL）、`updated_at`。
- 迁移插入全部默认规则；未知数据库 key 不参与运行，缺失 key 从代码默认值补齐。频率规则窗口至少 1 秒，并发规则窗口必须 0；不支持自动封禁的规则要求 `ban_seconds=0`。
- `identities(created_ip, created_at)` 索引用于创建/换身份滚动计数；计数包含已撤销记录。清理保留期为 `AUDIT_RETENTION`，身份窗口不得超过该值。

### 人设、表情与长期记忆（迁移 00009）

依据：[Bot 人设包、双核驱动与长期记忆方案](../plan/202611092002_bot-persona-memory/how.md)；实际结构以 `00009_bot_persona_memory.sql` 为准，带完整 Down。

- `character_cards` 新增 `heart_prompt`、`reason_prompt`、`alternate_greetings text[]`、`post_history_instructions`、`lorebook jsonb`、`tools text[]`、`expression_folder_id → media_folders`（SET NULL）、`creator_notes`、`source_format(manual|st|krp)`、`extensions jsonb`。来源和表情数只读；复制新卡不复制表情媒体，来源为 manual。
- `card_expressions`：`id`、`card_id → character_cards`（CASCADE）、`key`（每卡唯一，1–32 字）、`description`（≤200 字）、`media_item_id → media_items`（CASCADE）、`sort`、`created_at`。删除表情同时删除媒体条目；删除卡片同时清理表情文件夹。表情按普通 image 发送，与其他媒体共用 `conv_media_limit`，允许重复，每轮最多一张。
- `bots` 新增 `core_mode(single|dual)`（默认 single）、`reason_model`（默认空）、`memory_enabled`（默认 true）。
- `bot_settings` 新增 `reason_model`、`memory_model`（均默认空）、`memory_retention_days`（默认30，1–365）、`memory_extract_every`（默认20，5–200）、`ooc_guard`（默认true）。无新环境变量；模型为空按方案 §2 回退。
- `bot_usage.kind`：`reply`、`reason`、`memory`、`retry`，全部消耗计入总量与窗口；只有发送成功的 reply 计回复数。后台统计 `usage.windowReasonTokens` / `usage.windowMemoryTokens`，`memoryCount` 在 bot 顶层。
- `bot_peer_memories`：主键 `(bot_id, peer_identity_id)`，两者外键均 CASCADE；`profile jsonb`、`affinity`（-100–100）、`mood_label`（≤10字）、`mood_value`（0–10）、`last_interaction_at`、`created_at`、`updated_at`。画像仅含 basic_info、interest、life、psychological、relationship、preference；每 topic 最多10子项，整体≤6000字节。画像值与事件摘要入库前清洗邮箱和长数字。
- `bot_peer_events`：`id`、上述双键（引用画像，CASCADE）、`conversation_id`（SET NULL）、`gist`（1–120字）、`tags text[]`、`created_at`；每对裁剪为最新50条，回复注入最近8条。
- `bot_memory_cursors`：`conversation_id` 主键（CASCADE）、`bot_id`（CASCADE）、`extracted_until`、`fail_count`、`next_try_at`、`updated_at`。失败退避，超限延后10分钟但不推进游标；已焚毁消息不进入候选输入。worker 每60秒串行处理至多20个候选。
- 索引：`bot_peer_memories(last_interaction_at)`、`bot_peer_events(bot_id, peer_identity_id, created_at DESC)`。

| 新增清理项 | 保留规则 |
|---|---|
| 画像及其事件 | 最后互动后默认30天，取 `bot_settings.memory_retention_days`；无设置行也按30天 |
| 吊销身份的画像及事件 | 下次清理即删除，不与新匿名身份合并 |
| 记忆游标 | 对应会话结束超过8天删除 |

三项独立计数与日志，单项失败不阻止其他项。后台记忆接口挂 `RequireAdmin`，查看详情、删除、清空均审计；审计不记录画像或事件内容。
