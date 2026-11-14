# 怎么做：匹配与聊天界面改版 + 白天/夜晚模式

样式稿（只读参考，用户已确认）：https://claude.ai/artifact/UopwHtcACCdvbvPBP2xWd2 。本文件是实施依据，与样式稿有出入时以本文件为准。

## §0 硬性约束

- 只改 `frontend/`。不改后端、WS 协议、数据库。
- 不新增 npm 依赖。图标用内联 SVG（§2.1）。
- 颜色只用 §1 的 token，不要自行调色（对比度已按 WCAG AA 校验）。
- 保留现有可访问名，测试与 E2E 依赖它们：`消息输入框`、`表情`、`发送`、`发送图片或视频`、`选择图片或视频`、`emoji-panel`、`typing`、`message-list`、`match-button`、`ws-connected`、`查看图片`、`图片消息`、`发送中`、`发送失败，点击重试`、`将在 N 秒后焚毁`、`举报`（对话框名）、`提交举报`。
- 管理后台（`/admin`）仍强制浅色，不改其样式。
- 动画遵守 `index.css` 已有的 `prefers-reduced-motion` 全局规则；新增动画用 keyframes + `--animate-*` token。
- 1v1 匹配显示对方真实昵称与头像（现有产品决定，不变）。

## §1 主题：白天（奶白）/ 夜晚（暮紫）

### 1.1 Token

在 `index.css` 中新增一组聊天语义变量。`:root` 放白天值，`html[data-theme='dark']` 放夜晚值。`*-rgb` 变量是空格分隔的三元组，用于 `rgb(var(--x) / α)`。

| 变量 | 白天（奶白） | 夜晚（暮紫） | 用途 |
|---|---|---|---|
| `--c-bg-from` / `--c-bg-to` | `#F7F7FA` / `#EFEFF5` | `#0F1117` / `#1A1D29` | 聊天页背景渐变 |
| `--c-ink` | `#1C1C28` | `#F5F5F7` | 正文 |
| `--c-sub` | `#5E5E70` | `#9A9AA3` | 次要文字、时间、说明 |
| `--c-sys` | `#4A4A5A` | `#C9C9D1` | 系统消息胶囊文字 |
| `--c-ov-rgb` | `28 28 40` | `255 255 255` | 叠加层（玻璃、描边、芯片底） |
| `--c-peer` | `#FFFFFF` | `#252836` | 对方气泡（实色） |
| `--c-self` | `#6F5FE8` | `#6F5FE8` | 自己气泡、发送键激活（白字 4.7:1） |
| `--c-acc` | `#5B4BD6` | `#B4A9FF` | 强调文字：链接、正在输入、选中 |
| `--c-acc-rgb` | `111 95 232` | `139 124 246` | 强调色软底/描边 |
| `--c-acc-soft-ink` | `#4A3BC4` | `#D3CCFF` | 软底芯片/破冰语文字 |
| `--c-skip-rgb` | `180 48 126` | `232 139 196` | 「确认换人？」底/描边 |
| `--c-skip-ink` | `#B4307E` | `#F2B6DA` | 「确认换人？」文字 |
| `--c-danger` | `#D23B40` | `#F2777B` | 屏蔽、结束聊天、发送失败 |
| `--c-danger-rgb` | `210 59 64` | `242 119 123` | 危险软底 |
| `--c-sheet` | `#FFFFFF` | `#1E2130` | 底部菜单/对话框面板 |
| `--c-scrim` | `rgb(20 20 30 / 0.35)` | `rgb(5 6 10 / 0.6)` | 遮罩 |
| `--c-panel` | `rgb(255 255 255 / 0.55)` | `rgb(255 255 255 / 0.04)` | 桌面端聊天面板（GlassPanel） |
| `--gradient-cta` | `linear-gradient(135deg, #6F5FE8, #C2589C)` | 同左 | 每屏唯一主按钮，按钮字 ≥16px 粗体 |

把现有 Tailwind token 改为引用上述变量（类名不变，所有聊天页自动跟随主题）：

- `--color-night-from` → `var(--c-bg-from)`，`--color-night-to` → `var(--c-bg-to)`
- `--color-night-ink` → `var(--c-ink)`，`--color-night-sub` → `var(--c-sub)`
- `--color-glass` → `rgb(var(--c-ov-rgb) / 0.06)`，`--color-glass-line` → `rgb(var(--c-ov-rgb) / 0.08)`
- `--color-bubble-peer` → `var(--c-peer)`

新增 Tailwind token：`--color-bubble-self`、`--color-chat-sys`、`--color-chat-acc`、`--color-chat-acc-soft`（=`--c-acc-soft-ink`）、`--color-chat-danger`、`--color-sheet`、`--color-skip`（=`--c-skip-ink`）。软底/描边用任意值写法，例如 `bg-[rgb(var(--c-acc-rgb)/0.16)]`。再加一个 `@utility bg-cta { background-image: var(--gradient-cta); }`。

`--color-brand` / `--color-accent` / `--color-brand-2` 与大厅 token（`--page` 等）保持不变。

### 1.2 主题切换规则

- `routeTheme()`：`/admin` → `light`；**其余所有路由（含聊天路由）→ 用户偏好**。聊天路由不再强制 `dark`。`isChatRoute()` 仍用于隐藏顶栏，不变。
- `readLobbyTheme()`：localStorage 有 `light`/`dark` 时用它；否则跟随系统（`matchMedia('(prefers-color-scheme: dark)')`，不可用时为 `light`）。`TopBar` 的切换按钮行为不变（切换后写入 localStorage）。
- `BackgroundLayers`：暗色层沿用 `night-from → night-to` 渐变，按主题切 opacity（已有逻辑）。浅色层 `bg-lobby-bg` 不变。

### 1.3 主题相关的顺带修正

- `Sheet`、`BlockConfirm`、`ReportDialog`，以及 `PrivateJoinPage` 的「销毁确认」面板，背景改为 `bg-sheet`，遮罩用 `--c-scrim`。原来的 `bg-lobby-card` 在暗色下是半透明，会透出底下内容。文字继续用 `lobby-ink`/`lobby-sub`（已随主题切换）。
- `KickedOverlay`：确认在白天/夜晚下都有不透明背景，文字可读。
- `index.html` 的 viewport 加 `viewport-fit=cover`。
- 所有聊天页（match / rooms / bottles / private / stories play）的 `h-screen` 改为 `h-[100dvh]`。

## §2 通用组件

### 2.1 `components/Icon.tsx`（新增）

`<Icon name size? strokeWidth? className? />`：24×24 viewBox，`stroke="currentColor"`、圆角线帽，`aria-hidden`。需要的图标和 path：

- `back`：`M15 18l-6-6 6-6`
- `more`：三个实心圆 `cx=5/12/19 cy=12 r=1.8`（fill currentColor）
- `shuffle`：`M16 3h5v5` `M4 20L21 3` `M21 16v5h-5` `M15 15l6 6` `M4 4l5 5`
- `plus`：`M12 5v14` `M5 12h14`
- `smile`：圆 r=9；`M8.5 14.5s1.3 1.8 3.5 1.8 3.5-1.8 3.5-1.8`；`M9 9.5h.01` `M15 9.5h.01`
- `send`（向上箭头）：`M12 19V5` `M5 12l7-7 7 7`
- `flag`：`M5 21V4` `M5 4h12l-2.5 4.5L17 13H5`
- `ban`：圆 r=9；`M5.6 5.6l12.8 12.8`
- `close`：`M18 6L6 18` `M6 6l12 12`
- `arrow-down`：`M12 5v14` `M19 12l-7 7-7-7`
- `alert`：圆 r=9；`M12 8v4.5` `M12 16h.01`
- `sparkle`：`M12 3l1.9 5.6 5.6 1.9-5.6 1.9L12 18l-1.9-5.6L4.5 10.5l5.6-1.9z`

聊天相关页面里作为图标使用的 emoji 和字符（`←` `⋯` `📎` `😊`）全部换成 Icon。表情面板内容本身仍是 emoji。

### 2.2 `Bubble`

- 自己：`bg-bubble-self text-white`，无渐变。对方：`bg-bubble-peer border border-glass-line text-night-ink`，去掉 `backdrop-blur`。
- 字号 15px，行高 1.5，内边距 9px/14px，`whitespace-pre-wrap break-words`。
- 新增 `first`、`last` 两个布尔 prop 控制圆角，基础圆角 18px：
  - 对方：左上 = first ? 18 : 6；右上 18；右下 18；左下始终 6。
  - 自己：左上 18；右上 = first ? 18 : 6；右下始终 6；左下 18。
- 去掉 Bubble 自身的 `max-w`，宽度由 MessageList 行控制（行内最大 75%）。

### 2.3 `MessageList`

新增 props：`conversationKey?: string`（见下方新消息提示）、`showSender?: boolean`（默认 `true`）、`header?: ReactNode`（渲染在所有消息之前）、`footer?: ReactNode`（渲染在所有消息和输入提示之后）。

- **分组**：相邻两条消息属于同一 `memberId`，都不是 system，且 `createdAt` 相差 ≤ 3 分钟（任一缺失视为同组），就算同一组。组内行间距 3px，组间 12px。
- **头像**：对方只在每组最后一条显示（28px），其余位置留 28px 占位；自己不显示头像。
- **昵称**：只在 `showSender && !self && 组的第一条` 时显示在气泡上方。
- **时间分隔**：第一条消息之前，以及与上一条间隔 > 5 分钟时，插入居中的 12px `text-night-sub` 时间。格式：今天 `HH:mm`；昨天 `昨天 HH:mm`；更早 `M月D日 HH:mm`。没有 `createdAt` 时不插。
- **系统消息**（`kind === 'system'`）：居中胶囊，`text-chat-sys bg-glass rounded-full px-3 py-1.5 text-xs`，不带头像和昵称。
- **发送状态**：`sending` 时气泡 `opacity-60`，并保留一个 `sr-only` 的「发送中」文字。`failed` 时在气泡左侧放 32px 图标按钮（`alert`，`text-chat-danger`，可访问名「发送失败，点击重试」，点击调用 `onRetry`）。
- **正在输入**：渲染为对方一侧的小气泡（对方头像 + 圆角 `18 18 18 6` + 三个 `bg-chat-acc` 的呼吸点），外层保留 `data-testid="typing"`。头像取最近一条对方消息的 `avatarSeed`，没有时用 `anonymous`。
- **新消息提示**：用户不在底部（现有 `atBottom` 判断）时，统计新到达的消息数 N（N>0）。**按消息 id 判断，不能用列表长度差**：store 最多保留 500 条，焚毁也会让长度变小。只把 id 大于「已见最大 id」的消息算作新消息；「已见最大 id」只增不减（删除、裁剪、加载历史都不会让它变小，也不计入新消息）。会话切换用显式的新 prop `conversationKey?: string` 判断（各聊天页传入 conversationId 或 roomId）：它变化时计数归零、已见最大 id 重置为新列表的最大 id。**不要根据首条消息 id 推断会话切换。**自动滚到底的判断同样改为按最大 id 变化，在列表右下角浮出按钮「↓ N 条新消息」（`bg-bubble-self text-white rounded-full h-8 px-3 text-xs`，可访问名 `${N} 条新消息`）。点击后滚到底部并清零；用户自己滚到底部也清零。
- **媒体**：图片/视频最大宽度改为行宽的 60%（上限 240px），保留现有 alt/aria。
- 焚毁倒计时文案和颜色保持不变（颜色改为 `text-chat-acc`）。
- 列表容器隐藏滚动条外观（保持可滚动），内边距 `px-3 py-4`。

### 2.4 `Composer`

布局（从左到右）：

1. 媒体按钮：44px 圆形，`bg-glass`，`plus` 图标，可访问名不变。上传中图标换成 18px 转圈（`border-2` 环 + `animate-spin`），按钮禁用。**不要再把按钮文字换成「上传中…」**。
2. 输入胶囊：`flex-1 min-h-11 rounded-[22px] bg-glass border border-glass-line`，`focus-within` 时描边为 `rgb(var(--c-acc-rgb)/0.55)`。内部是 textarea（`rows=1`，按 `scrollHeight` 自动增高，最多 5 行，超出后内部滚动，`placeholder="说点什么…"`）+ 右侧 40px 表情按钮（`smile`，面板打开时为 `text-chat-acc`，否则 `text-night-sub`）。
3. 发送按钮：44px 圆形，可访问名「发送」，`send` 图标。不可发送时 `bg-glass text-night-sub scale-[0.92]` 且禁用；可发送时 `bg-bubble-self text-white scale-100`。150ms 过渡。

- 字数计数只在 `len > maxChars * 0.9` 时显示（`len / max`，超限为 `text-chat-danger`）。
- 表情面板在输入行下方，`grid-cols-8 sm:grid-cols-10`，每格 44px 高、`text-2xl`，最大高度约 248px，可滚动。
- 外层 `border-t border-glass-line bg-[rgb(var(--c-ov-rgb)/0.02)]`，底部内边距 `max(12px, env(safe-area-inset-bottom))`。
- Enter 发送 / Shift+Enter 换行、输入中节流、`maxChars`、`disabled`、媒体错误提示等现有行为不变。

### 2.5 其它

- `TypingDots`：点改用 `bg-chat-acc`。
- `GlassPanel`：背景改为 `bg-[var(--c-panel)]`，描边 `border-glass-line`，去掉 `backdrop-blur`；移动端全屏无圆角（现有类名控制）。
- 1v1 聊天页（`BottleConversationPage`、`PrivateJoinPage`）给 MessageList 传 `showSender={false}`。房间和故事页保持默认。

## §3 匹配页 `MatchPage`

### 3.1 状态与路由

- `machine.ts` 的状态不变（`idle | waiting | chatting | ended`）。新增纯函数：
  - `peerSummary(peer?)` → `['女', '23', '广东']`（性别 `female → 女`、`male → 男`，年龄，公开省份；缺失项跳过）。
  - `icebreakers(peerTags, myTags)` → 3 条破冰语：`'嗨，你好呀'`；有共同标签 X 时 `你也喜欢X吗？`，否则对方有标签 Y 时 `你也喜欢Y吗？`，否则 `'在忙什么呢'`；第三条 `'最近有什么开心的事吗'`。
  - `commonTags(peerTags, myTags)` → 交集，保持对方标签顺序。
  - 现有 `peerChips` 不再被 MatchPage 使用。如果没有其它调用方就删除，并同步删掉它的单测。
- 取消匹配：发送 `match.cancel` 后 `nav('/')`。
- 结束聊天（返回确认里的「结束聊天」）：发送 `match.leave` 后 `nav('/')`。
- `idle` 只在直接访问 `/match`（不带 `auto=1`）时出现，用 §3.2 的搜索页外壳渲染：中心是自己的头像（无涟漪），标题「随机匹配」，说明「和陌生人一对一随机聊天」，主按钮「开始匹配」（`bg-cta`），文字按钮「返回大厅」。
- 自己的头像和昵称：`useIdentityStore` 的 `avatarSeed` 与 `nickname`。自己的标签：`me?.profile.tags ?? []`。

### 3.2 搜索中（waiting）

纵向居中，从上到下：

- 顶部 56px 居中小标题「随机匹配」（15px 600）。
- 240px 区域：3 道涟漪环（120px 圆、1.5px 描边：两道 `rgb(var(--c-acc-rgb)/0.6)`，一道品牌粉 55%；`scale .62 → 2.15`、opacity `.75 → 0`；2.4s；分别延迟 0 / 0.8s / 1.6s）。中心是自己的 `GradientAvatar`（104px，label = 昵称首字），外加 6px 背景色描边 + 1px 强调色描边。
- 标题「正在寻找有趣的人」（20px 600）。下方提示每 4 秒轮换：`耐心一点，好聊的人值得等` / `匹配成功后会提醒你，可以先切出去` / `换个标签，也许能遇到更合拍的人`。
- 信息行：`mm:ss`（tabular-nums），加 `· N 人正在匹配`（前面有一个呼吸的 `bg-chat-acc` 小点）。N 来自 `onlineStats().matching`：进入 waiting 时拉取，之后每 10 秒拉一次；请求失败时隐藏这一段。
- 条件芯片（现有 `filterChips`），样式为 `bg-glass border-glass-line rounded-full h-7 px-3 text-xs`。
- 等待 ≥30 秒且有硬条件时，显示提示卡：「条件比较严格，放宽一点会更快匹配到」+ 文字按钮「调整条件」（`text-chat-acc`）。点击后取消匹配并回首页。
- 底部全宽描边按钮「取消匹配」（48px 高，`border-[rgb(var(--c-ov-rgb)/0.14)]`）。

### 3.3 匹配成功过渡

- 收到 `match.found` 后，正常进入 chatting 并渲染聊天界面，同时在上面盖一层 `fixed inset-0` 的过渡层，900ms 后自动消失，点击也可以立即关闭。这样单测和 E2E 都能直接找到聊天元素（Playwright 会等遮挡消失）。
- 过渡层内容：
  - 背景：页面背景加居中的径向光晕 `rgb(var(--c-acc-rgb)/0.22)`。
  - 自己和对方的头像（96px）从两侧滑入，叠压 18px；中间下方有一个 36px 的 `bg-bubble-self` 圆，里面是 `sparkle` 图标。
  - `匹配成功`（13px，`text-chat-acc`，字间距 0.08em）。
  - 对方昵称（24px 700）和 `peerSummary` 芯片。
  - 有共同标签时显示「你们都喜欢 X」。
  - 一条 160px 宽的进度条，900ms 线性走满。
- 页面处于后台（`document.hidden`）时：`document.title` 在「【匹配成功】」和原标题之间每秒交替，直到页面回到前台（`visibilitychange`），并调用一次 `navigator.vibrate?.(80)`。卸载时恢复原标题。

### 3.4 聊天中（chatting）

**头部**（60px，`border-b border-glass-line`）：

- 返回：44px 图标按钮（`back`，可访问名「返回」），点击后打开「结束确认」。
- 对方头像 36px。
- 昵称（15px 600，单行截断）。
- 副标题：对方正在输入时显示「正在输入…」（`text-chat-acc`）；否则显示 `peerSummary`。**每一项都是独立的 `<span>`**，中间用 `aria-hidden` 的 `·` 分隔（E2E 用 exact 文本匹配年龄和省份）。
- 「换一个」：34px 高的描边胶囊（`shuffle` 图标 + 文字）。
  - 第一次点击：按钮变成「确认换人？」（`bg-[rgb(var(--c-skip-rgb)/0.14)] border-[rgb(var(--c-skip-rgb)/0.6)] text-skip`），头部下方出现一条小提示「再点一次换下一个人，3 秒后自动取消」。
  - 3 秒内再点：调用 `next()`，按钮显示「换人中…」并禁用，直到状态变化；5 秒内没有变化就恢复。
  - 3 秒内没点：恢复原状。
- 「⋯」：44px 图标按钮（可访问名「更多操作」），打开操作菜单。

**操作菜单**：用 `Sheet`（标题「更多操作」）。

- 顶部是对方头像 + 昵称 + `peerSummary` 与 `#标签`。
- 「举报」（`flag` 图标，说明「勾选对方的消息并说明原因」）：关闭菜单，打开现有 `ReportDialog`。
- 「屏蔽」（`ban` 图标，`text-chat-danger`，说明「屏蔽对方并结束这段对话」）：关闭菜单，打开现有 `BlockConfirm`。
- 「取消」。

**结束确认**：用 `Sheet`（标题「结束这段对话？」）。

- 说明：「离开后对方会看到「对方已离开」，这段对话无法再继续。」
- 「结束聊天」：危险软底按钮，`bg-[rgb(var(--c-danger-rgb)/0.14)] text-chat-danger`，执行 §3.1 的结束聊天。
- 「继续聊」：描边按钮。

**消息区**：`MessageList`，参数为 `showSender={false}`、`header=<对方名片>`、`typing`。

- 对方名片：`rounded-[20px] bg-[rgb(var(--c-ov-rgb)/0.04)] border-glass-line p-5`，内容居中。
  - 头像 56px、昵称 17px 600。
  - 芯片：性别·年龄、省份（`bg-[rgb(var(--c-ov-rgb)/0.07)]`）、`#标签`（`bg-[rgb(var(--c-acc-rgb)/0.16)] text-chat-acc-soft`）。
  - **自己还没发过消息时**，显示说明文字（有共同标签时为「你们都喜欢 X，打个招呼吧」，否则为「打个招呼吧」）和 3 个破冰语按钮（36px 胶囊，`border-[rgb(var(--c-acc-rgb)/0.4)] bg-[rgb(var(--c-acc-rgb)/0.1)] text-chat-acc-soft`）。点击按钮直接 `chat.send(text)`。

**输入区**：`Composer`。

### 3.5 已结束（ended）——原地结束

- 头部保持不变：副标题改为「已离开」，隐藏「换一个」，保留「⋯」，因为结束后仍可以举报。
- 消息区保留全部历史，`footer` 放一个系统胶囊，内容是 `endReasonText(endReason)`。
- 输入区换成结束栏：
  - 居中说明「对话已结束，聊天记录仍可往上翻看」。
  - 两个按钮：「返回大厅」（描边，`nav('/')`）、「再来一个」（`bg-cta`，`shuffle` 图标，`startMatch()`）。
- 从结束态点「再来一个」，收到 `match.waiting` 后进入搜索页（现有 reducer 行为）。

### 3.6 布局

`<main class="mx-auto flex h-[100dvh] max-w-[760px] flex-col p-0 md:p-4">` + `GlassPanel`，与其它聊天页一致。搜索页、过渡页和 idle 不套 GlassPanel，全屏显示。

### 3.7 状态边界（审查修订）

- **有效对方**：`peer = state.peer ?? chat.peer`，`memberId`、昵称、头像都从它取（举报也一样）。断线恢复（resume）后没有 `match.found`，`chat.peer` 只有 memberId/昵称/头像，性别、年龄、省份、标签缺失时直接省略，不去补请求。结束态也能举报。
- **破冰语与发送**：破冰语只在 `status === 'chatting'` 且自己没发过消息时显示。MatchPage 传给名片和 Composer 的发送函数都要检查 `status === 'chatting'`，非 chatting 时什么都不做。
- **换一个的状态**：确认态、请求中状态和它们的定时器，都绑定到当前的 `status + conversationId`，任一变化立即重置并清掉定时器，旧会话的定时器不能影响新会话。被限流时（服务端只回 error toast，状态不变），5 秒后恢复按钮，当前消息和输入区保持可用。卸载时清理定时器。
- **过渡层与标题提醒**：都绑定到触发它们的 conversationId。以下情况立即撤销过渡层、停止提醒并恢复标题：
  - 该会话结束（ended）；
  - 重新排队（waiting）；
  - 收到新的 found（换了会话）；
  - 组件卸载。
  过渡层显示期间，底层聊天区设置 `inert`（或 `aria-hidden` + 禁止聚焦），关闭后恢复；过渡层本身是一个 `button`（可访问名「进入聊天」），点击或按 Esc 关闭。
- **开始匹配（乐观进入等待态）**：`startMatch()`（`auto=1` 首次进入、idle 的「开始匹配」、结束栏的「再来一个」）统一按下面的规则。
  1. **请求 id**：`WsClient.send()` 改为返回本次帧的 id（string），现有调用方不受影响，不改协议。服务端对 `match.start` / `match.next` 的错误会带回同一个 id（`SendError(env.ID, …)`）；前端用 `ws.on('error', (data, env) => …)` 接收，**envelope 是第二个参数**，`env.id` 就是请求 id。测试桩也必须传入第二个参数，并断言它和 `send()` 返回的 id 一致。
  2. **发起**：先存当前 reducer 状态的快照 `beforeStart`，再 `dispatch({ type: 'start' })` 进入 waiting，然后发送 `match.start`，记下 `pendingStartId = 返回的 id`。这样带 `auto=1` 进入时不会闪出「开始匹配」；卸载和取消沿用 waiting 的现有清理（发送 `match.cancel`）。
  3. **成功**：收到 `match.found`、resume，或者用户主动取消时，清空 `pendingStartId`。**收到 `match.waiting` 时不清空**：服务端是先发 waiting 再入队，入队失败还会再发一个同 id 的 error。
  4. **失败**：收到 `error`，且 `env.id === pendingStartId`，且当前仍是 waiting 时，视为这次开始匹配失败，用新增的 reducer action `{ type: 'restore', state: beforeStart }` 回到快照，并清空 `pendingStartId`。从结束态重试失败时回到原来的结束态，原会话 id 和历史都保留。id 不相同的 error（发消息限流、其它请求、旧请求）一律忽略。已经进入聊天后，迟到的错误不会回滚（此时 `pendingStartId` 已清空）。错误提示沿用 App 里已有的全局 toast。
  5. **不做超时回滚**：没收到回应不代表服务端没有入队，所以保持等待态，用户随时可以取消。迟到的 `match.waiting`、`match.found` 按正常流程处理。
  6. **`match.next`（换一个第二次点击）**：同样记下请求 id。收到同 id 的 error 时：
     - 仍是 chatting（被限流）→ 保持聊天，按钮恢复；
     - 已进入 waiting（服务端已结束旧会话，但入队失败）→ 回到 idle。
- **消费 `auto=1`**：发起开始匹配后，立即用 `replace` 去掉 URL 里的 `auto` 参数。否则在聊天中刷新页面会再次发送 `match.start`，服务端会把进行中的会话当成换人结束掉（`backend/internal/match/service.go` 的 `handleStart`）。
- **StrictMode（`main.tsx` 已启用）**：`handleStart` 并非没有副作用，不能再依赖「重复开始匹配是幂等的」这一说法。要求如下：
  - 自动开始用一个 `useRef` 守卫（`autoStartedRef`），只发送一次。StrictMode 在模拟卸载/重新挂载时会保留同一实例的 ref，所以 setup → cleanup → setup 只会产生一次 `match.start`。URL 的 replace 只用于防止刷新时再次发送，不能靠它防 StrictMode 重放。
  - `beforeStart` 和 `pendingStartId` 存在 ref 里，只在**真正发出**请求时写入，守卫拦下的重复调用不能覆盖它们。
  - 卸载时的 `match.cancel` 只在真实卸载时发送：StrictMode 模拟卸载后紧接着的重新挂载，不能留下一条「已取消」的排队。实现时用 effect 内的 ref 标志区分，例如 cleanup 时先标记，下一次 setup 再清除标记；或者用 `queueMicrotask` / `setTimeout(0)` 延迟到确认确实没有重新挂载再发送。具体写法由实施者决定，但必须有测试覆盖。
  - 测试在 `<StrictMode>` 下渲染 MatchPage，验证三点：
    - 带 `auto=1` 只发出一次 `match.start`，且没有发出 `match.cancel`；
    - 收到同 id 的 error 后回到 idle，而不是停在 waiting；
    - 真实卸载时发出一次 `match.cancel`。
  - 再加一条回归测试：渲染已消费过 auto 的 `/match`（没有 `auto` 参数）并 resume 进入聊天，此时不发送 `match.start`。

## §4 测试

- **必须同步迁移的旧断言**（在对应阶段内完成，不能留到 P4）：
  - `MatchPage.test.tsx:66/79` 用全页 `findByText` 找昵称和「对方」；新增过渡层和名片后昵称会重复，改为限定在 `header` 内查询（P3.2）。
  - `MessageList.test.tsx:54` 用 `getByText('发送失败，点击重试')` 找重试按钮，改为 `getByRole('button', { name: '发送失败，点击重试' })`（P2.3）。
  - `useRouteTheme.test.ts:17/29` 断言聊天路由强制暗色、私密页进入会话后切暗色，改为跟随偏好（P1.2）。
- **单测（TDD，先写失败用例）**：
  - `machine`：`peerSummary`、`icebreakers`、`commonTags`。
  - `MessageList`：分组与头像位置、`showSender=false` 不显示昵称、时间分隔、系统胶囊、新消息提示按钮、`header`/`footer` 插槽，原有用例不回退。
  - `Composer`：900 字以内不显示计数；发送按钮可访问名仍为「发送」，无内容时禁用。
  - `useRouteTheme`：所有聊天路由和 `/p/:token`（进入会话前后）都跟随偏好（light/dark 两种都测）；`/admin` 强制 light；未设置时跟随 `matchMedia`；`isChatRoute` 不变，`/p/:token` 进入会话后仍隐藏顶栏、退出后恢复。
  - `MatchPage`：
    - waiting 显示「正在寻找有趣的人」；
    - 取消后发送 `match.cancel` 并跳回首页；
    - 「换一个」第一次点击不发送，第二次才发送 `match.next`；
    - 「更多操作」→「举报」打开 ReportDialog；
    - 结束后历史消息仍在，显示结束原因和「再来一个」；
    - 返回 → 结束确认 →「结束聊天」发送 `match.leave`；
    - 破冰语点击后发出对应消息，自己发过消息后破冰语消失；未发消息就结束时，名片里不再有破冰按钮；
    - resume 后头部显示 `chat.peer` 的昵称，举报使用 `chat.peer.memberId`，结束后仍能举报；
    - 换一个：3 秒不点自动取消；第二次点击后 5 秒无状态变化则恢复；会话切换（新的 found）时确认态被重置；
    - 过渡层：found 后立刻 ended 时过渡层和标题提醒都被撤销；重复 found 只保留最新会话的过渡；页面回到前台时恢复标题；卸载时恢复标题；
    - `auto=1`：挂载后立即显示搜索页、不出现「开始匹配」，发送 `match.start`，并从 URL 去掉 `auto`；等待期间卸载会发送 `match.cancel`。
    - 开始匹配的错误处理：
      - 同 id 的 error 回到 idle；
      - waiting 之后到达的同 id error（入队失败）也回到 idle；
      - 从 ended 点「再来一个」后收到同 id error，回到 ended，历史消息和结束栏都还在；
      - 不同 id 的 error（例如发消息限流）不回滚；
      - 旧请求 id 的 error 不影响新请求；
      - found 之后迟到的同 id error 不会退出聊天。
    - `match.next` 的错误处理：仍在聊天时收到同 id error，保持聊天；已进入 waiting 后收到同 id error，回到 idle。
    - `WsClient.send()` 返回帧 id（`client.test.ts` 补一条用例）。
  - `MessageList` 新消息计数：列表达到 500 条（长度不再增长）后来新消息，仍然提示；删除（焚毁）若干条后再来新消息，计数正确；加载更早的历史不计入；已有未读计数时，因裁剪、加载历史或删除首条导致首条 id 变化，计数不归零；`conversationKey` 变化时归零。
- **E2E**：
  - `match.spec.ts`：`下一个` 改为先点「换一个」再点「确认换人？」，B 端仍断言「对方跳过了你」。
  - `report-ban.spec.ts`：先点「更多操作」，再点「举报」。
  - `clean-match.spec.ts`：头部年龄和省份仍能 exact 匹配，原则上不需要改。
  - `visual.capture.ts`（独立配置，用 `pnpm capture:visual` 运行）：等待文案改为「正在寻找有趣的人」。另外补充截图：白天和夜晚两种主题下，桌面（1280×800）和移动（390×844）的聊天中、操作菜单打开、结束态。两个页面配对的写法参考 `e2e/helpers.ts`。截图输出到 `e2e/__screenshots__/`，不入库。
- 现有测试中与本次可见行为变化冲突的断言，按新行为更新，并在汇报里逐条列出。

## §5 验证命令

在 `frontend/` 下执行：

- `pnpm typecheck`
- `pnpm lint`
- `pnpm test`
- `pnpm build`
- `pnpm capture:visual`：视觉截图，同样自启服务；审查者会逐张查看截图。
- `pnpm e2e`：自启后端和 Vite，需要本机 PostgreSQL `127.0.0.1:55432` 的 `anonchat_e2e` 库，且 8080、5173 端口空闲。
