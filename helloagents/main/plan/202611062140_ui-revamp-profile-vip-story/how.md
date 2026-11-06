# 技术设计: 界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书

> 本文件在 V1 方案包 `202611061637_anonchat-v1-v2/how.md` 的基础上做增量设计。没有提到的部分（技术栈、目录约定、安全基线、错误处理、留存、限流框架）一律沿用 V1，不得改动。

## 1. 设计系统（前端）

### 1.1 Token（写入 `frontend/src/index.css` 的 Tailwind v4 `@theme`）

| Token | 值 | 用途 |
|---|---|---|
| `--color-brand` | `#8B7CF6` | 主色（薰衣草紫） |
| `--color-brand-2` | `#A78BFA` | 自己气泡的渐变终点 |
| `--color-accent` | `#E88BC4` | 辅助渐变终点（紫→粉） |
| `--color-lobby-bg` | `#F7F7FA` | 大厅背景 |
| `--color-lobby-card` | `#FFFFFF` | 大厅卡片 |
| `--color-lobby-line` | `#EBEBEF` | 大厅卡片边框 |
| `--color-lobby-ink` | `#1C1C28` | 大厅主文字 |
| `--color-lobby-sub` | `#6E6E80` | 大厅次文字 |
| `--color-night-from` / `--color-night-to` | `#0F1117` / `#1A1D29` | 聊天页背景渐变（不用纯黑） |
| `--color-glass` | `rgb(255 255 255 / 0.06)` | 玻璃面板 |
| `--color-glass-line` | `rgb(255 255 255 / 0.08)` | 玻璃边框 |
| `--color-bubble-peer` | `rgb(255 255 255 / 0.08)` | 对方气泡 |
| `--color-night-ink` / `--color-night-sub` | `#F5F5F7` / `#8E8E93` | 聊天页主/次文字 |
| `--radius-sm` / `--radius-card` / `--radius-panel` | `12px` / `20px` / `24px` | 小元素 / 卡片与气泡 / 大面板 |
| `--font-sans` | `'Inter Variable', 'PingFang SC', 'Microsoft YaHei', system-ui, sans-serif` | 标题 700，正文 400 |
| `--shadow-soft` | `0 1px 2px rgb(16 16 32 / 0.04), 0 4px 16px rgb(16 16 32 / 0.04)` | 唯一允许的阴影（极浅） |

- 字体：新增依赖 `@fontsource-variable/inter@5.3.0`，在 `main.tsx` 中 `import '@fontsource-variable/inter'`。字体文件打包进静态资源，符合 CSP（`default-src 'self'`）。中文回退到系统字体。
- 间距只用 8 的倍数（Tailwind 的 `2/4/6/8/10/12/16/20/24`）。大厅区块之间的纵向间距为 64–96px（`gap-16`～`gap-24`），Bento 卡片之间的间距为 16–20px（`gap-4`/`gap-5`）。
- **强调色最多两个**：品牌紫 `#8B7CF6` 和辅助粉 `#E88BC4`。房间卡片、头像、徽标的渐变只能在这两种颜色之间取值，或使用它们的低饱和浅色（例如 `brand/15`、`accent/15`），不得引入第三种色相。错误提示用中性红 `#E5484D`，不计入强调色，只用于错误。
- 禁止：真人头像、外链图片、重阴影（超出 `--shadow-soft` 的阴影）、持续时间低于 150ms 或带弹跳的动效、展示昵称历史、在线时长、注册时间等身份信息。

### 1.2 动效

| 名称 | 定义 | 用途 |
|---|---|---|
| `animate-fade-up` | 200ms ease-out，opacity 0→1，translateY 8px→0 | 卡片、气泡入场 |
| hover | `transition duration-150 ease-out`，只改变颜色、背景或 2px 位移 | 所有可交互元素 |
| `animate-breathe` | 1.6s ease-in-out infinite，opacity 0.3↔1，scale 0.85↔1 | 正在输入的三个光点，依次延迟 0.2s |
| 背景过渡 | 根节点下放两层固定定位的背景（浅色层和暗色层），按路由切换暗色层的 opacity，300ms ease-out | 从大厅进入聊天页时由浅变暗 |

`@media (prefers-reduced-motion: reduce)` 下关闭以上所有动画，只保留瞬时切换。

### 1.3 主题与路由的对应关系

- **聊天类路由**固定使用暗色玻璃风格：`/match`、`/rooms/:roomId`、`/bottles/:conversationId`、`/p/:token`（进入会话后）、`/stories/:storyId/play`。
- **大厅类路由**默认浅色：`/`、`/rooms`、`/bottles`、`/p/new`、`/vip`、`/stories`、`/stories/new`、`/works/:workId`。顶栏的太阳/月亮图标可以把大厅切换到暗色（暗色大厅沿用聊天页的 token）。用户的选择保存在 `localStorage['anonchat.theme']` 中（取值 `light|dark`），只影响大厅类路由。
- 实现方式：新增 `useRouteTheme()`，根据当前路由和用户偏好算出 `light|dark`，写到 `<html data-theme>` 上。组件通过 `data-[theme=dark]` 变体或 CSS 变量取色。
- `/admin` 使用大厅浅色 token，不显示顶栏和抽屉。

### 1.4 公共组件（`frontend/src/components/`）

| 组件 | 说明 |
|---|---|
| `GradientAvatar` | 替换现有 `Avatar`（同名导出保留，避免大面积改动）。圆形，背景是 `linear-gradient(angle, 品牌紫, 辅助粉)`，angle 和两个色标的明度由 seed 的哈希决定，只在两种强调色的范围内变化。可选在中心显示一个字（大厅资料卡中显示昵称首字）或一个 emoji。不使用像素图案 |
| `Card` | 大厅卡片：白底，`rounded-[24px]`，`border-lobby-line`，`shadow-soft`，进入视口时 fade-up |
| `GlassPanel` | 聊天面板：`bg-glass backdrop-blur-[20px] border border-glass-line rounded-[24px]` |
| `Bubble` | 对方气泡用 `bubble-peer`；自己的气泡用 `linear-gradient(135deg,#8B7CF6,#A78BFA)` 白字；`rounded-[20px]`，入场 fade-up |
| `TypingDots` | 三个品牌紫光点，使用 `animate-breathe` |
| `Toggle`、`Segmented`、`RangeSlider`、`TagPicker`、`Sheet`（移动端底部抽屉 / 桌面端居中对话框） | 表单控件，统一使用 12px 圆角，激活态为品牌紫 |
| `Marquee` | 公告跑马灯。CSS transform 匀速滚动，速度 40px/s；hover 时暂停；只有一条且放得下时不滚动 |
| `Drawer` | 汉堡菜单抽屉，从左侧滑入（200ms），带遮罩，支持 Esc 关闭，有焦点陷阱 |
| `VipLock` | 半透明遮罩，加上居中胶囊按钮"🔒 激活码解锁 VIP 特权 →"，点击打开兑换对话框 |

### 1.5 匿名代号

1v1 聊天场景（match / bottle / private）中**不显示昵称**，改为显示匿名代号：`匿名·XXXX`，其中 `XXXX` = `sha256(conversationId + ':' + memberId)` 的前 4 位，转为大写十六进制。由前端纯函数 `codename(conversationId, memberId)` 计算。同一会话内保持稳定，不同会话之间无法关联。自己显示为"我"。房间聊天继续显示昵称（群聊需要称呼）。故事剧场显示角色名。

## 2. 页面设计

### 2.1 应用壳（大厅类路由）

```
┌──────────────────────────────────────────────────────────┐
│ ☰      AnonChat 匿名聊天                     👤+  📱  ☀ │  顶栏 64px，白底（暗色时为玻璃底）
├──────────────────────────────────────────────────────────┤
│ 📢 公告跑马灯（无公告时整条隐藏）                         │  40px，brand/10 底
└──────────────────────────────────────────────────────────┘
```

- ☰：打开抽屉导航。条目：首页、随机匹配、话题房间、匿名信、私密链接、故事书（公测）、VIP 与激活码、换个身份。底部显示匿名编号和连接状态点。
- 👤+：邀请好友，即创建私密链接，跳转 `/p/new`。
- 📱：添加到主屏幕。浏览器触发了 `beforeinstallprompt` 时，直接调用原生安装；否则弹出 Sheet，分别给出 iOS 和 Android 的添加步骤说明。
- ☀/🌙：大厅明暗切换。

### 2.2 首页 `/`（Bento Grid，容器最大宽度 1200px，12 列）

```
桌面（≥1024px）
┌───────────────────────────────────────────────────────────────┐
│ [广播卡 span-12] 📣 广播 · 内容……            （无广播时显示占位文案，点击去 /vip） │
├───────────────────────────────────────┬───────────────────────┤
│ 品牌行：AnonChat 匿名聊天 / ANONYMOUS CHAT        ● 已连接       │
├───────────────────────────────────────┬───────────────────────┤
│ [资料+匹配大卡 span-8 row-span-2]      │ [在线人数 span-4]      │
│  (沉) AC****X0Y1 ⧉  ⤴      ⟳  ˄      │  1,234 在线            │
│  昵称  沉稳的旅人            🔀 ✎     │  匹配中 56 · 房间 320   │
│  性别  ○男 ○女 ●保密                  ├───────────────────────┤
│  年龄  ──●──────────── 18             │ [匿名动态 span-4]       │
│  城市  [浙江 ▾] 公开 ◯   清流模式 ◯    │  · 📢 公告标题          │
│  标签  音乐 · 夜猫子 ›                │  · 📣 广播内容          │
│  匹配  [男][女][不限]   ← VipLock      │  · 📖 作品《雨夜书店》   │
│  同省  同省匹配 ◯       ← VipLock      │                        │
│            ( 匹配 )  ← 160px 圆，紫→粉渐变，外圈 brand/10 光环      │
│      点击开始匹配，遇见有趣的人         │                        │
├───────────────────────────────────────┼───────────────────────┤
│ [话题房间 2×2 span-8]                  │ [快捷入口 span-4]       │
│  💬 情感   💼 职场                     │  ✉️ 匿名信 →            │
│  🎮 游戏   🌙 夜聊                     │  🔗 私密链接 →          │
│  （每格：柔和渐变底 + emoji + 名称 + 在线数）│  📖 故事书 · 公测 →     │
└───────────────────────────────────────┴───────────────────────┘
右下角浮动胶囊（桌面端）：✉️ 匿名信、📖 故事书 · 公测。移动端不显示浮动胶囊，改用快捷入口卡。
移动端（<768px）：单列，顺序为 广播 → 资料+匹配 → 在线人数 → 话题房间（2×2 保持两列）→ 快捷入口 → 匿名动态。
```

- 资料卡中的头像使用 `GradientAvatar`，中心显示昵称首字。
- 匿名编号以掩码形式显示：前 2 位 + `****` + 后 4 位。复制按钮复制完整编号，复制后 toast 提示"已复制匿名编号，仅用于联系客服"。
- ⤴ 分享：调用 `navigator.share({ url: location.origin })`，不支持时复制站点链接。分享内容不含任何身份信息。
- ⟳ 换个身份（需要二次确认，沿用现有逻辑）。˄ 折叠资料区，只保留头像行和匹配按钮，状态保存在 localStorage。
- 资料修改即时保存：防抖 500ms 后调用 `PATCH /api/identity/profile`，失败时回滚并 toast 提示。
- 🔀 随机昵称：调用 `POST /api/identity/nickname/shuffle`。✎ 打开编辑框，支持 2–12 个字符，提交后服务端做违禁词检查。
- 年龄滑杆范围 18–80，旁边有"不填"选项，未设置时显示"保密"。
- 城市：选择省级行政区（34 项，列表写死在前端和后端的常量中），"公开"开关决定对方能否看到。
- 匹配偏好和同省开关只对 VIP 生效。非 VIP 时区域上盖 `VipLock`；VIP 时显示"VIP 至 2026-12-06"徽标。
- "匹配"按钮：先保存偏好，再 `navigate('/match?auto=1')`。匹配页加载后自动发送 `match.start`。

### 2.3 聊天页（暗色玻璃）

```
背景：linear-gradient(160deg,#0F1117,#1A1D29) 全屏
┌ GlassPanel（最大宽度 760px，移动端全屏无圆角）──────────────┐
│ ← (渐变圆) 匿名·7F3A   ♀ 24 · 浙江 · #音乐 #夜猫子   ⋯ │ 头部：对方公开资料芯片，⋯ 里是举报、拉黑、下一位、离开
│──────────────────────────────────────────────────────────│
│        对方气泡 rgba(255,255,255,.08)                     │
│                          自己气泡 紫→淡紫渐变 ▶           │
│  ● ● ●（呼吸光点，表示对方正在输入）                       │
│──────────────────────────────────────────────────────────│
│ [ 输入框（玻璃底） ]                         [发送 渐变圆钮] │
└──────────────────────────────────────────────────────────┘
```

匹配等待态：页面中央是一个大渐变圆，外圈有呼吸光环。下方显示"正在寻找…"、已等待时长、当前生效条件（芯片形式，如"清流 · 只匹配女生 · 同省"）和"取消"按钮。等待满 30 秒且存在硬条件时，追加一行提示："当前条件较严格，可以返回大厅调整"。

### 2.4 其他页面

| 路由 | 风格 | 内容 |
|---|---|---|
| `/rooms` | 浅色 | 房间卡片网格（emoji + 渐变 + 在线数） |
| `/bottles` | 浅色 | 标题改为"匿名信"（副标题"漂流瓶"）。写信卡片使用信纸样式（白底，左侧有一条 brand/40 竖线），以及"捞一封"和"我的信" |
| `/p/new` | 浅色 | 创建私密链接卡片 |
| `/vip` | 浅色 | 当前 VIP 状态、权益说明（性别筛选、同省匹配、广播卡）、激活码输入框、剩余广播卡、"发布广播"表单 |
| `/stories` | 浅色 | 故事书：两个 Tab，"等待开演"（可以认领角色）和"书架"（已收录的作品）；"发起故事"按钮 |
| `/stories/new` | 浅色 | 发起故事表单 |
| `/works/:workId` | 浅色 | 作品阅读页。用对话体排版角色名和正文；有点赞、举报；作者额外有"删除"，参演者额外有"撤回同意" |
| `/stories/:storyId/play` | 暗色 | 剧场聊天，复用聊天页布局。头部显示故事标题和双方角色名；结束后弹出"申请收录 / 同意收录"面板 |

## 3. 后端设计

### 3.1 数据库迁移 `00004_profile_vip_story.sql`

```sql
-- 身份资料与权益
ALTER TABLE identities
  ADD COLUMN public_code     text,                 -- 匿名编号：10 位 Crockford Base32，大写
  ADD COLUMN gender          text NOT NULL DEFAULT 'secret' CHECK (gender IN ('male','female','secret')),
  ADD COLUMN age             smallint CHECK (age IS NULL OR age BETWEEN 18 AND 80),
  ADD COLUMN province        text,                 -- 取值必须在 provinces 常量中，NULL 表示未设置
  ADD COLUMN province_public boolean NOT NULL DEFAULT false,
  ADD COLUMN tags            text[] NOT NULL DEFAULT '{}',   -- ≤5 个，取值必须在 tags 常量中
  ADD COLUMN clean_mode      boolean NOT NULL DEFAULT false,
  ADD COLUMN match_gender    text NOT NULL DEFAULT 'any' CHECK (match_gender IN ('any','male','female')),
  ADD COLUMN match_same_province boolean NOT NULL DEFAULT false,
  ADD COLUMN vip_until       timestamptz,
  ADD COLUMN broadcast_cards int NOT NULL DEFAULT 0 CHECK (broadcast_cards >= 0);
-- 存量身份回填匿名编号（十六进制大写，同样是 10 位，只用于存量数据）
UPDATE identities SET public_code = upper(substr(replace(gen_random_uuid()::text,'-',''),1,10)) WHERE public_code IS NULL;
ALTER TABLE identities ALTER COLUMN public_code SET NOT NULL;
CREATE UNIQUE INDEX identities_public_code_key ON identities (public_code);

-- 房间 emoji
ALTER TABLE rooms ADD COLUMN emoji text NOT NULL DEFAULT '💬';
-- （再按 00003 中的 slug 给种子房间更新 emoji）

-- 会话类型
ALTER TYPE conversation_kind ADD VALUE IF NOT EXISTS 'story';

-- 公告（管理员发布）
CREATE TABLE announcements (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  content    text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 120),
  starts_at  timestamptz NOT NULL DEFAULT now(),
  ends_at    timestamptz,
  created_by uuid NOT NULL REFERENCES admin_users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);

-- 广播（用户使用广播卡提交，人工审核）
CREATE TABLE broadcasts (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  identity_id  uuid NOT NULL REFERENCES identities(id),
  content      text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 60),
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','removed')),
  reviewed_by  uuid REFERENCES admin_users(id),
  reviewed_at  timestamptz,
  reject_reason text,
  show_until   timestamptz,          -- 审核通过时设置为 now()+24h
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON broadcasts (status, show_until);

-- 激活码
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
CREATE INDEX ON activation_codes (batch_id);

-- 故事书
CREATE TABLE stories (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  host_identity_id uuid NOT NULL REFERENCES identities(id),
  title           text NOT NULL CHECK (char_length(title) BETWEEN 2 AND 30),
  opening         text NOT NULL CHECK (char_length(opening) BETWEEN 10 AND 500),
  tags            text[] NOT NULL DEFAULT '{}',
  role_a          text NOT NULL CHECK (char_length(role_a) BETWEEN 1 AND 12),
  role_b          text NOT NULL CHECK (char_length(role_b) BETWEEN 1 AND 12),
  host_role       text NOT NULL CHECK (host_role IN ('a','b')),
  status          text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting','active','ended','expired','removed')),
  conversation_id uuid UNIQUE REFERENCES conversations(id) ON DELETE SET NULL,
  guest_identity_id uuid REFERENCES identities(id),
  active_at       timestamptz,
  ended_at        timestamptz,
  valid           boolean,                    -- 结束时由服务端判定
  host_consent    boolean NOT NULL DEFAULT false,
  guest_consent   boolean NOT NULL DEFAULT false,
  expires_at      timestamptz NOT NULL,       -- 等待态 24h 后过期
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON stories (status, created_at DESC);

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
CREATE INDEX ON story_works (status, created_at DESC);
CREATE TABLE story_work_likes (
  work_id     uuid NOT NULL REFERENCES story_works(id) ON DELETE CASCADE,
  identity_id uuid NOT NULL REFERENCES identities(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (work_id, identity_id)
);

-- 举报扩展：举报对象可以是作品或广播
ALTER TABLE reports ADD COLUMN target_type text NOT NULL DEFAULT 'message' CHECK (target_type IN ('message','bottle','broadcast','work'));
ALTER TABLE reports ADD COLUMN target_id text;
```

- 新生成的匿名编号：10 位 Crockford Base32（字符集 `0-9A-HJKMNP-TV-Z`），使用 `crypto/rand` 生成。插入遇到唯一冲突时重试，最多 3 次。
- `reports.reported_identity_id` 仍然必填：举报作品时填作品的发起人，举报广播时填广播的提交人。
- **留存**：已有的 7 天清理任务同时清理以下内容（新增）：等待超过 24h 的 `stories` 置为 `expired`；`broadcasts` 中 `show_until` 已过 7 天或状态为 rejected 超过 7 天的记录删除；已兑换满 180 天的 `activation_codes` 保留（属于财务凭证，不删除）。`story_works` 不受 7 天留存约束（见 why.md 风险表）。删除故事会话时，`stories.conversation_id` 置空，`stories` 本身保留到对应作品被删除后再删，未生成作品的故事在结束 7 天后删除。

### 3.2 常量（`backend/internal/profile/consts.go`，前端 `src/lib/profileConsts.ts` 保持一致）

- 省份：北京、天津、河北、山西、内蒙古、辽宁、吉林、黑龙江、上海、江苏、浙江、安徽、福建、江西、山东、河南、湖北、湖南、广东、广西、海南、重庆、四川、贵州、云南、西藏、陕西、甘肃、青海、宁夏、新疆、香港、澳门、台湾（共 34 项）。
- 标签（最多选 5 个）：音乐、电影、游戏、动漫、读书、运动、旅行、美食、摄影、宠物、学生党、打工人、夜猫子、早起党、情感树洞、闲聊、学习、二次元。
- 新增一个前端单元测试，断言两份常量完全一致：读取后端 `consts.go` 的文本做比对，或从同一份 JSON 生成。推荐方案：把列表放进 `backend/internal/profile/consts.json`，后端用 `go:embed` 读取，前端测试读取同一个文件做比对。

### 3.3 新模块与职责

| 包 | 职责 |
|---|---|
| `internal/profile` | 资料校验、更新、随机昵称、自定义昵称（过违禁词）、公开资料视图 `PublicProfile` |
| `internal/vip` | 激活码生成、规范化、哈希、兑换（事务内：`SELECT … FOR UPDATE`，校验未兑换、未撤销、未过期，延长 `vip_until = greatest(vip_until, now()) + vip_days`，累加 broadcast_cards），以及 `IsVIP(ident, now)` |
| `internal/feed` | 公告列表、广播提交/列表、匿名动态聚合、在线统计 |
| `internal/story` | 故事发起、认领、开演、结束判定、收录同意、作品生成、作品列表/阅读/点赞/删除/撤回、等待超时 |
| `internal/admin` | 扩展：公告 CRUD、广播审核、激活码批次（生成后只返回一次明文，并提供 CSV 下载）、作品审核与下架、按匿名编号查身份 |

### 3.4 匹配规则（改 `internal/match`）

`IdentityContext` 在入队预取时增加 `Profile` 快照，内容为 gender、age、province、tags、clean_mode、match_gender、match_same_province、is_vip。

兼容判定 `compatible(a, b)` 必须**双向**满足以下全部条件：
1. 现有规则：未互相拉黑；"最近聊过"在 `relaxAfter` 内不配对（现有放宽逻辑不变）。
2. `a.clean_mode == b.clean_mode`。清流用户只匹配清流用户，非清流用户只匹配非清流用户。
3. 如果 a 是 VIP 且 `a.match_gender != 'any'`，要求 `b.gender == a.match_gender`（gender 为保密的人不满足任何性别筛选）。
4. 如果 a 是 VIP 且 `a.match_same_province`，要求 a、b 都设置了省份且两者相同。
5. 非 VIP 的 `match_gender` 和 `match_same_province` 一律视为 any/false（服务端判定，不信任前端）。
6. 硬条件（第 2–4 条）不会随时间放宽。

候选人选择：在所有兼容的候选人中，选标签交集最大的；交集相同时选排队最久的。复杂度保持为单次扫描 O(n)，只在内存中计算。

`match.found` 的负载调整为（**去掉了 nickname 和 avatarSeed**）：
```json
{ "conversationId": "...", "peer": { "memberId": 2, "gender": "female|male|null", "age": 24|null, "province": "浙江|null", "tags": ["音乐"], "cleanMode": true } }
```
`gender` 为 secret 时输出 null；`province_public=false` 时 province 输出 null。
`match.waiting` 的负载增加 `"filters": {"cleanMode":bool,"gender":"any|male|female","sameProvince":bool}`，即服务端最终生效的条件。

### 3.5 REST API（新增或变更）

所有新接口都沿用 V1 的约定：Origin 校验、`__Host-anon` Cookie、JSON 错误 `{"error":{"code","message"}}`、限流。只读的公开接口（统计、动态、公告、作品列表与阅读）也要求有身份，统一使用 `protected`。参与聊天的接口（故事认领、故事消息）使用 `chatProtected`。

| 方法与路径 | 说明 |
|---|---|
| `GET /api/identity/me` | 增加 `publicCode`、`profile{gender,age,province,provincePublic,tags,cleanMode}`、`matchPrefs{gender,sameProvince}`、`vip{active,until}`、`broadcastCards` |
| `PATCH /api/identity/profile` | 部分更新以上 profile 和 matchPrefs 字段，返回完整 me。校验失败返回 `invalid_request`；非 VIP 设置 matchPrefs 时返回 `vip_required` |
| `POST /api/identity/nickname/shuffle` | 生成并保存随机昵称。限流：每身份每分钟 10 次 |
| `PUT /api/identity/nickname` | 设置自定义昵称：2–12 个字符，去掉首尾空白，禁止控制字符；违禁词返回 `content_blocked`。限流：每身份每天 10 次 |
| `POST /api/identity/reset` | 变更：新身份继承 `vip_until` 和 `broadcast_cards`，资料重置为默认，并生成新的匿名编号 |
| `POST /api/vip/redeem` | `{code}`。规范化：去空格和连字符，转大写，把 `O→0`、`I/L→1`。失败统一返回 `invalid_code`，不区分"不存在 / 已用 / 已撤销 / 已过期"，防止被探测。限流：每身份 5 次/10 分钟，每 IP 20 次/10 分钟，超出返回 `rate_limited`。成功返回 me |
| `GET /api/stats/online` | `{online, matching, inRooms}`，服务端缓存 10 秒 |
| `GET /api/announcements` | 当前有效公告（starts_at ≤ now < ends_at 或 ends_at 为空，且未删除），按 created_at 倒序，最多 5 条 |
| `GET /api/broadcasts/current` | 当前展示中的广播（approved 且 show_until > now），最多 3 条，按审核时间倒序 |
| `POST /api/broadcasts` | `{content}`：先过违禁词，再扣 1 张广播卡（事务内，卡数为 0 时返回 `no_broadcast_card`），状态为 pending。限流：每身份每天 5 次 |
| `GET /api/broadcasts/mine` | 自己提交的广播及审核状态 |
| `GET /api/feed` | 匿名动态：合并有效公告、展示中的广播、最近通过的作品（各取最新 10 条），按时间倒序，最多 20 条。每条为 `{type:'announcement'|'broadcast'|'work', id, text, at}`，不含任何身份信息 |
| `GET /api/rooms` | 每个房间增加 `emoji` 字段 |
| `GET /api/stories?status=waiting` | 等待开演的故事列表（不含发起人信息），分页参数 `before` |
| `POST /api/stories` | 发起故事。title、opening 过违禁词；同一身份同时最多有 1 个 waiting 或 active 的故事，超出返回 `quota_exceeded`；expires_at = now+24h |
| `GET /api/stories/{id}` | 故事详情，包含 `myRole: 'host'|'guest'|null`、status、valid、host_consent、guest_consent、conversationId（仅参与者可见） |
| `POST /api/stories/{id}/join` | 认领剩下的角色：status 从 waiting 变为 active，创建 kind=story 的会话，双方加入（发起人 memberId=1，认领者 memberId=2），通过 WS 给发起人推送 `story.started`。不能认领自己的故事；已被认领时返回 `story_taken` |
| `POST /api/stories/{id}/end` | 任一参与者结束。服务端判定 valid：`ended_at - active_at ≥ 5 分钟`，且双方各有 ≥5 条非系统消息。会话标记为 ended，并推送 `story.ended{valid}` |
| `DELETE /api/stories/{id}` | 发起人在 waiting 状态下取消 |
| `POST /api/stories/{id}/consent` | `{agree:bool}`：参与者设置收录同意。只有 valid=true 的故事可以操作；双方都同意时，在事务内生成 `story_works`（status=pending），lines 只含角色和正文 |
| `GET /api/works?before=` | 已通过的作品列表（书架） |
| `GET /api/works/{id}` | 作品详情。只有 approved 的作品对所有人可见；pending 状态只对参与者可见。返回 `liked`、`mine: 'host'|'guest'|null` |
| `POST /api/works/{id}/like` / `DELETE /api/works/{id}/like` | 点赞和取消点赞，幂等，同时维护 like_count |
| `DELETE /api/works/{id}` | 发起人删除作品（状态改为 removed） |
| `POST /api/works/{id}/withdraw` | 参与者撤回同意：作品状态改为 removed |
| `POST /api/works/{id}/report`、`POST /api/broadcasts/{id}/report` | 举报，写入 reports（target_type 为 work 或 broadcast） |

**管理后台**（沿用 `__Host-admin`、admin 中间件，所有写操作都写入 `admin_audit_logs`）：

| 方法与路径 | 说明 |
|---|---|
| `GET/POST /api/admin/announcements`、`DELETE /api/admin/announcements/{id}` | 公告的列表、创建和删除 |
| `GET /api/admin/broadcasts?status=pending` | 广播审核队列 |
| `POST /api/admin/broadcasts/{id}/approve` / `reject {reason}` / `remove` | 驳回时退还广播卡（事务内处理） |
| `GET/POST /api/admin/activation-batches` | 创建批次：`{label, vipDays, broadcastCards, count, expiresAt}`。响应中的 `codes` 明文只出现这一次，格式为 `XXXX-XXXX-XXXX-XXXX` |
| `POST /api/admin/activation-batches/{id}/revoke` | 撤销批次内所有未兑换的码 |
| `GET /api/admin/works?status=pending`、`POST /api/admin/works/{id}/approve` / `reject {reason}` / `remove` | 作品审核与下架 |
| `GET /api/admin/identities/by-code/{code}` | 按匿名编号查身份：返回 identity_id、创建时间、VIP 状态、封禁状态，可以直接发起封禁 |

### 3.6 WebSocket

- 新增 S→C：`story.started{storyId, conversationId}`、`story.ended{storyId, valid}`、`story.consent{storyId, hostConsent, guestConsent, workId?}`。
- 故事会话的消息走现有 `msg.send` 管道（kind=story 的会话）。消息负载中的 `nickname` 字段，对故事会话填角色名，对 1v1 会话（match/bottle/private）**填空字符串**，前端改用匿名代号显示。房间会话保持不变。
- 现有的 `typing` 事件保持不变，前端在 1v1 场景和故事剧场中渲染 `TypingDots`。

### 3.7 新增错误码

`vip_required`、`invalid_code`、`no_broadcast_card`、`story_taken`、`story_not_valid`、`not_found`（V1 没有，本包新增）。前端 `errorMessages.ts` 要同步补充这些错误码的中文文案。

### 3.8 防泄漏

扩展 V1 的泄漏测试（`internal/api` leak test），覆盖所有新增的用户侧接口和 WS 事件：响应中不得出现 `identity_id`、`identityId`、`token`、`ip`、`account`、`phone`、`created_ip`、`vip_until` 等字段，`/api/identity/me` 除外（me 只返回自己的信息）。作品的 `lines` 和动态 `feed` 中不得出现任何 memberId 以外的身份标识。另外，资料字段只在 me 和 `match.found.peer` 中出现。

## 4. 测试策略

- **后端单元测试（RED→GREEN）**：资料校验（省份、标签、年龄边界）；激活码生成与规范化；兼容判定的全部组合（清流、性别、同省、VIP/非 VIP、保密性别）；标签打分；故事有效性判定（4:59 与 5:00、4 条与 5 条消息）。
- **后端集成测试**：兑换（成功、重复兑换、撤销、过期、限流、并发兑换同一个码只能成功一次）；广播扣卡与驳回退卡；故事完整流程（发起 → 认领 → 发消息 → 结束 → 双方同意 → 生成作品 → 审核 → 出现在书架和 feed）；换身份时继承 VIP；匹配偏好端到端（两个 VIP 互相筛选）；防泄漏。
- **前端单元测试**：codename、主题判定 `useRouteTheme`、VipLock 显示逻辑、资料表单的防抖与回滚、Marquee 是否滚动的判定、常量一致性。
- **E2E**（Playwright，数据由 e2eseed 准备，需要扩展 e2eseed：创建管理员和一个可用激活码）：
  1. 设置资料并刷新后仍然保留；
  2. 兑换激活码后 VipLock 消失；
  3. 两个浏览器上下文都开启清流后能配对，并且看到匿名代号和对方的资料芯片；
  4. 提交广播 → 管理员审核通过 → 首页广播卡可见；
  5. 故事书完整流程（测试中把有效判定的阈值通过 `APP_ENV=test` 下的配置项 `STORY_MIN_DURATION` 缩短到 1 秒）；
  6. 原有 5 个 E2E 用例全部通过。
- **视觉检查清单**（由审查者对照截图人工核对）：token 值、圆角、两种强调色、无重阴影、无真人头像、移动端单列、浅→暗过渡、prefers-reduced-motion。
