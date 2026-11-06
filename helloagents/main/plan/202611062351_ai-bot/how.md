# 技术设计: AI 机器人（Bot）

> 本设计是 V1 和 U 包之上的增量。没有提到的部分（技术栈、目录约定、错误处理、限流框架、sqlc、goose、前端风格）一律沿用现状，不得改动。
> 所有代码注释和文档使用简体中文，风格与现有代码一致。

## 0. 硬性约束（实施时逐条自查）

1. **不暴露 bot 身份**：`match.found`、`msg.new`、`typing`、历史消息、会话列表、`/api/identity/*` 等所有用户侧接口不得新增任何能区分 bot 的字段。bot 的消息必须走与真人相同的落库和广播路径。
2. **默认提示词只要求"以角色身份自然聊天"**，不要写"被问是不是 AI 时否认"之类的指令。角色卡内容由运营者自行填写。
3. 不修改已有接口的语义；`msg.new` 和历史消息只**新增**可选字段 `mediaUrl`。
4. 新依赖：**不引入任何新的 Go 依赖**（LLM 客户端用 `net/http` 自己实现）。前端也不新增依赖。
5. 迁移只做加法：新表、新索引，以及给 `messages.media_id` 加外键。
6. 日志中不得出现 API Key 和消息正文；LLM 错误响应体最多记录前 200 字节。
7. 不提交 git commit。

## 1. 数据模型（`backend/migrations/00005_bots.sql`）

```sql
-- +goose Up
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
```

Down 部分按相反顺序删除：`bot_usage`、`bots`、外键约束和索引、`media_items`、`media_folders`、`character_cards`。

- token 用量 = `prompt_tokens + completion_tokens`。
- 留存：`jobs/cleaner.go` 增加一项，删除 30 天前的 `bot_usage`（窗口上限最多 7 天，不受影响）。
- bot 的 identity 不会被吊销，所以不会被身份清理任务删除。删除 bot 时才吊销它的 identity。
- `testutil.Truncate` 要加上这些新表。
- 新增查询分别写在 `queries/bot.sql` 和 `queries/media.sql` 里，用 `make sqlc` 生成代码；生成的文件要一起提交到工作区。

## 2. 配置（`internal/config`）

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `BOT_ENABLED` | `false` | 为 `true` 时启动 bot 引擎（worker、匹配兜底、漂流瓶循环）。管理接口始终可用 |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI 兼容接口地址（不带 `/chat/completions`） |
| `LLM_API_KEY` | 空 | `BOT_ENABLED=true` 时必填 |
| `LLM_MODEL` | 空 | 默认模型；`BOT_ENABLED=true` 时必填 |
| `LLM_TIMEOUT` | `60s` | 单次请求超时 |
| `LLM_SEND_CACHE_KEY` | `true` | 是否在请求中带 `prompt_cache_key`；遇到会拒绝未知参数的服务商时关掉 |
| `BOT_MATCH_FALLBACK_AFTER` | `10s` | 排队多久后由 bot 兜底 |
| `BOT_BOTTLE_MIN_AGE` | `30m` | 瓶子投出多久仍没人回复，bot 才会去回 |
| `MEDIA_ROOT` | `./data/media` | 媒体文件根目录（启动时自动创建） |
| `MEDIA_MAX_UPLOAD_MB` | `100` | 单个文件的大小上限 |

需要新增 `getBool` 辅助函数。在 `config_test.go` 中补充校验用例（`BOT_ENABLED=true` 缺少 key 或 model 时报错）。`deploy/docker-compose.yml` 给后端加媒体卷（挂到 `/data/media`，并设置 `MEDIA_ROOT=/data/media`）。新的环境变量写进 `deploy/README.md`，以及项目里已有的 `.env` 示例文件（如果有）。

## 3. 消息管道改造（`internal/conversation`）

### 3.1 抽出 `SendAs`

把 `HandleSend` 中"成员/会话校验 → 限流 → 过滤 → 落库 → 广播"的部分抽成：

```go
// SendInput 是一次发送的参数。
type SendInput struct {
    Identity      *store.Identity
    ConversationID pgtype.UUID
    ClientMsgID   pgtype.UUID
    Kind          string      // text | image | video
    Body          string
    MediaID       pgtype.UUID // image/video 必填
    SkipRateLimit bool        // bot 发送时为 true（bot 跨多个会话发送，不受单人限流约束）
}
// SendAs 返回落库的消息；inserted=false 表示幂等命中。错误用 *SendError{Code, Msg} 表示，Code 与现有错误码一致。
func (s *Service) SendAs(ctx context.Context, in SendInput) (msg store.Message, inserted bool, err error)
```

- `HandleSend` 解析载荷后调用 `SendAs`，把 `*SendError` 映射回 `cl.Error`，行为与现在**完全一致**。用户通过 WS 仍然只能发 `text`，现有的"V1 只支持文本消息"校验保留在 `HandleSend` 里。
- `SendAs` 中：`text` 要求 body 非空；`image`/`video` 要求 `MediaID` 有效，body 可以为空。所有类型的 body 都不超过 1000 字。内容过滤只对非空 body 执行。
- `InsertMessage` 查询要加上 `media_id` 参数（同步修改 bottle 等调用方，传空值）。

### 3.2 `mediaUrl`

- `msgNewMessage` 和 `messageResponse` 新增 `MediaURL string \`json:"mediaUrl,omitempty"\``。当 `media_id` 有效时，取值为 `/api/media/<id>`。
- 历史查询已经返回 `media_id`，在 `toMessageResponse` 中填入（room 等调用方同步修改）。

### 3.3 观察者

```go
// Observer 观察会话事件；实现方必须立即返回（耗时操作自行开 goroutine）。
type Observer interface {
    OnMessage(ev MessageEvent)
    OnConversationEnded(conversationID pgtype.UUID)
}
type MessageEvent struct {
    ConversationID   pgtype.UUID
    ConversationKind store.ConversationKind
    MessageID        int64
    SenderIdentityID pgtype.UUID
    SenderMemberID   int32
    Kind             string
}
func (s *Service) AddObserver(o Observer)
```

- `SendAs` 在真正插入并广播之后通知 `OnMessage`；`End` 在 `affected > 0` 之后通知 `OnConversationEnded`。
- 再加一个 `BroadcastTyping(ctx, convID, identityID, memberID)` 供 bot 使用，复用 `HandleTyping` 的广播逻辑（排除发送者和有屏蔽关系的人）。

## 4. 媒体模块（新包 `internal/media`）

### 4.1 存储

- 文件路径为 `MEDIA_ROOT/<folder_id>/<item_id><ext>`。先写入同目录的临时文件，再 `rename`；数据库写入失败时删除文件。
- 允许的 MIME 由前 512 字节嗅探（`http.DetectContentType`）决定，不信任客户端声明：
  - `image/jpeg` `.jpg`、`image/png` `.png`、`image/gif` `.gif`、`image/webp` `.webp` → kind=image；
  - `video/mp4` `.mp4`、`video/webm` `.webm` → kind=video。
  - 其他一律拒绝，返回 `media_rejected`（httpx 中如果没有这个错误码就新增）。
- 标签规范化：去掉首尾空白，去重；每个标签 1–20 字，最多 20 个。caption 最多 200 字。

### 4.2 管理接口（全部挂 `RequireAdmin`，写操作记审计日志）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/media-folders` | 列表，带每个文件夹的媒体数量 |
| POST | `/api/admin/media-folders` | `{name}` |
| PATCH | `/api/admin/media-folders/{id}` | 重命名 |
| DELETE | `/api/admin/media-folders/{id}` | 删除文件夹、所有媒体行和磁盘文件 |
| GET | `/api/admin/media-folders/{id}/items` | 媒体列表 |
| POST | `/api/admin/media-folders/{id}/items` | multipart：`files`（可以多个）、`tags`（逗号分隔，应用到这批所有文件）、`caption`。请求体总上限为 `MEDIA_MAX_UPLOAD_MB × 10`，单文件上限为 `MEDIA_MAX_UPLOAD_MB`。返回新建的条目 |
| PATCH | `/api/admin/media-items/{id}` | `{tags, caption}` |
| DELETE | `/api/admin/media-items/{id}` | 删除行和文件（引用它的消息的 `media_id` 由外键置空） |
| GET | `/api/admin/media-items/{id}/file` | 后台预览 |

### 4.3 用户侧

`GET /api/media/{id}`，挂 `chatProtected`。只有当这个媒体出现在请求者所属会话的未焚毁消息中时才放行（已离开的成员也可以访问，和历史消息接口一致）。否则返回 404，不区分"不存在"和"无权限"。用 `http.ServeContent` 输出，以支持 Range 请求（视频拖动进度条），并设置 `Cache-Control: private, max-age=86400`。

## 5. LLM 客户端（`internal/bot/llm.go`）

```go
type ChatMessage struct{ Role, Content string }   // role: system | user | assistant
type ChatRequest struct {
    Model       string
    Messages    []ChatMessage
    MaxTokens   int
    Temperature float64
    CacheKey    string
}
type ChatResult struct {
    Content                                      string
    PromptTokens, CompletionTokens, CachedTokens int
}
type LLM interface { Chat(ctx context.Context, req ChatRequest) (ChatResult, error) }
```

OpenAI 兼容实现：

- 请求 `POST {LLM_BASE_URL}/chat/completions`，请求体为 `{model, messages, max_tokens, temperature, stream:false}`；当 `LLM_SEND_CACHE_KEY=true` 且 CacheKey 非空时，再加 `prompt_cache_key`。
- 整个进程共用一个 `http.Client`（Transport 设置 `MaxIdleConnsPerHost: 16`），复用连接。
- 缓存命中数取以下两者的较大值：`usage.prompt_tokens_details.cached_tokens`（OpenAI、OpenRouter、通义等）和 `usage.prompt_cache_hit_tokens`（DeepSeek）。
- 遇到 429 或 5xx 时重试 1 次（间隔 1 秒，响应头有 `Retry-After` 时按它等待，但不超过 5 秒）；其他错误直接返回。
- 只有 `BOT_ENABLED=true` 时才构造这个客户端。

## 6. 提示词（`internal/bot/prompt.go`，纯函数，必须有单元测试）

**目标是前缀稳定**：同一个 bot 的前缀在不同对话之间保持一致，同一个对话的前缀在多轮之间只追加不改动，这样服务商的前缀缓存才能命中。

消息顺序：

1. `system`（bot 级，只有角色卡或媒体标签变化时才会变）：
   - 固定的平台说明：你在一个匿名聊天 App 上和陌生人聊天；像手机聊天一样简短、口语化；不用 Markdown；一次最多 3 条消息。
   - 角色卡各字段（空字段不输出）。
   - 可用的媒体标签词表：该 bot 文件夹中所有标签去重后**排序**。没有媒体时这一段不输出。
   - 输出格式说明（见下方解析规则）。
2. `system`（对话级，同一对话内不变）：对话类型（"随机匹配到的陌生人"或"回复你捞到的漂流瓶"）、对方的公开资料（性别、年龄、省份仅在公开时、标签），漂流瓶对话还包括瓶子原文。
3. 历史消息：对方的消息为 `user`，bot 自己的为 `assistant`。bot 发过的媒体写成 `[图片:标签1,标签2]` 或 `[视频:...]`。连续同一角色的消息用 `\n` 合并成一条。
4. 结尾 `system`（每轮都会变，所以放在最后）：当前时间（格式为"周X HH:MM"，使用 `Asia/Shanghai` 时区，如果配置了其他时区就用配置的），以及本轮指令：
   - 正常回复："回复对方。"
   - 开场："对方刚和你匹配上，还没说话，你先打个招呼。"
   - 回漂流瓶："你捞到了这个漂流瓶，写一条回复。"

**历史截断**：最多保留 60 条（合并前的消息条数）。超过时按 30 条为一块，从最老的一端整块丢弃：`start = ceil((n-60)/30)*30`。这样在 30 轮之内前缀不变。写成纯函数 `historyStart(n, max, step int) int`。

**缓存 key**：`anonchat-bot-<botID>`。

**输出解析** `ParseReply(text string) []Bubble`（纯函数）：

- 按行拆分，去掉空行；如果模型在行首加了"角色名：" / "角色名:" 这样的前缀，去掉它。
- 整行匹配 `^\[(图片|视频)[:：]\s*(.+?)\]$` 时，生成媒体气泡，标签按 `,，、` 拆分。
- 只有一行且为 `[沉默]` 时返回空（本轮不回复）。
- 最多 3 个气泡；每个文本气泡最多 1000 字。

## 7. Bot 引擎（`internal/bot`）

### 7.1 结构

- `service.go`：角色卡、bot 的增删改查，用量统计（供后台使用）。
- `handlers.go`：后台 HTTP 接口。
- `limits.go`：纯函数 `Check(l Limits, u Usage) (exceeded bool, reason string)`，分为 bot 级（总量、窗口）和对话级（对话回复数、最小间隔）。
- `pacing.go`：纯函数，带可注入的随机源：
  - `readDelay(min, max)`：读消息延迟；
  - `typingDelay(runes, msPerChar)`：限制在 [800ms, 8s] 之间，当 msPerChar=0 时返回 0；
  - `bottleNext(interval)`：下一次回瓶时间 = interval × [0.7, 1.3]。
- `engine.go`：worker，实现 `conversation.Observer` 和匹配兜底的 provider 接口。
- `bottles.go`：漂流瓶循环。

引擎在内存中缓存 bot 列表（`identity_id → bot`），每 30 秒刷新一次。后台写操作完成后调用 `engine.Invalidate()` 立即刷新。如果引擎没有启动，handler 收到的是 nil，要做判空。

### 7.2 对话中的回复流程

1. `OnMessage(ev)` 依次过滤：会话类型不是 match 或 bottle 的跳过；发送者本身是 bot 的跳过。然后在 goroutine 中查询这个会话里未离开的 bot 成员（查询 `GetConversationBot`）；没有就结束。
2. 防抖：每个会话维护一个状态 `convState{timer, firstPendingAt, running, dirty, lastReplyAt}`。
   - 每来一条用户消息，就把计时器重置为 `readDelay(min,max)`。
   - 但从第一条待处理消息算起，最长等待时间是 `2 × reply_delay_max_ms`，到了就必须触发。
   - 生成回复期间又来了新消息时，标记 `dirty`；本轮发完后重新调度。
3. 计时器触发后执行 `turn(conv)`：
   - 从数据库读最新的 bot 设置和会话。会话已结束就清理状态并返回。
   - 检查对话最小间隔：未到时间就改为在 `lastReplyAt + interval` 触发。
   - 检查限额：bot 级（`used_*` 与 `total_*` 比较；窗口内对 `bot_usage` 求和），对话级（这个对话的 `SUM(replies)`）。超限时：匹配会话以 `peer_left` 结束；漂流瓶会话保持沉默。
   - 组装提示词，调用 LLM（超时用 `LLM_TIMEOUT`）。**无论成败**，只要拿到了 usage 就记录：插入 `bot_usage`，并累加 `bots.used_tokens` 和 `used_replies`，放在同一个事务里。
   - 解析出气泡后逐条发送：先 `BroadcastTyping`，再 sleep `typingDelay`，然后重新确认会话没有结束，最后调用 `SendAs`（`SkipRateLimit=true`）。
   - 媒体气泡：在 bot 的文件夹里找同时包含所有指定标签的媒体；找不到就退而求其次，找至少命中一个标签的；排除本对话已经发过的；本对话已发媒体数达到 `conv_media_limit` 时跳过。仍然没有就静默跳过这一条。
   - LLM 调用出错时，记录 warn 日志，本轮不回复。
4. `OnConversationEnded` 停止计时器并删除状态。
5. 引擎的 `Run(ctx)` 退出时，停止所有计时器。
6. 测试需要的注入点：随机源、sleep 函数、bot 列表刷新间隔、漂流瓶循环间隔，都通过 Option 注入。

### 7.3 开场

匹配兜底成功后，引擎会收到 `BotMatched(convID, botID)`，在 `readDelay` 之后检查：如果会话里还没有任何消息，就执行开场。角色卡的 `first_message` 非空时直接发送它（不调用 LLM，但仍然记录 `replies=1`、token 为 0 的用量）；否则用开场指令调用 LLM。

## 8. 匹配兜底（`internal/match`）

```go
// BotProvider 由 bot 引擎实现。
type BotProvider interface {
    // PickBot 返回一个可用于和该用户配对的 bot 身份；没有可用的返回 nil, nil。
    PickBot(ctx context.Context, user *store.Identity, ic IdentityContext) (*BotCandidate, error)
    BotMatched(conversationID, botIdentityID pgtype.UUID)
}
type BotCandidate struct { Identity *store.Identity; Profile PrefProfile }
func (s *Service) SetBotFallback(after time.Duration, p BotProvider) // 在 Run 之前调用
```

- 导出 `match.Compatible(a, b PrefProfile) bool`，供 bot 包按同样的规则筛选（bot 的 Profile 中 VIP=false，即 bot 自身没有筛选偏好）。
- `queueEntry` 新增 `nextFallbackAt time.Time` 和 `fallbackPending bool`。入队时 `nextFallbackAt = enqueuedAt + after`。
- 匹配 goroutine 每次 tick 时，在 `tryMatchAll` 之后，对 `now >= nextFallbackAt` 且没有 pending 的条目设置 `pending=true`，并为每个条目开一个 goroutine：
  1. 调用 `PickBot`。如果返回 nil 或出错，通过命令 `cmdFallbackDone` 让匹配 goroutine把 `pending` 置为 false，并设置 `nextFallbackAt = now + after`。
  2. 拿到候选后，发送命令 `cmdClaim`，在匹配 goroutine 中**原子地**检查这个条目是否仍在队列中，在就移除。如果已经不在（被真人配走或者取消了），直接放弃。
  3. 认领成功后执行与 `onPair` 相同的流程：建会话、订阅用户、给用户发 `match.found`（peer 用 bot identity 的公开资料，构造方式和真人完全一样），然后调用 `BotMatched`。失败时把条目放回队首。
- 条目处于 pending 期间仍然可以和真人配对：真人优先。
- `PickBot` 的筛选条件：`enabled`、`serve_match`、绑定了角色卡；用户和 bot 之间没有任何方向的屏蔽；满足 `Compatible(user, bot)`；没有触发 bot 级限额；进行中的匹配会话数小于 `max_active_matches`。在符合条件的 bot 中优先选不在 `ic.Recent` 里的，然后随机挑一个。并发时可能超过上限 1，可以接受（写注释说明）。

## 9. 漂流瓶（`internal/bot/bottles.go` + `internal/bottle`）

- `bottle.Service` 新增 `HoldForBot(ctx, botIdentityID pgtype.UUID, minAge time.Duration) (*PickedBottle, error)`，在一个事务里完成：
  - 选出一个瓶子：`floating` 状态，或者是 `held` 状态但已超时；未过期；`created_at <= now-minAge`；投瓶人不是任何 bot；还没有人回复过（不存在 `conversation_id IS NOT NULL` 的 pick 记录）；这个 bot 没捞过；与投瓶人之间没有屏蔽；按 `created_at` 升序取一个，用 `FOR UPDATE SKIP LOCKED`。
  - 然后执行 `MarkBottleHeld` 和 `InsertBottlePick`。没有可用的瓶子时返回 nil, nil。
- 循环每 30 秒执行一次（可注入）。对每个 `enabled && serve_bottle && 有角色卡` 的 bot：
  - 当 `now >= nextBottleAt[bot]` 并且没有触发 bot 级限额时，调用 `HoldForBot`。
  - 拿到瓶子后用"回漂流瓶"指令生成回复（只取文本气泡，用换行拼起来），然后调用现有的 `bottle.Service.Reply(botIdentityID, bottleID, text)`，再记录用量（带上新会话 ID）。
  - 如果 LLM 失败、回复为空或者内容被拦截，调用 `ThrowBack` 把瓶子放回去。
  - 不管有没有捞到瓶子，都设置 `nextBottleAt = now + bottleNext(interval)`。没有瓶子时改用 `interval/4`。
- 之后投瓶人在这个会话里发的消息，走 7.2 的正常流程。

## 10. Bot 管理接口（`internal/bot/handlers.go`，全部 `RequireAdmin`，写操作记审计日志）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/admin/character-cards` | 列表 / 新建 |
| PUT/DELETE | `/api/admin/character-cards/{id}` | 更新 / 删除（绑定它的 bot 的 card_id 被置空） |
| GET | `/api/admin/bots` | 列表：设置、对外资料（昵称、性别、年龄、省份、标签、清流模式）、角色卡名、文件夹名，以及用量（`usedTokens`、`usedReplies`、窗口内 token 和条数、缓存命中 token 数、进行中的匹配数） |
| POST | `/api/admin/bots` | 新建：先创建 identity（随机 token 哈希、`identity.NewPublicCode()`、用户给的昵称或 `identity.GenerateNickname()`、`identity.GenerateAvatarSeed()`），再写入 bots 行，放在同一个事务里 |
| PUT | `/api/admin/bots/{id}` | 更新全部设置和对外资料。资料用 `profile` 包的校验函数校验（标签必须在常量表里），写入 identities 的现有列（`UpdateIdentityProfile` 和 `UpdateIdentityNickname`） |
| POST | `/api/admin/bots/{id}/reset-usage` | 把 `used_tokens` 和 `used_replies` 清零 |
| DELETE | `/api/admin/bots/{id}` | 先以 `peer_left` 结束这个 bot 所有进行中的匹配会话，再删除 bots 行，最后吊销它的 identity |

JSON 字段一律用 camelCase，时间用 RFC3339。

## 11. 装配（`internal/api/routes.go`、`testutil`）

- `Deps` 新增 `Media *media.Handler`、`BotAdmin *bot.Handler`、`BotEngine *bot.Engine`（可以为 nil）。
- `BOT_ENABLED=true` 时：创建 LLM 客户端和 Engine；`convSvc.AddObserver(engine)`；`matchSvc.SetBotFallback(cfg.BotMatchFallbackAfter, engine)`；在 `StartBackground` 中运行 `engine.Run(ctx)`。
- 媒体接口和 bot 管理接口不受 `BOT_ENABLED` 影响，始终注册。
- `testutil.Config` 新增：`BotLLM bot.LLM`（非 nil 时启用引擎）、`BotFallbackAfter`、`BotTick`、`MediaRoot`（默认 `t.TempDir()`）、`BottleMinAge`。

## 12. 前端

- `frontend/src/lib/api/client.ts`：
  - 消息类型新增 `mediaUrl?: string`；
  - 新增后台接口函数：角色卡、bot、媒体文件夹和媒体条目的增删改查；上传用 `FormData`。
- `MessageList`：
  - `kind=image` 渲染为 `<img loading="lazy">`，限制最大宽度，圆角，点击在新标签页打开原图；
  - `kind=video` 渲染为 `<video controls preload="metadata" playsInline>`；
  - 没有 `mediaUrl` 时显示灰色的"[媒体已失效]"。
  - 样式遵守 U 包的设计 token。
- `AdminPage`：新增"Bot"标签页（按现有标签页的写法），包含三个子面板：
  1. **角色卡**：列表和编辑表单（各字段使用 textarea）。
  2. **Bot**：
     - 列表显示启用状态、用量进度（已用/上限）、缓存命中率（缓存 token / prompt token）和进行中的匹配数；
     - 编辑表单：绑定角色卡和文件夹、模型、各项限额、节奏参数、对外资料；
     - 操作：新建、启停、重置用量、删除（需二次确认）。
  3. **媒体库**：文件夹列表（新建、重命名、删除）；选中文件夹后显示媒体网格（缩略图或视频首帧预览，使用后台的 file 接口），可编辑每项的标签和描述，可删除；有一个上传区，支持多选文件，并能给这批文件统一填写标签。
- 测试：`MessageList` 对 image、video 和失效三种情况的渲染测试；后台中表单与 payload 之间的转换函数（如有）写单元测试。

## 13. 测试

**单元测试（`go test ./...`）**：

- `prompt`：
  - 前缀稳定：同一个 bot 的两个不同对话，第 1 条 system 消息完全相同；同一个对话追加历史后，前 k 条消息不变。
  - `historyStart` 的边界。
  - `ParseReply`：多行、媒体行、全角冒号、角色名前缀、`[沉默]`、超过 3 条的截断。
- `limits`：每个维度的 0（不限）、未超和刚好等于上限三种情况。
- `pacing`：返回值在区间内；msPerChar=0 时返回 0。
- `llm`：用 httptest 验证：请求体包含 `prompt_cache_key`（并且开关关掉时不包含）；两种缓存命中字段格式都能解析；5xx 时重试 1 次；Authorization 头正确。
- `matcher` 兜底：
  - 到期的条目被 bot 认领；
  - pending 期间来了真人时，真人配对优先，认领失败；
  - pending 期间用户取消，认领失败；
  - provider 返回 nil 后，过了 `after` 才再次尝试。

**集成测试（`-tags integration`，使用假 LLM）**：

1. 匹配兜底端到端：用户排队，兜底时间设为 200ms，收到 `match.found`（检查 peer 字段集合与真人配对时完全相同）。用户发一条消息，假 LLM 返回 `"你好呀\n[图片:自拍]"`，用户依次收到文本和图片的 `msg.new`，图片带 `mediaUrl`。用户 GET 这个 mediaUrl 得到 200；另一个身份 GET 得到 404。
2. 对话上限：`conv_reply_limit=1`。第二条用户消息之后，会话以 `peer_left` 结束。
3. 总上限：`total_reply_limit` 达到后，PickBot 不再选中这个 bot（用户继续排队）。
4. 漂流瓶：真人投瓶，`BOTTLE_MIN_AGE=0`，循环间隔设得很短。bot 回复后，投瓶人收到 `bottle.reply`，并在"我的瓶子"里看到这条回复；投瓶人在会话里再发一条消息，bot 会回复。
5. 后台：角色卡和 bot 的增删改查；文件夹的创建、上传（合法的 png、非法的 txt）、修改标签、删除（磁盘文件也被删掉）；未登录访问返回 401。
6. 现有集成测试全部通过（`InsertMessage` 签名变化会影响多个包，要同步修改）。

## 14. 知识库

同步更新：

- `helloagents/main/wiki/data.md`：新增的表和 `messages.media_id` 外键；
- `protocol.md`：`msg.new` 和历史消息中的 `mediaUrl`，`/api/media/{id}`；
- `deploy.md`：新增的环境变量和媒体卷。
- `overview.md` 中增加一句模块说明。

不写 CHANGELOG。

## 15. 增补（2026-11-07 用户要求）：后台配置全局开关和模型接入

用户要求：bot 功能的开关、模型 API 的相关配置，都要能在管理后台修改，**修改后立即生效，不需要重启**；不能只靠环境变量。

### 15.1 存储（迁移 `00006_bot_settings.sql`）

```sql
CREATE TABLE bot_settings (
  id                     smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),   -- 单行表
  enabled                boolean NOT NULL DEFAULT false,                  -- 全局开关
  llm_base_url           text    NOT NULL DEFAULT '',
  llm_api_key_enc        bytea,                                           -- AES-256-GCM(nonce||ciphertext)，NULL 表示未设置
  llm_api_key_hint       text    NOT NULL DEFAULT '',                     -- 只存末 4 位，用于后台显示
  llm_api_key_base_url   text    NOT NULL DEFAULT '',                     -- key 保存时对应的 Base URL（规范化：去空白与末尾 /）
  llm_model              text    NOT NULL DEFAULT '',
  llm_timeout_seconds    int     NOT NULL DEFAULT 60  CHECK (llm_timeout_seconds BETWEEN 5 AND 300),
  llm_send_cache_key     boolean NOT NULL DEFAULT true,
  match_fallback_seconds int     NOT NULL DEFAULT 10  CHECK (match_fallback_seconds BETWEEN 3 AND 600),
  bottle_min_age_seconds int     NOT NULL DEFAULT 1800 CHECK (bottle_min_age_seconds BETWEEN 0 AND 604800),
  timezone               text    NOT NULL DEFAULT 'Asia/Shanghai',
  updated_by             uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  updated_at             timestamptz NOT NULL DEFAULT now()
);
```

- **首次初始化**：服务启动时，如果表里没有这一行，就用环境变量的值插入一行（`INSERT ... ON CONFLICT DO NOTHING`）。初始化映射：
  - `BOT_ENABLED` → `enabled`；
  - `LLM_BASE_URL`、`LLM_MODEL`、`LLM_TIMEOUT`、`LLM_SEND_CACHE_KEY`、`BOT_MATCH_FALLBACK_AFTER`、`BOT_BOTTLE_MIN_AGE`、`BOT_TIMEZONE` → 对应的列。
  - 行存在之后，**以数据库为准**，这些环境变量不再生效。
- **API Key**：
  - **key 与 Base URL 绑定**（防止 key 被引到别的地址）：保存 key 时同时记下当时的 Base URL
    （`llm_api_key_base_url`，规范化 = 去首尾空白与末尾 `/`）。
  - 生效的 key = 数据库中的 key（解密后），且**仅当"当前 Base URL == `llm_api_key_base_url`"** 时才算数；
    数据库没有（或不匹配）时回退到环境变量 `LLM_API_KEY`，但**仅当"当前 Base URL == 环境变量 `LLM_BASE_URL`"** 时才算数。
  - 两者都不满足时视为**没有 key**：`ready=false`、`notReadyReason="Base URL 已变更，请重新填写 API Key"`、
    `apiKeySource=none`（`apiKeyHint` 仍保留，用于提示"旧 key 的末 4 位"）。
  - 加密密钥来自新增的环境变量 `SETTINGS_ENC_KEY`（base64 编码，32 字节）。加密复用 `verify.Encrypt` 和 `verify.Decrypt`；如果它们是 verify 包的内部函数，就移到 `internal/platform/secretbox` 这类通用包里，并让 verify 改为引用新位置，行为不变。
  - 没有配置 `SETTINGS_ENC_KEY` 时，后台不允许保存 API Key（返回 `invalid_request`，提示"服务器未配置 SETTINGS_ENC_KEY，无法保存密钥"），其余设置照常可以保存。
  - **任何接口都不返回 key 明文或密文**，只返回 `apiKeySet`（bool）、`apiKeyHint`（如 `••••abcd`）和 `apiKeySource`（`db`、`env` 或 `none`）。日志和审计日志里也不能出现 key。
- **配置校验调整**：`BOT_ENABLED=true` 时不再强制要求配置 `LLM_API_KEY` 和 `LLM_MODEL`，因为可以在后台补填。新增 `SETTINGS_ENC_KEY`，长度不为 32 字节时报错；生产环境未配置时只打 warn 日志。

### 15.2 运行时

- **引擎始终创建并运行**，不再由 `BOT_ENABLED` 决定。观察者注册、匹配兜底注册、漂流瓶循环都始终装配。
- 引擎在内存中缓存一份"生效配置"：启动时加载；后台保存后调用 `Invalidate` 立即刷新；另外每 30 秒自动刷新一次。读取都走内存，匹配 goroutine 内不访问数据库。
- **LLM 客户端按配置构建**：base URL、key、超时或缓存开关有变化时，重建 `OpenAIClient`（新客户端仍共用进程级的 `http.Client`；超时用每次请求的 context 控制，或者按超时值缓存客户端）。默认模型取 `llm_model`，bot 自己设置了模型时以 bot 的为准。测试注入的假 LLM（`testutil.Config.BotLLM`）优先于按配置构建的客户端。
- **全局开关关闭**（或缺少 key、base URL、model 中的任何一项，视为"不可用"）时：
  - `OnMessage` 和开场不再调度新的回复；
  - `PickBot` 返回 nil；
  - 漂流瓶循环跳过；
  - 已经在进行的回复轮次在发送前重新检查，关闭后不再发送；进行中的匹配会话以 `peer_left` 结束，漂流瓶会话保持沉默，与 bot 停用的处理一致。
- **匹配兜底的时间也是动态的**：`BotProvider` 新增 `FallbackAfter() time.Duration`，返回 0 表示不兜底；它只读内存缓存。matcher 每次 tick 时用"当前的 after"判断条目是否到期（到期时间 = `max(enqueuedAt + after, retryAt)`，其中 retryAt 是上次失败后设置的下次重试时间）。`SetBotFallback` 保留，但不再需要传入固定的 after。
- 漂流瓶的最短瓶龄和时区，同样从缓存的配置里读取。

### 15.3 后台接口（`RequireAdmin`，写操作记审计日志，审计内容不含 key）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/bot-settings` | 返回全部设置，加上 `apiKeySet`、`apiKeyHint`、`apiKeySource`、`apiKeyStale`（key 绑定在别的 Base URL 上、当前不生效）、`secretStorageAvailable`（是否配置了 `SETTINGS_ENC_KEY`），以及 `ready`（当前是否可用）和不可用的原因 `notReadyReason` |
| PUT | `/api/admin/bot-settings` | 全量更新。`apiKey` 字段：不传或为空字符串表示**保持不变**；传入新值表示替换（并按本次 `baseUrl` 更新 `llm_api_key_base_url`）；另有 `clearApiKey: true` 表示清除数据库中的 key。**改了 `baseUrl` 又没给新 `apiKey` 时，只要旧地址上还有生效的 key（数据库或环境变量来源），就返回 `invalid_request`「修改 Base URL 时需要重新填写 API Key」**；同时带 `clearApiKey: true` 则允许保存（结果是没有 key）。`baseUrl` 必须是 http 或 https 的绝对 URL；时区必须能通过 `time.LoadLocation`；其余字段按表约束校验。保存后调用 `engine.Invalidate()` |
| POST | `/api/admin/bot-settings/test` | 测试连接。用"已保存的设置"（如果请求体里带了 baseUrl、model、apiKey，就用这些临时值覆盖，但不保存）发送一次极短的请求（一条 user 消息"ping"，`max_tokens=8`）。返回 `{ok, latencyMs, model, error}`，其中 error 是截断后的错误文案，不含 key。**安全约束：请求体里的 `baseUrl` 与已保存的不同时，绝不使用已保存的 key——必须同时带上 `apiKey`，否则直接返回 `ok=false`、`error="更换 Base URL 测试时需要填写 API Key"`，且不发出任何网络请求。** 这个接口限流：每个管理员每分钟最多 10 次 |

### 15.4 前端

在 Bot 标签页的最上方新增"全局设置"子面板：

- 总开关（Toggle），以及 Base URL、模型、API Key、超时（秒）、缓存 key 开关、匹配兜底时间（秒）、漂流瓶最短瓶龄（分钟）、时区。
- API Key 输入框使用 `type="password"`：
  - 已设置时，占位符显示"已设置 ••••abcd（来自数据库或环境变量），留空表示不修改"；
  - 旁边有"清除"按钮（需要二次确认）；
  - `secretStorageAvailable=false` 时禁用输入框，并提示需要在服务器上配置 `SETTINGS_ENC_KEY`。
- 顶部显示当前状态（可用或不可用及原因）。
- "保存"和"测试连接"两个按钮；测试连接结果显示延迟或错误信息。
- 新增的 API 函数放在 `features/admin/botApi.ts`，**不得进入主包**。

### 15.5 测试

- 单元测试：设置校验（URL、时区、取值范围）；生效 key 的优先级（数据库 > 环境变量 > 无）；加解密往返。
- 集成测试：
  1. GET 和 PUT 的响应里没有 key；数据库中 `llm_api_key_enc` 不包含明文；不传 `apiKey` 时 key 保持不变；`clearApiKey` 能清除 key。
  2. **不重启热切换**：服务启动时全局关闭 → 用户排队超过兜底时间也不会被 bot 接走；通过 PUT 打开后，下一次排队会被 bot 兜底。
  3. 修改模型后，假 LLM 收到的下一次请求使用新模型；bot 自己设置的模型优先级更高。
  4. 没有配置 `SETTINGS_ENC_KEY` 时，保存 key 会被拒绝，但其他字段可以保存。
  5. 测试连接接口：分别用一个 httptest 假服务返回成功和 401，验证 ok、error 以及 error 中不含 key。
- 现有测试全部通过；为了适配"引擎始终存在"，`testutil` 可以调整，但不得修改已有测试的断言。

### 15.6 文档

`deploy/README.md`、`.env.example` 和知识库要写明：
- `SETTINGS_ENC_KEY` 的作用和生成方法（`openssl rand -base64 32`）；
- 环境变量只用于首次初始化，之后以后台设置为准；
- API Key 的优先级。

## 16. 修正（2026-11-07 线上问题）：人设注入方式

**现象**：线上 bot 不认角色卡，自称"DeepSeek AI 助手"；匹配后的开场一直没有发出来。

**原因**：
- 中转网关（`api.zcw.work`）会丢弃开头那条 system 消息。线上开场请求的 prompt 只有 54 个 token，正好是"对话级 system + 结尾 system"两段的长度，承载人设的第 1 条 system 不见了。用户用 probe 复测：只发一条 system 时人设无效，把人设放进 user 消息就有效。
- 开场请求的 `max_output_tokens=300` 被思考型模型的推理过程用光，正文为空（`completion_tokens=300`，`replies=0`）。

**修正**：
- 迁移 `00007_bot_persona_mode.sql`：
  - `bot_settings.persona_mode`，取值 `user` 或 `system`，**默认 `user`**；
  - bots 的 `max_output_tokens` 默认值改为 1024。已经存在的 bot 不会自动修改，需要在后台手动调大。
- `user` 方式：不发送任何 system 消息。
  - 第一条 user 消息以"【角色设定】……【设定结束】"开头（内容与 system 方式的 bot 级 + 对话级相同，前缀稳定）；如果对方的第一句话也是 user，就接在设定块后面。
  - 本轮提示（时间 + 指令）追加到最后一条 user 消息末尾；最后一条是 assistant 时，单独补一条 user。
- `system` 方式：保留原来的四段布局（§6）。
- 后台"全局设置"新增"人设注入方式"选项。PUT 时这个字段为空表示保持当前值。
- 模型正文为空并且 `finish_reason=length` 时，打 warn 日志，提示调大输出上限（日志不含正文）。
