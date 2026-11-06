# 任务清单: 界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书

目录: `helloagents/main/plan/202611062140_ui-revamp-profile-vip-story/`

执行顺序：U1 → U2 → U3 → U4 → U5 → U6 → U7。按用户要求（2026-11-06），U1–U6 和 U7.1–U7.3 由实施者一次连续完成，每个阶段结束时自行运行验证；审查者在全部完成后统一复核。U7.4 部署需要用户确认后执行。
通用验证命令沿用 V1：`make test`、`make test-integration`、`make web-test`、`make e2e`，再加 `cd frontend && pnpm build`。

---

## U1. 设计系统与界面改版（只改前端，不改后端和协议）

- [√] U1.1 设计 token、字体与动效
  - 涉及文件：`frontend/src/index.css`、`frontend/src/main.tsx`、`frontend/package.json`
  - 完成标准：
    - how.md 1.1 中的 token 全部写入 `@theme`；
    - 安装 `@fontsource-variable/inter@5.3.0` 并引入；
    - 1.2 中的 `fade-up`、`breathe` 动画和 hover 约定已定义；
    - 在 `prefers-reduced-motion` 下关闭动画。
- [√] U1.2 主题与背景过渡
  - 涉及文件：`frontend/src/app/useRouteTheme.ts`（新建）、`App.tsx`
  - 完成标准：
    - 路由与主题的对应关系符合 1.3；
    - 根节点有浅、暗两层背景，暗色层的 opacity 在 300ms 内过渡；
    - 大厅的明暗选择保存在 localStorage，不影响聊天页。
  - TDD：先为 `useRouteTheme` 的判定函数写单元测试（RED）。
- [√] U1.3 公共组件
  - 涉及文件：`frontend/src/components/`
  - 完成标准：
    - 实现 1.4 中的 GradientAvatar（保留 `Avatar` 导出名）、Card、GlassPanel、Bubble、TypingDots、Toggle、Segmented、RangeSlider、TagPicker、Sheet、Marquee、Drawer、VipLock；
    - 实现 1.5 中的 `codename()`（`src/lib/codename.ts`）；
    - 渐变只在品牌紫和辅助粉之间取值。
  - TDD：codename 的稳定性和区分度、Marquee 是否需要滚动的判定，先写测试。
- [√] U1.4 应用壳与顶栏
  - 涉及文件：`frontend/src/app/TopBar.tsx`、`App.tsx`
  - 完成标准：
    - 顶栏按 2.1 布局：☰ 打开抽屉，中间是品牌名，右侧是 👤+（跳转 `/p/new`）、📱（添加到主屏幕的 Sheet，提供 beforeinstallprompt 和 iOS/Android 说明）、☀/🌙；
    - 抽屉列出 2.1 的全部条目：尚未实现的路由（`/vip`、`/stories`）先显示，点击后 toast"即将开放"，由 U4/U6 接通；
    - 公告跑马灯先预留空槽，U4 再接数据；
    - 保留 `data-testid="ws-connected"` 和"换个身份"的二次确认逻辑。
- [√] U1.5 页面改版：现有页面全部换上新风格
  - 涉及文件：`frontend/src/features/**`、`components/MessageList.tsx`、`Composer.tsx`、`ReportDialog.tsx`、`BlockConfirm.tsx`、`Toast.tsx`、`KickedOverlay.tsx`、`VerificationModal.tsx`
  - 完成标准：
    - 首页先用 Bento 布局重排现有入口：随机匹配大卡、房间 2×2（数据取 `/api/rooms` 的前 4 个，emoji 暂时按 slug 在前端映射）、匿名信、私密链接。资料卡、在线人数、匿名动态、广播卡的位置先用骨架占位，U4 再接通；
    - 聊天类页面（match、room、bottle 会话、private 会话）改用 2.3 的暗色玻璃布局，气泡使用 Bubble，正在输入使用 TypingDots；
    - match、bottle、private 会话中显示匿名代号，不显示昵称，房间聊天仍显示昵称；
    - 匹配等待态按 2.3 设计；
    - 漂流瓶页面的标题改为"匿名信"；
    - 移动端单列；
    - admin 页面只换成浅色 token，不改功能。
- [√] U1.6 验证
  - 完成标准：现有的单元测试和 5 个 E2E 全部通过（只有在文案或结构确实改变时才调整断言，不得删除用例）；typecheck、lint、build、`pnpm audit` 都通过。用 Playwright 对首页和匹配页在桌面（1280×800）和移动端（390×844）各截一张图，保存到 `frontend/e2e/__screenshots__/`（不入库，加入 .gitignore），供审查者核对。

## U2. 后端：资料、匹配偏好、统计（TDD）

- [√] U2.1 迁移 `00004_profile_vip_story.sql`：按 how.md 3.1 一次性建好全部表和字段（包括 U3、U5 要用的），Down 迁移可以完整回滚；更新 sqlc 查询并重新生成代码；种子房间更新 emoji。
- [√] U2.2 `internal/profile`：常量放在 consts.json 中，用 go:embed 读取；校验、更新、随机昵称、自定义昵称、PublicProfile；匿名编号生成（Crockford Base32，冲突重试），新建身份时写入。
- [√] U2.3 接口：扩展 `GET /api/identity/me`；新增 `PATCH /api/identity/profile`、`POST /api/identity/nickname/shuffle`、`PUT /api/identity/nickname`，限流按 3.5。`vip_required` 的判定先调用一个 `vip.IsVIP` 的最小实现（只看 vip_until > now）。
- [√] U2.4 匹配：按 3.4 的兼容判定（双向）和标签打分修改；`match.found` 和 `match.waiting` 的负载按 3.4 调整；1v1 会话（match/bottle/private）消息负载里的 nickname 置空（REST 历史接口同样处理）。
  - TDD：兼容判定的组合表驱动测试，打分测试，集成测试覆盖两个 VIP 互相筛选、清流隔离。
- [√] U2.5 `GET /api/stats/online`（10 秒缓存）；`GET /api/rooms` 返回 emoji。
- [√] U2.6 换身份时继承 VIP 和广播卡，资料重置，生成新的匿名编号（集成测试覆盖）。
- [√] U2.7 扩展防泄漏测试，覆盖 me、profile、match.found、stats；补充错误码 `vip_required`、`not_found`。
- [√] U2.8 验证：race 测试、集成测试、vet、staticcheck、govulncheck 都通过。

## U3. 后端：VIP 激活码、公告、广播、动态（TDD）

- [√] U3.1 `internal/vip`：激活码生成（80 bit，格式 `XXXX-XXXX-XXXX-XXXX`）、规范化、哈希、兑换（事务 + FOR UPDATE + 并发安全），限流按 3.5，失败统一返回 `invalid_code`。
- [√] U3.2 `POST /api/vip/redeem`；管理后台激活码批次的生成、列表、撤销（明文只返回一次）；写入 admin_audit_logs。
- [√] U3.3 `internal/feed`：公告（管理后台 CRUD + 用户侧列表）、广播（提交时扣卡；审核通过、驳回退卡、下架；当前广播；我的广播；举报）、`GET /api/feed` 聚合（先聚合公告和广播，作品在 U5 接入）。
- [√] U3.4 留存任务扩展：清理广播（规则见 3.1）。
- [√] U3.5 管理后台：按匿名编号查身份。
- [√] U3.6 防泄漏测试覆盖 U3 的全部用户侧接口；补充错误码 `invalid_code`、`no_broadcast_card`。
- [√] U3.7 e2eseed 扩展：创建测试管理员和一个可用激活码，写到 e2e 能读取的位置（沿用现有 e2eseed 的输出方式）。
- [√] U3.8 验证：同 U2.8。

## U4. 前端：大厅接通资料、VIP、公告广播、动态

- [√] U4.1 资料卡：按 2.2 接通 me、profile、nickname 接口；防抖 500ms 后保存，失败回滚；折叠状态保存在 localStorage；复制匿名编号；分享；换身份。
- [√] U4.2 匹配偏好与 VipLock；"匹配"按钮先保存偏好，再进入 `/match?auto=1`；匹配页展示对方资料芯片和生效的条件，30 秒后给出提示。
- [√] U4.3 `/vip` 页面：VIP 状态、权益说明、兑换、剩余广播卡、发布广播、我的广播。
- [√] U4.4 首页接通：公告跑马灯、广播卡（无广播时显示占位文案并链接到 /vip）、在线人数（30 秒轮询）、匿名动态、房间 emoji 改为读后端字段。
- [√] U4.5 管理后台：公告、广播审核、激活码批次（生成后展示明文并支持 CSV 下载；提示"只显示一次"）、按匿名编号查身份。
- [√] U4.6 PWA：`public/manifest.webmanifest`、图标（SVG/PNG，使用品牌渐变和抽象图形）、`<link rel=manifest>`、theme-color。不做 Service Worker（Web Push 属于 V2）。
- [√] U4.7 errorMessages 补充新错误码的中文文案；补单元测试。
- [√] U4.8 E2E：资料保存后刷新仍保留；兑换后 VipLock 消失；双清流配对，看到匿名代号和对方资料芯片；广播从提交到审核通过再到首页可见。原有用例全部通过。

## U5. 后端：故事书（TDD）

- [√] U5.1 `internal/story`：发起、列表、详情、认领（并发认领同一个故事只能成功一次）、取消、结束判定（`STORY_MIN_DURATION` 配置项，默认 5 分钟，`APP_ENV=test` 时允许调短；最少消息数 5）、同意与生成作品（lines 不含身份）、等待超时清理。
- [√] U5.2 WS：`story.started`、`story.ended`、`story.consent`；故事会话消息的 nickname 填角色名。
- [√] U5.3 作品：列表、详情（不同状态的可见性规则）、点赞（幂等）、删除、撤回、举报；feed 接入作品。
- [√] U5.4 管理后台：作品审核、驳回、下架。
- [√] U5.5 留存规则（3.1）：故事和会话的清理不得删除作品。
- [√] U5.6 防泄漏测试覆盖故事和作品的全部接口与事件；补充错误码 `story_taken`、`story_not_valid`。
- [√] U5.7 验证：同 U2.8。

## U6. 前端：故事书

- [√] U6.1 `/stories`（"等待开演"与"书架"两个 Tab）、`/stories/new`、认领流程。
- [√] U6.2 `/stories/:storyId/play` 剧场：暗色玻璃，显示角色名，结束按钮，收录同意面板（实时显示双方的同意状态）。
- [√] U6.3 `/works/:workId` 阅读页：对话体排版、点赞、举报、删除、撤回。
- [√] U6.4 首页的故事书浮动入口和快捷入口、抽屉条目接通；匿名动态中的作品条目可以点击进入。
- [√] U6.5 管理后台：作品审核。
- [√] U6.6 E2E：故事书完整流程（两个上下文 + 管理员审核）。全部 E2E 通过。

## U7. 收尾

- [√] U7.1 知识库同步：`helloagents/main/wiki/{protocol,data,overview,glossary,deploy}.md`、`project.md`。
- [√] U7.2 全量验证：后端（race、集成、vet、staticcheck、govulncheck）、前端（测试、typecheck、lint、build、audit）、E2E（`--repeat-each=2`）。
- [√] U7.3 视觉检查清单：审查者对照截图人工核对 how.md 第 4 节的视觉清单。
- [ ] U7.4 部署到 cch_hk：先备份数据库，再构建、执行迁移、完成冒烟测试。需要用户确认后执行。
- [ ] U7.5 HITL：用户协议中的 VIP 与 UGC 条款、激活码分发渠道、作品与广播的审核人员安排。

---

## 执行记录

- 2026-11-06 pi（deepseek-flash）一次连续完成 U1–U6、U7.1–U7.3。审查者复核：后端 vet、staticcheck、race、集成测试通过；前端测试、typecheck、lint、build、audit 通过；E2E 10/10 通过；迁移在空库上 up→down→up 通过；截图对照视觉规范通过。
- 第 1 轮审查修正：
  - R1：新增的公开 UGC 改为保存打码后的文本；
  - R2：故事角色名过违禁词，标签限定在预设列表内；
  - R3：发广播、发起故事改挂 chatProtected；
  - R4：认领故事时检查双向拉黑；
  - V1–V3：在线人数卡改为紧凑，浮动胶囊只在 ≥1440px 时显示，等待圆加上呼吸光环。
  - 审查者复跑：全量测试、TestTypingBroadcastToPeer ×5、E2E 10/10，均通过。
- 待办：U7.4 部署（注意：`migrate down` 会回滚到空库，回退只能依赖部署前的数据库备份）；U7.5 HITL。
