# 技术设计: Bot 人设包、双核驱动与长期记忆

> 本设计是上一包 `202611062351_ai-bot` 之上的增量。没有提到的部分（技术栈、目录约定、错误处理、sqlc、goose、前端风格、日志写法）一律沿用现状。
> 代码注释和文档使用简体中文，风格与现有代码一致。动手前先通读 `backend/internal/bot/{engine,prompt,llm,settings,bottles,service,handlers}.go`。

## 0. 硬性约束（实施时逐条自查）

1. **不暴露 bot 身份**：用户侧接口（WS 推送、REST、匹配结果、历史消息）不新增任何字段。表情包以普通 `image` 消息发送，走 `conversation.SendAs`。记忆、好感度、情绪、智核输出只存在于后台和提示词里。
2. **不写"否认自己是 AI"之类的指令**（上一包 §0.2）。跑偏检测**不得**检测或过滤任何关于 AI 身份的表述。
3. **不执行上传的代码**：`.krp` 里的 `tools/*.py` 一律忽略。
4. **依赖**：只允许把 `github.com/BurntSushi/toml` 从 indirect 提升为直接依赖，**版本保持 go.mod 里现有版本不变**（ADR-5）。不引入其他 Go 依赖，前端不新增依赖。PNG 和 zip 只用标准库（`archive/zip`，PNG 手工解析 chunk）。
5. **迁移只做加法**：`00009_bot_persona_memory.sql`，带完整 Down。
6. **日志**里不得出现 API Key、消息正文、记忆内容（画像值、事件文本）、智核输出正文。LLM 错误只记录状态码，不记录可能回显请求内容的响应正文；结束原因仅记录固定协议值。
7. **所有 LLM 调用都计入 bot 用量和限额**（`bot_usage.kind` 区分类型）。
8. 不提交 git commit；不改动与本包无关的文件；`git status` 里已有的用户改动不要回滚。

## 1. 数据模型（`backend/migrations/00009_bot_persona_memory.sql`）

```sql
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
```

> `key` 的 CHECK 如果 Postgres 正则不支持 `\u` 转义，就改成在应用层校验（同一规则：字母、数字、`_`、`-`、中文，1–32 个字符），CHECK 只保留 `char_length(key) BETWEEN 1 AND 32`。

同步修改：
- `queries/bot.sql` 里角色卡和 bot 的所有 SELECT、INSERT、UPDATE 都要带上新列；`InsertBotUsage` 增加 `kind` 参数。
- 新建 `queries/memory.sql`，放 §6 用到的查询；表情相关查询放进 `queries/bot.sql`。
- `testutil.Truncate` 加入 4 张新表（`card_expressions`、`bot_peer_events`、`bot_peer_memories`、`bot_memory_cursors`，注意外键顺序）。
- 运行 `make sqlc`。

## 2. 设置与模型选择

`Settings`（`internal/bot/settings.go`）增加 `ReasonModel`、`MemoryModel`、`MemoryRetention time.Duration`、`MemoryExtractEvery int`、`OOCGuard bool`，从 `bot_settings` 读取；后台 GET/PUT 一并支持，校验范围与 CHECK 一致。环境变量**不新增**（新列都有默认值）。

模型选择（写成 `Engine` 的小函数，带单元测试）：

| 调用 | 模型 | max_tokens | temperature | 缓存键 |
|---|---|---|---|---|
| 心核（reply / retry） | `bot.model` → `settings.Model` | `bot.max_output_tokens` | `bot.temperature` | `CacheKey(bot.ID)`（不变） |
| 智核（reason） | `bot.reason_model` → `settings.ReasonModel` → 心核模型 | `max(bot.max_output_tokens, 800)` | 0.5 | `CacheKey(bot.ID) + "-reason"` |
| 记忆抽取（memory） | `settings.MemoryModel` → `settings.Model` | 1200 | 0.2 | `"anonchat-memory"` |

三类调用都遵守 `settings.PersonaMode`：`user` 模式下不发 system 消息，把 system 内容放进第一条 user 消息（参照现有 `buildUserModeMessages` 的写法抽一个通用助手函数，不要复制粘贴三份）。

## 3. 角色卡与导入（新子包 `backend/internal/bot/persona/`）

`persona` 包只做**纯解析**，不访问数据库、不写文件，方便单元测试。落库由 `bot.Service` 完成。

### 3.1 统一结构

```go
// Card 是解析后的角色卡（与 character_cards 的列一一对应）。
type Card struct {
    Name, Description, Personality, Scenario, FirstMessage, ExampleDialogue, SystemPrompt string
    HeartPrompt, ReasonPrompt, PostHistoryInstructions, CreatorNotes string
    AlternateGreetings []string
    Lorebook           []LoreEntry
    Tools              []string
    SourceFormat       string          // st | krp
    Extensions         map[string]any  // 不认识的字段
}

// LoreEntry 是一条世界书条目（存进 character_cards.lorebook）。
type LoreEntry struct {
    Keys          []string `json:"keys"`
    Content       string   `json:"content"`
    Enabled       bool     `json:"enabled"`
    Constant      bool     `json:"constant"`       // 常驻：不需要关键词触发
    CaseSensitive bool     `json:"case_sensitive"`
    Order         int      `json:"order"`          // 小的排前面
    Comment       string   `json:"comment"`
}

// ExpressionFile 是 .krp 里的一个表情（图片内容已读入内存，已通过大小和类型校验）。
type ExpressionFile struct {
    Key, Description, FileName string
    Data                       []byte
}

// Result 是一次导入的结果。
type Result struct {
    Card        Card
    Expressions []ExpressionFile
    Warnings    []string // 给运营者看的中文提示
}

// Parse 按文件内容嗅探格式：PNG 签名 → ST PNG；zip 签名 → .krp；否则按 JSON 解析 ST 卡。
func Parse(fileName string, data []byte) (Result, error)
```

字段校验（导入和手动编辑共用，放在 `persona.Validate(Card) error`，返回中文错误）：
- `Name` 1–40 字（超长截断并加 warning，空则报错）。
- 单个文本字段最多 20000 字；`AlternateGreetings` 最多 20 条，每条最多 2000 字。
- `Lorebook` 最多 100 条，每条 `Content` 最多 2000 字、`Keys` 最多 20 个；`Enabled=false` 的条目保留但不生效。
- `Tools` 只允许 §5.3 白名单里的名字，未知名字丢弃并加 warning。

### 3.2 SillyTavern 映射

支持三种形式：
- **V2 / V3 JSON**：`spec` 为 `chara_card_v2` 或 `chara_card_v3`，字段在 `data` 下。
- **V1 JSON**：没有 `spec`，字段在顶层。
- **PNG**：遍历 chunk，读取 keyword 为 `ccv3`（优先）或 `chara` 的 `tEXt` / 未压缩 `iTXt` 块，值做 base64 解码后按 JSON 处理。读到 `IEND` 停止。chunk 长度不合理时报错，不 panic。

| ST 字段 | 角色卡列 |
|---|---|
| `name` | `name` |
| `description` | `description` |
| `personality` | `personality` |
| `scenario` | `scenario` |
| `first_mes` | `first_message` |
| `mes_example` | `example_dialogue` |
| `system_prompt` | `system_prompt` |
| `post_history_instructions` | `post_history_instructions` |
| `alternate_greetings` | `alternate_greetings` |
| `creator_notes` | `creator_notes` |
| `character_book.entries[]` | `lorebook`：`keys`、`content`、`enabled`、`constant`、`case_sensitive`、`insertion_order` → `order`、`comment` |
| 其他（`tags`、`creator`、`character_version`、`extensions`、`assets`、`group_only_greetings`…） | `extensions`（原样保留） |

`character_book` 里 `selective`、`secondary_keys`、`position` 等暂不支持的语义：条目照常导入，加一条 warning 说明"只按主关键词触发"。

宏替换在**组装提示词时**做（库里保留原文）：`{{char}}` / `<BOT>` → 角色名；`{{user}}` / `<USER>` → `对方`。大小写不敏感。

### 3.3 `.krp` 人设包

上传的是 zip（扩展名 `.zip` 或 `.krp` 都接受）。zip 内要么有且只有一个 `<id>.krp/` 顶层目录，要么这些文件直接放在根目录：

```
index.toml          # 必需：name?、intro?、tools?（AnonChat 扩展）、[[commands]]（忽略）
heart.prompt        # 可选：心核人设 → heart_prompt
reason.prompt       # 可选：智核人设 → reason_prompt
expressions/
  index.json        # 可选：[{"id","path","description"}]，path 相对 expressions/
  <图片>
tools/*.py          # 忽略，每个文件名记一条 warning
```

映射规则：
- `name` → `name`（缺省用目录 id）；`intro` → `creator_notes`。
- `tools = ["recall_memory", ...]`（AnonChat 扩展，可选）→ `tools`。
- `[[commands]]` 每条记一条 warning："已忽略人设命令 /xxx"。
- `index.toml` 里其他键放进 `extensions["krp"]`。
- 表情：`id` → `key`（不符合 §1 的规则就跳过并加 warning），`description` 原样保留；`path` 必须指向 zip 里 `expressions/` 下的文件。

### 3.4 不可信输入的硬性限制（导入整体失败时返回 400，中文错误）

| 项 | 限制 |
|---|---|
| 上传文件大小 | ≤ 50 MB（`http.MaxBytesReader`） |
| zip 条目数 | ≤ 300 |
| zip 解压总大小 | ≤ 100 MB；按实际读取的字节累计，**不信任**头部声明的大小（用 `io.LimitReader`） |
| 单个文本文件（toml / prompt / json） | ≤ 1 MB |
| 单个表情图片 | ≤ media 现有的单文件上限（`media.Service.MaxUploadBytes()`），由调用方传入 |
| 表情数量 | ≤ 100 |
| 路径 | 拒绝绝对路径、包含 `..`、反斜杠和符号链接的条目 |
| 表情类型 | `http.DetectContentType` 必须是 `image/png`、`image/jpeg`、`image/gif`、`image/webp` 之一，否则跳过并加 warning |
| JSON / PNG 文本块 | ≤ 5 MB |

### 3.5 落库流程（`bot.Service.ImportCard`）

1. `persona.Parse` → `Result`。
2. 事务里插入角色卡。
3. 有表情时：调用 media 服务创建文件夹（名称为 `表情·<角色名>`，重名时追加 `-2`、`-3`…，并截断到 40 字），逐个 `SaveItem`（`tags` 留空、`caption` 用 description），再插入 `card_expressions`，最后更新卡片的 `expression_folder_id`。
4. 任一步失败：删除已创建的文件夹（media 的 `DeleteFolder` 会删除文件）和卡片，返回错误。不允许留下半张卡。
5. `bot.Service` 通过一个小接口依赖 media（`CreateFolder`、`SaveItem`、`DeleteFolder`），在 `cmd/server` 装配时注入，集成测试里注入真实的 `media.Service`。

**删除角色卡**时，如果 `expression_folder_id` 不为空，同时删除该文件夹。

**手动管理表情**（§10）也走同一个文件夹：卡片还没有表情文件夹时，第一次上传会自动创建。

## 4. 心核提示词（修改 `prompt.go`）

`PromptCard` 增加新字段；`PromptInput` 增加：

```go
Expressions  []PromptExpression // key + description，按 sort、key 排序；只在 single 模式渲染
CoreMode     string             // single | dual
Memory       *PromptMemory      // nil 表示没有记忆
Relation     PromptRelation     // 好感度与情绪（没有记忆时为默认值）
TriggeredLore []string          // 本轮触发的世界书内容（非常驻）
ReasonNotes  *ReasonResult      // dual 模式的智核结论
FixHint      string             // 跑偏重写时的纠正说明
```

**前缀稳定性规则**（不能破坏缓存）：只依赖 bot 和卡片的内容放在第 1 段；只依赖会话的内容放在第 2 段；每轮会变的内容一律放到结尾段。

第 1 段（bot 级）按顺序拼接，空字段跳过：
1. `platformPrompt`（不变）
2. 角色名、身份与外貌、性格与说话风格、场景设定（现有）
3. **人设**：`heart_prompt`
4. 示例对话、额外要求（现有）
5. **常驻设定**：`Enabled && Constant` 的世界书条目，按 `Order` 排序，总长不超过 1500 字
6. 可用的媒体标签（现有）
7. **可用表情**（只在 `CoreMode=single` 且有表情时）：`key（说明）`，用顿号分隔
8. `outputFormatPrompt`；single 模式且有表情时追加一句："想发表情时单独一行写 [表情:表情名]，一轮最多一个。"

第 2 段（会话级）：现有内容 + **记忆块**（§6.3 渲染，没有记忆时不输出）。

结尾段（每轮都变），按顺序：
1. 当前时间（现有）
2. **关系**：好感度阶段 + 情绪（§6.4）
3. **相关设定**：`TriggeredLore`
4. **内心**（dual）：`想法：…\n回复要点：…`；如果智核选了表情，补一句"这一轮的表情已经选好，你只需要说话"
5. `post_history_instructions`
6. `FixHint`（重写时）
7. 本轮指令（现有）

宏替换（§3.2）对第 1 段、开场白、世界书内容、`post_history_instructions` 都生效。

**世界书触发**（`matchLore(entries, texts []string) []string`，纯函数）：`texts` 取对方最近 6 条文本消息加上瓶子原文；`Enabled && !Constant` 的条目只要任一 key 是子串就算命中（`CaseSensitive=false` 时先统一转成小写）；按 `Order` 排序，总长不超过 1500 字，超出的丢掉。

**开场白**：开场且 `first_message` 和 `alternate_greetings` 里有非空项时，随机挑一条（用 Engine 的 `RandSource`）做宏替换后直接发送，不调用模型（沿用现有逻辑）。

**ParseReply 扩展**：
- 新增整行匹配 `^\[表情[:：]\s*(.+?)\]$` → `Bubble{Kind: "expression", Key: …}`。
- 每次回复最多 1 个表情气泡，**不占用** `maxBubbles`（3 条）的名额。
- `[沉默]` 的语义不变。

## 5. 智核（新文件 `reason.go`）

### 5.1 提示词

前缀（每个 bot 稳定，属于缓存前缀）：
- 内置智核说明（常量 `reasonBasePrompt`）："你是角色「{角色名}」的内心（智核）。你不直接对对方说话，而是在角色开口前想清楚：对方说了什么、想要什么、你此刻的情绪、你和对方的关系，以及这一轮该怎么回。你的结论会交给负责说话的心核。"
- 卡片的 `reason_prompt`（为空时跳过）。
- 角色概要：`description`、`personality`、`heart_prompt`，每项截断到 1500 字。
- 可用工具（§5.3，只列启用的）和可用表情（`key（说明）`）。
- 输出协议（§5.2）。

后面依次是：会话级段（与心核相同，含记忆块），一条 user 消息（最近 20 条历史渲染成 `对方：…` / `你：…` 的文本记录 + 当前时间 + 关系 + 触发的世界书），然后是工具结果（如果有）。

### 5.2 输出协议（纯 JSON 文本，不使用原生 function calling，见 ADR-3）

智核每次只输出一个 JSON 对象，二选一：

```json
{"tool_calls":[{"name":"recall_memory","arguments":{"query":"考试"}}]}
```

```json
{"thought":"他今天心情不好，想找人说话","guidance":"先安慰，问问发生了什么，别说教","mood":{"label":"担心","value":4},"affinity_delta":1,"expression":"","silent":false}
```

解析（`ParseReasonOutput(text string) (ReasonOutput, error)`，纯函数）：
- 去掉 ```` ```json ```` 代码块标记，截取第一个 `{` 到最后一个 `}`，然后 `json.Unmarshal`。
- `thought`、`guidance` 各截断到 300 字；`mood.label` 截断到 10 字，`mood.value` 限制在 0–10；`affinity_delta` 限制在 −5 到 +5。
- `expression` 不在卡片表情里时置空。
- `tool_calls` 每轮最多执行 3 个，未启用或未知的工具返回"（工具不可用）"。

### 5.3 内置工具白名单

| 名称 | 参数 | 行为 |
|---|---|---|
| `recall_memory` | `query` | 按空白和中英文逗号拆出最多 3 个关键词，对 `bot_peer_events.gist` 和画像的 topic、subtopic、值做不区分大小写的子串匹配，返回最多 8 行（事件带 `MM-DD` 日期）。没有记忆时返回"（没有相关记忆）"。 |
| `get_time` | 无 | 返回本地时区的完整日期、星期和时间。 |
| `lookup_lore` | `keyword` | 在卡片世界书中已启用条目的 keys 和 content 里做子串匹配，返回最多 3 条，每条截断到 500 字。 |

卡片 `tools` 为空数组时三个全部启用。KouriChat 的 `list_expressions` / `send_expression` 对应为：表情列表直接写进提示词，结论里的 `expression` 字段即为发送选择。

### 5.4 循环与降级

```
for step := 0; step < 3; step++ {
    out := callReason(...)            // 每次调用记一条 kind=reason 的用量
    if 是 tool_calls 且 step < 2 { 执行工具，把结果作为 user 消息「工具结果：…」追加；continue }
    if 是 tool_calls 且 step == 2 { 视为失败 }
    break
}
```

第 3 次调用前，在工具结果后面追加"不要再调用工具，直接输出结论。"。调用报错、解析失败或最终仍是 `tool_calls` 时，返回 `nil`，引擎按 single 流程继续（结尾段不带"内心"，single 模式下表情由心核决定）。

## 6. 长期记忆（新文件 `memory.go`、`memory_worker.go`，查询在 `queries/memory.sql`）

### 6.1 画像结构

`profile` 的 topic 固定为 6 个（参考 memobase 的陪伴配置）：

| topic | 中文 | 示例 subtopic |
|---|---|---|
| `basic_info` | 基本信息 | 称呼、年龄段、所在城市、职业或学业 |
| `interest` | 兴趣 | 游戏、音乐、运动 |
| `life` | 生活 | 作息、宠物、家人、近期计划 |
| `psychological` | 性格与状态 | 性格、最近的情绪 |
| `relationship` | 和你的关系 | 对你的称呼、约定、对你的态度 |
| `preference` | 聊天偏好 | 喜欢的话题、雷区 |

限制：每个 topic 最多 10 个 subtopic（超出时删除 `t` 最旧的）；subtopic ≤ 20 字；值 ≤ 100 字；整个画像序列化后 ≤ 6000 字节（超出时按 `t` 从旧到新删除）。

**隐私清洗**（`scrubPII`，纯函数，写入前对值和 gist 执行）：把连续 7 位以上的数字（可夹空格或 `-`）替换为 `***`，把邮箱替换为 `***`。抽取提示词也明确禁止记录手机号、证件号、精确住址和各类账号。

### 6.2 抽取任务（`MemoryWorker`）

- 在 `Engine.Run` 里启动，每 60 秒一轮，单个 goroutine 串行处理，跟随 Engine 的 ctx 一起退出。全局未就绪（`!Ready`）时跳过本轮。
- 候选查询（`ListMemoryCandidates`，参考写法，执行前用 `EXPLAIN` 确认走了索引）：

```sql
-- name: ListMemoryCandidates :many
SELECT c.id AS conversation_id, b.id AS bot_id, cm_peer.identity_id AS peer_identity_id,
       COALESCE(mc.extracted_until, '-infinity'::timestamptz)::timestamptz AS extracted_until,
       COALESCE(mc.fail_count, 0)::int AS fail_count
FROM bots b
JOIN conversation_members cm_bot ON cm_bot.identity_id = b.identity_id
JOIN conversations c ON c.id = cm_bot.conversation_id
JOIN conversation_members cm_peer ON cm_peer.conversation_id = c.id AND cm_peer.identity_id <> b.identity_id
LEFT JOIN bot_memory_cursors mc ON mc.conversation_id = c.id
JOIN LATERAL (
  SELECT count(*) AS n, max(m.created_at) AS last_at,
         bool_or(m.sender_identity_id = cm_peer.identity_id) AS has_peer
  FROM messages m
  WHERE m.conversation_id = c.id
    AND m.created_at > COALESCE(mc.extracted_until, '-infinity'::timestamptz)
    AND m.kind IN ('text','image','video') AND m.burned_at IS NULL
) nm ON nm.n > 0 AND nm.has_peer
WHERE b.enabled AND b.memory_enabled
  AND c.kind IN ('match','bottle')
  AND (c.ended_at IS NULL OR c.ended_at > now() - interval '8 days')
  AND (mc.next_try_at IS NULL OR mc.next_try_at <= now())
  AND (c.ended_at IS NOT NULL OR nm.n >= sqlc.arg(extract_every)::int OR nm.last_at < now() - interval '10 minutes')
ORDER BY nm.last_at
LIMIT 20;
```

  > 实际列名（如 `sender_identity_id`、`burned_at`、`kind` 的取值）以 `00001_init.sql` 和后续迁移为准；如有出入，按真实表结构调整，语义保持不变。**已焚毁的消息不参与抽取。**

- 处理每个候选：
  1. 读 bot。bot 用量超限时跳过，不推进游标（复用 `Check(LimitsFromBot, usage)`）。
  2. 读取游标之后的消息（按时间升序，最多取最新的 80 条；媒体消息渲染成 `[图片:标签]`），读取现有画像和好感度。
  3. 调用记忆模型（§6.5 的提示词）。
  4. 解析 JSON（同样的容错截取），在**一个事务里**：upsert `bot_peer_memories`（合并画像、`affinity += clamp(delta, −10, 10)`，并限制在 −100 到 100）；插入事件（每次最多 3 条，gist ≤ 120 字）；把该对的事件裁剪到最新 50 条；upsert 游标（`extracted_until` = 本批最后一条消息的 `created_at`，`fail_count=0`，`next_try_at=NULL`）；写入用量（`kind=memory`，`replies=0`）。
  5. 失败（调用报错或解析失败）：`fail_count+1`，`next_try_at = now + 5min × fail_count`。`fail_count` 达到 3 时直接把游标推进到本批末尾并清零，记一条 warn 日志（不含内容），避免一直卡住。失败调用如果有 token 用量也要记录。
- 抽取可以由事件提前触发：`OnConversationEnded` 时非阻塞地通知 worker 立即跑一轮（带缓冲为 1 的 channel，满了就丢弃）。

### 6.3 记忆读取与渲染

- 回复时（匹配会话的每一轮，以及漂流瓶 `bottleReply` 针对投瓶人）调用 `loadPeerMemory(botID, peerID)`：读取画像行和最近 8 条事件。`bot.memory_enabled=false` 时返回 nil。
- `RenderMemory(m *PromptMemory) string`（纯函数，总长 ≤ 1500 字，超出时先砍事件、再砍画像）：

```
【你对对方的记忆（来自之前的聊天。自然地想起就好，不要逐条复述，也不要提"记忆""资料"这类词）】
基本信息：称呼=小林；所在城市=杭州
兴趣：游戏=原神
最近的事：
- 11-05 他说下周要考驾照
```

### 6.4 好感度与情绪

- 阶段映射（`affinityStage(int) string`，纯函数）：≤ −40 反感（冷淡、不耐烦）；−39 到 −10 戒备（保持距离）；−9 到 19 陌生（普通的陌生人社交）；20 到 49 熟悉（轻松随意）；50 到 79 好感（主动、亲近）；≥ 80 亲密（非常信任、依恋）。
- 结尾段渲染：`你和对方的关系：熟悉（轻松随意）。你现在的情绪：开心（7/10）。` 情绪标签为空时只写关系。
- 更新来源：dual 模式下每轮智核的 `affinity_delta`（±5）和 `mood`；两种模式下的记忆抽取（±10）。
- 每次 bot 成功发出回复后，调用 `TouchPeerMemory` upsert 一行，刷新 `last_interaction_at`（同时应用智核的 delta 和情绪）。

### 6.5 抽取提示词（常量，前缀稳定）

system 内容要点（照写为中文常量）：
- 你负责整理聊天机器人角色对聊天对象的长期记忆。
- 只记录对方**亲口说出**的关于他自己的事实、你们之间的约定或称呼，以及对话中发生的值得以后想起的事情。不要猜测，不要记录角色自己的设定。
- 禁止记录手机号、证件号、精确住址、各类账号和密码。
- 给出允许的 6 个 topic 和各自含义。
- 只输出 JSON：

```json
{"profile":[{"topic":"basic_info","subtopic":"称呼","value":"小林","action":"set"}],
 "events":[{"gist":"他说下周要考驾照","tags":["考试"]}],
 "affinity_delta":2}
```

  `action` 只能是 `set` 或 `delete`；`events` 最多 3 条；`affinity_delta` 在 −10 到 10 之间，表示这段对话后角色对对方的好感变化（对方友善、真诚就加，冒犯、敷衍就减）。

user 内容：当前画像（JSON）、当前好感度、角色名，然后是新对话记录（`对方：…` / `你（角色名）：…`）。

## 7. 跑偏检测（新文件 `ooc.go`）

`CheckStyle(bubbles []Bubble) []string`（纯函数，返回命中的规则名）：

| 规则 | 判定 |
|---|---|
| `markdown` | 行首是 `#`、`- `、`* `、`> `、`数字. `；或者包含 `**`、```` ``` ````、`\|---` |
| `leak` | 包含 `角色设定`、`设定结束`、`系统提示`、`回复要点`、`"thought"`、`"guidance"`，或者以 `{"` 开头 |
| `service` | 包含 `有什么可以帮`、`希望对你有帮助`、`如果你还有其他问题`、`我很乐意为你`、`请问还有什么`、`作为你的助手` |
| `too_long` | 任一文本气泡超过 200 字，或者全部文本合计超过 400 字 |

**明确不检测**：任何关于"AI / 机器人 / 模型"身份的表述（§0.2）。为此写一条测试：包含"我是 AI"的文本不得命中任何规则。

流程（`settings.OOCGuard=true` 时）：心核输出 → `ParseReply` → `CheckStyle`。命中时，设置 `FixHint`（"上一次回复不符合聊天风格（{规则中文名}）。请重新以角色身份像手机聊天一样简短口语化地回复，不要用 Markdown，不要像客服，不要复述设定。"），再调用一次心核（`kind=retry`）。仍然命中时调用 `Sanitize(bubbles)`：去掉 Markdown 标记、删除 `leak` 命中的行、超长气泡在 200 字以内的最后一个句末标点处截断。只重写一次。

## 8. 引擎改动（`engine.go`、`bottles.go`）

`runTurn` 新流程（现有的检查和收尾逻辑保持不变）：

1. 现有检查：会话、bot、启用状态、全局就绪、开场判定、成员、身份、限额。
2. 读取卡片、卡片表情（新查询 `ListCardExpressions`）、对方身份 id、记忆（`memory_enabled` 时）。
3. 开场：卡片有开场白（含备选）时随机挑一条直接发送；否则只调用心核（不走智核）。
4. `bot.core_mode == "dual"`：运行 §5.4，得到 `reason`（可能为 nil）。`reason.Silent` 为真时：记录用量、应用情绪和好感度、结束本轮。
5. 组装 `PromptInput`（含 `Memory`、`Relation`、`TriggeredLore`、`ReasonNotes`、`Expressions`、`CoreMode`），调用心核（`kind=reply`）。
6. §7 跑偏检测和重写（`kind=retry`）。
7. 依次发送气泡（现有 `sendBubble`）。表情气泡：只在 single 模式下处理，按 key 查找卡片表情，以 `image` 发送，`MediaID` 为表情的 `media_item_id`，typing 时长按 2 个字计算。表情不受 `PickMediaForBot` 的去重约束，允许重复；**与其他媒体共同受 `conv_media_limit` 约束**（按实际媒体消息条数计数），每轮最多 1 个。
8. dual 模式下智核选了表情：所有文本气泡发完后，如果本轮进行中没有收到对方新消息（新增 `e.hasPendingInput(convID)`，读取 `convState.dirty`），就发送表情；否则放弃。
9. 有任一气泡发送成功时调用 `TouchPeerMemory`（附带智核的 delta 和情绪）。
10. `recordUsage(ctx, bot, convID, res, replied, kind)`：增加 `kind` 参数，所有调用点都要更新。心核调用沿用 `replied` 的规则，其他类型的 `replies` 固定为 0。

`bottleReply`：同样注入投瓶人的记忆和关系，`Expressions` 为空（漂流瓶回复不发表情），不走智核（漂流瓶是一次性的长回复，没必要双核），同样执行跑偏检测。

## 9. 清理任务（`internal/jobs/cleaner.go`）

每次清理新增三项（每项单独计数并记日志，失败不影响其他项）：
1. `DELETE FROM bot_peer_memories WHERE last_interaction_at < now() - make_interval(days => (SELECT memory_retention_days FROM bot_settings WHERE id = 1))`（`bot_settings` 没有行时按 30 天处理）。
2. `DELETE FROM bot_peer_memories m USING identities i WHERE i.id = m.peer_identity_id AND i.revoked_at IS NOT NULL`。
3. `DELETE FROM bot_memory_cursors mc USING conversations c WHERE c.id = mc.conversation_id AND c.ended_at < now() - interval '8 days'`。

事件随画像级联删除。

## 10. 后台 API（全部挂在 `RequireAdmin` 下，沿用现有的错误码和审计写法）

| 方法 | 路径 | 说明 | 审计动作 |
|---|---|---|---|
| POST | `/api/admin/character-cards/import` | multipart，字段 `file`；成功返回 201 `{card, warnings}` | `bot.card.import` |
| GET/POST/PUT | `/api/admin/character-cards[/{id}]` | 请求和响应增加新字段：`heartPrompt`、`reasonPrompt`、`alternateGreetings`、`postHistoryInstructions`、`lorebook`、`tools`、`creatorNotes`、`sourceFormat`（只读）、`expressionCount`（只读） | 现有 |
| GET | `/api/admin/character-cards/{id}/expressions` | 列表：`id`、`key`、`description`、`mediaItemId`、`sort` | — |
| POST | `/api/admin/character-cards/{id}/expressions` | multipart：`file`、`key`、`description` | `bot.expression.create` |
| PATCH | `/api/admin/card-expressions/{id}` | `{key, description, sort}` | `bot.expression.update` |
| DELETE | `/api/admin/card-expressions/{id}` | 同时删除对应的媒体条目 | `bot.expression.delete` |
| GET/POST/PUT | `/api/admin/bots[/{id}]` | 增加 `coreMode`、`reasonModel`、`memoryEnabled`；列表增加 `windowReasonTokens`、`windowMemoryTokens`、`memoryCount` | 现有 |
| GET/PUT | `/api/admin/bot-settings` | 增加 `reasonModel`、`memoryModel`、`memoryRetentionDays`、`memoryExtractEvery`、`oocGuard` | 现有 |
| GET | `/api/admin/bots/{id}/memories?limit&offset` | 列表：对方的 `identityId`、`publicCode`、`nickname`、`affinity`、`moodLabel`、`moodValue`、`eventCount`、`lastInteractionAt`；按 `last_interaction_at` 倒序 | — |
| GET | `/api/admin/bots/{id}/memories/{identityId}` | 详情：`profile`、`events[]` | `bot.memory.view` |
| DELETE | `/api/admin/bots/{id}/memories/{identityId}` | 删除单条 | `bot.memory.delete` |
| DELETE | `/api/admin/bots/{id}/memories` | 清空该 bot 的全部记忆 | `bot.memory.clear` |

审计 `detail` 里只写 id 和数量，**不写记忆内容**。

## 11. 前端（`frontend/src/features/admin/`）

- `botApi.ts`：新增类型和接口函数（仍然只在 admin 懒加载包里，**主包不能出现 bot 相关字符串**，沿用 B7 的检查方式）。
- `CharacterCardsPanel.tsx`：
  - 列表增加"导入"按钮（`<input type="file" accept=".json,.png,.zip,.krp">`），成功后刷新列表，并在对话框里展示 `warnings`。卡片上显示来源标记（手动 / ST / KRP）和表情数量。
  - 编辑弹窗分成"基础 / 双核 / 开场白 / 世界书 / 工具与表情"几个分区（用现有 UI 组件，不新增依赖）：
    - 双核：心核人设、智核人设（附说明：只在 bot 设为双核时生效）。
    - 开场白：开场白 + 备选开场白列表（可增删）+ 历史后指令。
    - 世界书：条目列表（关键词用逗号分隔、内容、常驻、启用、顺序），可增删。
    - 工具：三个复选框；全部不勾选表示全部启用，要在界面上写明。
    - 表情：只在已保存的卡片上显示；缩略图（用 `/api/admin/media-items/{id}/file`）、key、说明，可上传、修改、删除。
- **复制角色卡**：保留全部可编辑字段（含双核、备选开场白、世界书、工具和作者备注），不复制表情媒体；新卡来源由后端记为 `manual`。现有创建接口不支持媒体复制，界面明确说明。
- Bot 表单：驱动模式（单核 / 双核，附成本说明）、智核模型、长期记忆开关。列表行显示智核和记忆的 token 用量，以及记忆条数。
- Bot 行增加"记忆"按钮 → 记忆弹窗：分页列表；点开查看画像（按 topic 分组展示）和事件；单条删除和清空都要二次确认。
- 全局设置：智核默认模型、记忆模型、记忆保留天数（1–365）、抽取间隔（5–200 条）、跑偏检测开关。
- `botForm.ts` 的校验与后端范围一致，并补对应的单元测试。

## 架构决策 ADR

### ADR-1: 长期记忆用 Go 原生实现
**上下文:** 需要按用户的长期记忆。候选有 memobase（Apache-2.0，有 Go SDK，但 2026-01 之后停更）和 mem0（活跃，但需要 Python 服务和向量库）。
**决策:** 参照 memobase 的"画像 + 事件"模型，在现有 Postgres 里实现，复用 LLM 客户端、设置和用量体系。
**理由:** 不新增服务和运维面；记忆天然能按 bot 隔离并计入限额；数据量小（每对最多 50 条事件）。
**替代方案:** 自建 memobase → 拒绝：停更，额外 3 个容器，用量不在现有体系里。mem0 → 拒绝：同样要额外服务，按条抽取的 token 成本更高。
**影响:** 检索能力弱于向量方案（见 ADR-4）；抽取提示词需要自己维护。
**状态:** 已采纳（用户确认）

### ADR-2: 内置工具白名单替代 `.krp` 的 `tools/*.py`
**上下文:** KouriChat 的 `.krp` 会动态导入并执行 `tools/*.py`。
**决策:** 只支持 Go 内置的 `recall_memory`、`get_time`、`lookup_lore`，卡片按名字启用；`.py` 忽略。
**理由:** 执行上传的代码等同于远程代码执行；Go 进程里也无法运行 Python。
**替代方案:** 沙箱执行脚本 → 拒绝：复杂度和攻击面都远超收益。
**影响:** 依赖自定义工具的 `.krp` 人设功能会缩减，导入时给出 warning。
**状态:** 已采纳

### ADR-3: 智核用 JSON 文本协议调用工具
**上下文:** 生产环境走 OpenAI 兼容中转网关，已知有网关会丢弃或改写 system 消息（因此才有 `persona_mode`），原生 `tools` / function calling 的兼容性更没有保证。
**决策:** 智核输出约定好的 JSON，由引擎解析并执行工具，最多 2 轮。
**替代方案:** 原生 function calling → 拒绝：网关兼容性风险，而且现有 LLM 客户端需要大改。
**影响:** 依赖模型遵守 JSON 格式；解析失败时降级为单核，不影响回复。
**状态:** 已采纳

### ADR-4: 事件检索用关键词加时间，不用向量
**上下文:** 每对最多 50 条事件；项目没有配置 embedding 服务；中文全文检索需要扩展。
**决策:** 子串匹配加时间倒序；最近 8 条事件始终注入。
**替代方案:** pgvector 或 pg_trgm → 拒绝：要么需要扩展和 embedding API，要么需要超级用户权限创建扩展，对这个数据量收益很小。
**状态:** 已采纳

### ADR-5: 用 `BurntSushi/toml` 解析 `index.toml`
**上下文:** 上一包约定"不引入新的 Go 依赖"。`.krp` 的元数据是 TOML，可能包含多行字符串和表数组。
**决策:** 把 go.mod 里已经存在（indirect，来自工具依赖）的 `github.com/BurntSushi/toml` 提升为直接依赖，版本不变，不新增下载。
**替代方案:** 手写 TOML 子集解析器 → 拒绝：多行字符串、转义、表数组容易解析错，测试成本高于收益。
**影响:** 新增一个直接依赖（MIT 许可证，成熟稳定）。
**状态:** 已采纳（用户 2026-11-09 确认）

### ADR-6: 记忆按（bot，对方身份）隔离，不跨身份合并
**决策:** 主键为 `(bot_id, peer_identity_id)`；身份重置后视为新的人；旧身份吊销后清理其记忆。
**理由:** 匿名产品的隐私预期：用户重置身份就应该"重新开始"。
**状态:** 已采纳（用户确认）

## 安全与性能

- **安全:**
  - 导入时执行 §3.4 的全部限制，并有专门的测试：zip 炸弹（声明大小很小、实际很大）、路径穿越（`../x`）、伪装成 PNG 的非图片、损坏的 PNG chunk 长度。
  - 后台接口都要求管理员登录；记忆的查看和删除写审计，审计不含内容。
  - 用户侧接口不新增字段（§0.1）。
  - 记忆写入前做 PII 清洗（§6.1）；已焚毁的消息不参与抽取。
  - 日志不打印记忆、智核输出和消息正文。
- **性能:**
  - single 模式通常调用一次心核；跑偏检测命中时至多追加一次重写。固定前缀不随会话及每轮内容变化，缓存实际命中率取决于上游模型。
  - dual 模式通常每轮 2–4 次调用，跑偏重写可再追加一次；智核沉默时不调用心核。由运营者按 bot 选择开启。
  - 记忆 worker 每60秒或会话结束通知时启动一轮，每轮最多处理20个候选，串行执行。
  - 记忆读取先按主键读取 bot 开关；开启且存在画像时，再查询画像及最近8条事件（均走主键或索引）。后台 bot 列表的记忆计数在集合查询内聚合，不逐 bot 查询。

## 测试与部署

- **单元测试（`go test ./...`）**：
  - `persona`：ST V1、V2、V3 JSON；PNG（`chara` 和 `ccv3`）；`.krp` zip 的两种目录布局；各项安全限制；宏替换；warnings 内容。测试样例放在 `internal/bot/persona/testdata/`，PNG 和 zip 在测试里用代码生成，不提交二进制文件。
  - `prompt`：前缀稳定性（同一 bot 两个不同会话的第 1 段逐字节相同；记忆和世界书变化不影响第 1 段）；世界书触发；开场白随机挑选；表情行解析；user 和 system 两种模式。
  - `reason`：JSON 容错解析、数值截断、工具循环上限、降级。
  - `memory`：画像合并与容量裁剪、PII 清洗、渲染长度上限、好感度阶段。
  - `ooc`：各条规则；"我是 AI"不命中任何规则；Sanitize。
  - 模型选择函数。
- **集成测试（`-tags integration`）**：
  - 导入 `.krp`（含表情）后，卡片、文件夹、媒体条目、表情记录都存在；导入失败时没有残留。
  - dual 模式完整一轮（假 LLM 依次返回 tool_calls、结论、心核回复）：用量出现 2 条 `reason` 和 1 条 `reply`，表情消息以 `image` 落库，好感度已更新。
  - 有新消息打断时不发表情。
  - 记忆 worker：会话结束后抽取，画像和事件写入，游标推进；失败退避；超限跳过；已焚毁消息不进入抽取输入。
  - 第二次会话的提示词里包含第一次抽取的记忆；另一个 bot 的提示词里没有。
  - 清理任务：过期记忆、吊销身份的记忆、旧游标都被删除。
  - 用户侧 `msg.new` 和历史消息里没有新增字段（对比字段集合）。
- **前端**：`botForm` 校验、导入按钮调用、记忆弹窗的删除确认（Vitest + Testing Library）。
- **部署**：只需要执行迁移，不新增服务或环境变量。部署方式见记忆 `cch-hk-deploy`（git archive 上传 + compose 重建，保留 `.env` 和 override）；部署前先备份数据库。

## 知识库同步（领域语言）

`wiki/glossary.md` 新增术语：心核（heart）、智核（reason）、驱动模式（single / dual）、人设包（.krp）、表情（expression）、世界书（lorebook）、长期记忆（画像 profile、事件 event）、好感度（affinity）、情绪（mood）、跑偏检测（OOC guard）。代码命名统一使用括号里的英文，"人设包"不再另起叫法。

`wiki/data.md`：新表和新列；记忆保留规则。`project.md`：bot 能力摘要和最后同步说明。`deploy/README.md`：本包没有新增服务，说明迁移和备份注意事项。
