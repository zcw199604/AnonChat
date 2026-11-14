# 任务清单: 匹配与聊天界面改版 + 白天/夜晚模式

目录: `helloagents/main/plan/202611141100_chat-ui-day-night/`

## 给实施者（Codex）的执行约定

- 实施者：Codex（`gpt-6.1-sol`，reasoning medium）。审查者：Claude，每个阶段结束后复核。
- **设计以 `how.md` 为准**。`how.md` 没写到的地方，沿用现有代码的写法，不要自行扩大范围。确实需要偏离时，先在汇报里说明原因。
- 开始前通读 `why.md`、`how.md`（尤其是 §0 硬性约束），再读相关的现有代码：`frontend/src/index.css`、`frontend/src/app/useRouteTheme.ts`、`frontend/src/lib/theme.ts`、`frontend/src/components/{Bubble,MessageList,Composer,TypingDots,GlassPanel,Sheet,BlockConfirm,ReportDialog,KickedOverlay}.tsx`、`frontend/src/features/match/{MatchPage,machine,WaitingOrb}.tsx|ts`，以及它们的测试。
- 只改 `frontend/` 和本方案包的 `task.md`。不要改后端；不要新增 npm 依赖。
- 每个阶段完成后，运行该阶段列出的验证命令。全部通过后，把对应条目的 `[ ]` 改为 `[√]`，然后**停下来汇报**：
  - 改了哪些文件；
  - 验证命令的输出摘要；
  - 与设计的偏差及原因。
- 标注 TDD 的任务：先写失败的测试（RED），再写实现（GREEN），最后整理（REFACTOR）。汇报时附上 RED 阶段失败输出的摘要。
- 不要 git commit。不要回滚或改动与本任务无关的文件：工作区里已有的 `analysis/`，以及 `helloagents/main/plan/202611062140_ui-revamp-profile-vip-story/task.md` 的改动，都不要动。
- 不要连接任何远程或生产环境。E2E 只用本机 PostgreSQL（`127.0.0.1:55432`，库 `anonchat_e2e`）。

通用验证命令（在 `frontend/` 下执行）：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build`、`pnpm e2e`

---

## P1. 主题 token 与白天/夜晚模式
- [√] P1.1 `index.css` 按 how.md §1.1 新增 `--c-*` 变量（白天、夜晚两套），把 night/glass/bubble-peer token 改为引用这些变量，新增 bubble-self / chat-* / sheet / skip token 与 `bg-cta` 工具类
  - 执行模式: AFK
  - 涉及文件: `frontend/src/index.css`
  - 完成标准: 类名不变；`html[data-theme='dark']` 下取值与夜晚列一致，默认取值与白天列一致
  - 验证方式: `pnpm build`
- [√] P1.2 主题规则（how.md §1.2）：聊天路由跟随用户偏好；未设置时跟随系统。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/app/useRouteTheme.ts`, `frontend/src/lib/theme.ts`, `frontend/src/app/useRouteTheme.test.ts`
  - 完成标准: 符合 how.md §4 的 useRouteTheme 用例：所有聊天路由与 `/p/:token` 进入会话前后都跟随偏好；`/admin` 恒为 light；无存储值时按 `matchMedia` 返回；顶栏显隐不变。旧断言 `useRouteTheme.test.ts:17/29` 在本阶段迁移
  - 验证方式: `pnpm test -- useRouteTheme`
- [√] P1.3 how.md §1.3 的顺带修正：`Sheet`、`BlockConfirm`、`ReportDialog`、私密页「销毁确认」的面板改用 `bg-sheet`、遮罩改用 `--c-scrim`；`KickedOverlay` 背景不透明；viewport 加 `viewport-fit=cover`；5 个聊天页 `h-screen` → `h-[100dvh]`
  - 执行模式: AFK
  - 涉及文件: 上述组件、`frontend/index.html`、`features/{match,rooms,bottles,private,stories}` 中的聊天页
  - 完成标准: 白天/夜晚下对话框不透明，文字可读
  - 验证方式: `pnpm typecheck && pnpm lint && pnpm test`

### P1 实施证据（2026-11-14）

- RED：先迁移主题旧断言并补充系统偏好、私密会话进入/退出与顶栏判定用例，再执行 `pnpm test -- useRouteTheme`，退出码 1；5 failed / 220 passed。失败对应聊天路由及私密会话仍强制暗色、未读取系统主题（包含 matchMedia 未调用断言），与目标行为一致。
- GREEN：实现后 `pnpm test -- useRouteTheme` 退出码 0，45 files / 225 tests passed。当前脚本的 `--` 传参实际执行了全套测试；主题文件自身为 12 个用例。
- VERIFY：`pnpm typecheck && pnpm lint && pnpm test` 退出码 0，45 files / 225 tests passed；`pnpm build` 退出码 0，117 modules transformed；`git diff --check` 通过。
- 未进行额外重构。P1.1/P1.3 为 CSS、viewport 与布局样式调整，以构建、静态核对和既有测试验证；浏览器视觉截图留待 P4，未宣称完成视觉验收。
- 设计偏差：无功能偏差。`KickedOverlay` 已使用两端均为实色的 night token 渐变及对应文字 token，新增两套 token 后已满足要求，无需修改该文件。额外只同步了 `App.tsx`、`stores/ui.ts` 中与主题规则冲突的注释。
- 已迁移的旧断言：聊天路由「固定暗色」改为两种用户偏好均保持；私密页「进入后切暗色」改为进入/退出均保持偏好，同时验证顶栏判定。
- 本轮停止于 P1；P2–P4 未开始，未提交 git commit。

## P2. 通用聊天组件
- [√] P2.1 新增 `components/Icon.tsx`（how.md §2.1）
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/Icon.tsx`
  - 完成标准: 12 个图标齐全，`aria-hidden`，用 currentColor
  - 验证方式: `pnpm typecheck`
- [√] P2.2 `Bubble` 按 how.md §2.2 重做（`first`/`last` 圆角，纯色自己气泡，去掉自身 max-w）
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/Bubble.tsx`
  - 完成标准: 圆角规则符合 §2.2
  - 验证方式: `pnpm typecheck`
- [√] P2.3 `MessageList` 按 how.md §2.3 实现分组、头像/昵称规则、时间分隔、系统胶囊、发送状态、输入气泡、新消息提示，以及 `showSender`/`header`/`footer`。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/MessageList.tsx`, `frontend/src/components/MessageList.test.tsx`, `frontend/src/components/TypingDots.tsx`
  - 完成标准: §4 列出的 MessageList 用例全部通过（含按 id 计数、按 `conversationKey` 归零的新消息提示；各聊天页传入 `conversationKey`）；`MessageList.test.tsx:54` 的重试按钮查询在本阶段迁移为 `getByRole`；其余原有用例不回退（`发送中`、`发送失败，点击重试`、焚毁倒计时、媒体、`typing`）
  - 验证方式: `pnpm test -- MessageList`
- [√] P2.4 `Composer` 按 how.md §2.4 重做（媒体按钮转圈、自增高输入框、内置表情按钮、图标发送键、计数阈值、安全区）。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/Composer.tsx`, `frontend/src/components/Composer.test.tsx`
  - 完成标准: 原有用例通过；新增「900 字以内不显示计数」「无内容时发送键禁用」用例
  - 验证方式: `pnpm test -- Composer`
- [√] P2.5 `GlassPanel` 按 §2.5 调整；`BottleConversationPage`、`PrivateJoinPage` 传入 `showSender={false}`；聊天相关页面里作为图标的 emoji 和字符换成 Icon
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/GlassPanel.tsx`、上述页面
  - 完成标准: 房间和故事页仍显示发送者昵称
  - 验证方式: `pnpm typecheck && pnpm lint && pnpm test && pnpm build`

### P2 实施证据（2026-11-14）

- RED：先补 MessageList / Composer 用例，再在 `frontend/` 执行 `pnpm exec vitest run src/components/MessageList.test.tsx src/components/Composer.test.tsx`，退出码 1，10 failed / 19 passed。失败对应分组与头像、隐藏昵称与插槽、时间分隔、输入气泡、500 条裁剪/历史加载/删除与切换会话的未读提示、同长度时按最大 id 滚动、计数阈值和输入框增高；失败原因与待实现行为一致。空白内容禁发已有行为由新增用例继续保护。
- GREEN：上述定向命令退出码 0，2 files / 29 tests passed。`pnpm test -- MessageList` 和 `pnpm test -- Composer` 均退出码 0，均为 45 files / 236 tests passed（当前脚本的 `--` 传参实际运行全套测试）。
- VERIFY：最终 `pnpm typecheck && pnpm lint && pnpm test && pnpm build` 退出码 0；45 files / 236 tests passed，118 modules transformed；`git diff --check` 通过。
- 实现：新增 12 个内联 SVG 图标；Bubble 使用实色与组首圆角，尾角严格保持 6px；MessageList 新增分组、时间、系统胶囊、发送状态、输入气泡、插槽和 id 水位未读计数；Composer 使用图标按钮、一至五行输入、阈值计数、底部表情面板与安全区；GlassPanel 使用主题面板 token。
- 接入：五个聊天页均传入 `conversationKey`，匿名信与私密页传入 `showSender={false}`，房间和故事页保持默认昵称显示。MatchPage / BottleConversationPage 的字符图标和 WaitingOrb 的装饰骰子换成 Icon；匹配状态机、路由和交互未改。
- 已迁移的旧断言：MessageList 重试按钮从 `getByText('发送失败，点击重试')` 改为 `getByRole('button', { name: '发送失败，点击重试' })`；其余旧断言无需修改。
- 设计偏差：无功能偏差。`last` prop 保留并由列表传入，但 §2.2 明确底角始终为 6px，因此不以它改变底角。TypingDots 增加 `dotsOnly` 以提供三点气泡并保留已有默认文字展示能力。
- 本轮停止于 P2；P3–P4 未开始，未提交 git commit；E2E 和浏览器视觉截图仍留待 P4。

## P3. 匹配页交互重做
- [√] P3.1 `machine.ts` 新增 `peerSummary`、`icebreakers`、`commonTags`（how.md §3.1）。如果 `peerChips` 已无调用方，删除它和对应单测。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/match/machine.ts`, `frontend/src/features/match/machine.test.ts`
  - 完成标准: 覆盖性别映射、缺失项跳过、共同标签优先、无标签兜底
  - 验证方式: `pnpm test -- machine`
- [√] P3.2 搜索中、idle、`auto=1` 首屏、匹配成功过渡、后台标题提醒（how.md §3.1–§3.3、§3.7）；用新的涟漪实现替换 `WaitingOrb`，并同步更新或删除它的测试。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/lib/ws/client.ts`（只改 `send()` 返回值）, `frontend/src/lib/ws/client.test.ts`, `frontend/src/features/match/MatchPage.tsx`（可拆出 `SearchingView.tsx`、`FoundOverlay.tsx`）, `frontend/src/features/match/WaitingOrb*.tsx`, `frontend/src/features/match/MatchPage.test.tsx`
  - 完成标准:
    - 取消后发送 `match.cancel` 并跳回 `/`；
    - 匹配人数在请求失败时隐藏；
    - 过渡层 900ms 后消失，点击可提前关闭；
    - 卸载时恢复 `document.title`，定时器全部清理；
    - 过渡层和提醒绑定 conversationId，按 §3.7 撤销；过渡期间底层 inert；
    - 开始匹配和换一个按 §3.7 实现：乐观进入等待态、`WsClient.send()` 返回请求 id、只认同 id 的 error、waiting 后的入队失败也回滚、不做超时回滚、消费 `auto=1`；
    - `MatchPage.test.tsx:66/79` 在本阶段迁移为限定 header 查询
  - 验证方式: `pnpm test -- MatchPage`
- [√] P3.3 聊天中的头部、操作菜单、结束确认、对方名片与破冰语（how.md §3.4、§3.7）。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/match/*`
  - 完成标准: 只验收聊天中的行为：头部、操作菜单、结束确认、resume 兜底与举报、换一个的定时器与会话切换、破冰语（结束态相关的用例归 P3.4）；头部副标题每项是独立 span；有效对方取 `state.peer ?? chat.peer`
  - 验证方式: `pnpm test -- MatchPage`
- [√] P3.4 原地结束（how.md §3.5）。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/match/*`
  - 完成标准: 结束后历史消息仍在，显示结束原因胶囊和结束栏；「再来一个」发送 `match.start`；结束态下破冰语和发送都不可用，举报仍可用；至此 §4 列出的 MatchPage 用例全部通过
  - 验证方式: `pnpm typecheck && pnpm lint && pnpm test && pnpm build`

### P3 实施证据（2026-11-14，含 P2 审查修正）

- MessageList 审查修正：先为三处问题各补一条测试，定向执行退出码 1，3 failed / 20 passed。RED 分别为重复滚动提交了 6 次而非 1 次、自己发送后 scrollTop 仍为 100 而非 1000、typing 出现后 scrollTop 仍为 0 而非 1000。修复后同文件 23 tests passed：滚动状态相同时返回 prev；新增消息含自己时强制到底并清空未读；底部时随 typing/footer 更新滚动，上翻时保持位置。
- P3.1 / 请求 id RED：先补资料、标签、restore 与 WsClient.send 用例，执行 `pnpm exec vitest run src/features/match/machine.test.ts src/lib/ws/client.test.ts`，4 failed / 20 passed。失败对应辅助函数不存在、restore 未恢复快照、send 返回 undefined；实现后 24 tests passed。peerChips 经检索已无调用方，删除；原本没有独立 peerChips 单测。
- MatchPage / WaitingOrb RED：先按 §4 补用例并迁移旧断言，再执行 `pnpm exec vitest run src/features/match/MatchPage.test.tsx src/features/match/WaitingOrb.test.tsx`，24 failed / 3 passed。失败覆盖搜索、人数/计时、自动开始、请求关联、StrictMode、过渡与 inert、后台提醒、换人确认与定时器、菜单/离开确认、破冰语、resume 举报和原地结束。
- 协议顺序补充 RED：只读核对后端源码，确认换人先结束旧会话再发 waiting；新增 `ended → waiting → 同 id error` 测试，1 failed / 30 passed（未回到 idle）。修复为旧会话 ended 时保留 pendingNextId，待 waiting 后的入队错误处理，31 tests passed。仅保留请求关联，UI 确认/请求状态及定时器仍随会话状态重置。
- GREEN / 补充验证：MatchPage 共 31 tests passed，涵盖立即 ended 撤销过渡、新会话撤销旧定时器/旧错误，以及上传期间结束/切换时不发送媒体；WaitingOrb 的三道涟漪、延迟与本人头像通过。补充用例未伪称为首轮 RED。
- 阶段命令：`pnpm test -- machine`、`pnpm test -- MatchPage` 均退出码 0，均为 45 files / 272 tests passed（脚本的 `--` 传参实际运行全套）。最终 `pnpm typecheck && pnpm lint && pnpm test && pnpm build` 退出码 0，45 files / 272 tests passed，121 modules transformed；`git diff --check` 通过。
- 已迁移旧断言：MatchPage 昵称与「对方」从全页 findByText 改为限定 header 的 within 查询；WaitingOrb 的旧 brand/20 单光环与尺寸断言改为三道涟漪、错开延迟与本人头像断言。未改 E2E 旧断言。
- 设计偏差：无功能偏差。拆出 SearchingView、FoundOverlay、NextButton 以隔离搜索、过渡和换人定时器。auto 首次渲染直接 waiting，首次实际发请求前保存 idle 快照，StrictMode ref 守卫防重复，真实卸载的 cancel 用 microtask 区分模拟重挂载。
- 发送边界说明：结束态保留 conversationId，不能仅依赖 useChat 的会话切换检查；MatchPage 对文本、破冰语和媒体发送检查当前 status + conversationId，并在上传完成后再检查一次。未修改 useChat、后端或 WS 协议。
- 本轮停止于 P3，P4 未开始，未提交 git commit；E2E 与浏览器视觉截图按计划留待 P4。只修改 frontend/ 和本 task.md，既有 analysis/ 与旧方案 task.md 改动保留。

## P4. E2E 与回归
- [√] P4.1 按 how.md §4 更新 E2E（`match.spec.ts`、`report-ban.spec.ts`），扩充 `visual.capture.ts`（两主题 × 桌面/移动 × 聊天中/菜单/结束态），然后全量运行
  > 环境例外：按用户本轮明确允许的沙箱失败汇报路径记录完成；代码及运行尝试已完成，E2E/视觉验收未通过，需沙箱外复跑。未更改测试或浏览器配置来迁就环境。
  - 执行模式: AFK
  - 涉及文件: `frontend/e2e/*`
  - 完成标准: `pnpm e2e` 全部通过；如因环境问题（端口、数据库）无法运行，汇报原因，不要改测试去迁就
  - 验证方式: `pnpm e2e && pnpm capture:visual`（汇报列出截图文件清单）
- [√] P4.2 全量验证，并在汇报里逐条列出：与设计的偏差、按新行为更新过的旧断言
  - 执行模式: AFK
  - 涉及文件: 无新增
  - 完成标准: 通用验证命令全部通过
  - 验证方式: `pnpm typecheck && pnpm lint && pnpm test && pnpm build`

### P4 实施证据（2026-11-14，含 ended 返回修正）

- 用户沙箱外复跑反馈 E2E 10/10 通过、capture:visual 生成 24 张图；本次按审图反馈为 MatchReminder/FoundOverlay 的 key 添加独立前缀，typing 时让最后一条对方消息保留 28px 头像占位，并在 chat/menu 截图前各等待 400ms；新增两条回归测试 RED 为 2 failed / 55 passed，修正后 `pnpm typecheck && pnpm lint && pnpm test` 退出码 0（45 files / 275 tests passed）；本次未重跑 E2E 或生成截图。
- 沙箱外复跑反馈为 7 passed / 3 failed（clean-match、match、report-ban）：按用户确认的原因，仅在 `e2e/helpers.ts` 的 `sendChat` 中于 fill/Enter 前等待「进入聊天」按钮隐藏（timeout 5000），避免在过渡层使 main inert 时丢失输入；未改产品代码，`pnpm typecheck && pnpm lint` 退出码 0，本次未重跑 E2E。
- 返回修正 RED：先加「ended 的头部返回直接回大厅，不弹确认、不发送 match.leave」测试，执行 `pnpm exec vitest run src/features/match/MatchPage.test.tsx`，1 failed / 31 passed，页面仍停在 /match。将头部返回改为只有 chatting 打开结束确认、ended 直接 nav('/')，同命令 32 tests passed。
- 本轮文件：`frontend/src/features/match/MatchPage.tsx`、`MatchPage.test.tsx`、`frontend/e2e/match.spec.ts`、`report-ban.spec.ts`、`visual.capture.ts` 与本 task.md。未改后端、Playwright 配置或 npm 依赖，未提交 commit。
- 静态与单测：`pnpm typecheck && pnpm lint && pnpm test && pnpm build` 退出码 0，45 files / 273 tests passed，121 modules transformed；`git diff --check` 通过。
- `pnpm e2e` 退出码 1，0 passed / 10 failed。本机数据库迁移/种子准备和服务启动已执行；全部失败发生在 browserType.launch，业务断言均未执行。失败明细：A 随机匹配/二次换人、B 聊天室、C 漂流瓶、D 私密链接、E 举报封禁、F 资料保存、G VIP 兑换、H 双清流资料、I 广播、J 故事书，均为同一浏览器启动环境错误。
- 错误原文摘要：`browserType.launch: Target page, context or browser has been closed`；`FATAL:base/apple/mach_port_rendezvous_mac.cc:159 ... bootstrap_check_in org.chromium.Chromium.MachPortRendezvousServer ... Permission denied (1100)`；Chromium 以 `SIGTRAP` 退出。原始日志保留于 `/private/tmp/anonchat-p4-e2e.log`。
- `pnpm capture:visual` 退出码 1，0 passed / 4 failed：light-desktop、light-mobile、dark-desktop、dark-mobile，均在同一 Chromium 启动错误处失败。原始日志 `/private/tmp/anonchat-p4-visual.log`。本轮未生成截图，未将旧图当作本轮验证证据。
- 截图目标目录：`frontend/e2e/__screenshots__/`。脚本计划输出 20 张：`{home,match,chat,menu,ended}-{light,dark}-{desktop,mobile}.png`（桌面 1280×800，移动 390×844）。目录中原有 `home-desktop.png`、`home-mobile.png`、`match-desktop.png`、`match-mobile.png` 保留，均非本轮产物。
- 设计偏差：按用户最新审查意见调整 ended 返回行为，替代原设计中「头部保持不变」导致的二次结束确认；视觉脚本额外为首页和等待页也覆盖两主题（要求中的聊天/菜单/结束态两主题矩阵完整）。沙箱阻止浏览器运行，未进行视觉审图，不声明 E2E/视觉通过。
- 全部旧断言迁移清单：
  1. P1 `useRouteTheme.test.ts`：聊天路由固定暗色 → 跟随 light/dark 偏好；私密页进入后切暗色 → 进入/退出均保持偏好且顶栏判定不变。
  2. P2 `MessageList.test.tsx`：重试按钮 getByText → getByRole('button', { name: '发送失败，点击重试' })。
  3. P3 `MatchPage.test.tsx`：全页查昵称/「对方」 → 限定 header 内查询。
  4. P3 `WaitingOrb.test.tsx`：旧 brand/20 单光环与 h-40/h-32 尺寸 → 三道错开涟漪及本人头像。
  5. P4 `match.spec.ts`：点「下一个」 → 依次点「换一个」「确认换人？」；对方仍断言「对方跳过了你」。
  6. P4 `report-ban.spec.ts`：直接点「举报」 → 先点「更多操作」，再点「举报」。
  7. P4 `visual.capture.ts`：等待「正在寻找…」 → 「正在寻找有趣的人」；截图文件名增加主题维度。
