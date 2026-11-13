# 任务清单: 外站匹配接入（chat2019 / 晚风）与用户媒体消息

目录: `helloagents/main/plan/202611131925_external-match-bridge/`

## 给实施者（Codex）的执行约定

- 实施者：Codex（`gpt-6.1-sol`，reasoning medium）。审查者：Claude，每个阶段结束后复核。
- **设计以 `how.md` 为准**。`how.md` 没写到的地方，沿用现有代码的写法，不要自行扩大范围。确实需要偏离时，先在汇报里说明原因。
- 开始前通读 `why.md`、`how.md`（尤其是 §0 硬性约束），再读相关的现有代码：`backend/internal/match/`、`backend/internal/conversation/pipeline.go`、`backend/internal/media/`、`backend/internal/bot/engine.go`（`PickBot` / `OnMessage` 的接法）、`backend/internal/bot/settings.go`（单行设置表的写法）。
- 参考源码 `/Users/zcw/project/liao` **只读**，不要修改它。
- 每个阶段完成后运行该阶段列出的验证命令。全部通过后，把对应条目的 `[ ]` 改为 `[√]`，然后**停下来汇报**：改了哪些文件、验证命令的输出摘要、与设计的偏差及原因。
- 标注 TDD 的任务：先写失败的测试（RED），再写实现（GREEN），最后整理（REFACTOR）。汇报时附上 RED 阶段的失败输出摘要。
- 不要 git commit；不要回滚或改动与本任务无关的文件（工作区里已有的 `analysis/` 和其他方案包的改动不要动）。
- 只有在需要确认数据库时，才连接本机开发库（`127.0.0.1:55432`）。不要连接任何远程或生产环境；**不要访问真实的 chat2019 / 晚风站点**（P7 的探测工具只编写、只用 httptest 测试，不实际运行）。

通用验证命令（在项目根目录执行）：

- `make sqlc`
- `make test`
- `make test-integration`（本机 PostgreSQL：127.0.0.1:55432）
- `make lint`（`go vet` + `staticcheck` + 前端 lint）
- **不要执行 `make migrate-down`**：它会回滚全部迁移、清空开发库。迁移回滚只在集成测试创建的临时数据库里验证
- `make web-test`
- `cd frontend && pnpm build`

---

## 0. 方案边界确认
- [√] 0.1 核对 why.md 的范围内和范围外与 how.md 的设计边界一致；确认 how.md ADR-2（新增 gorilla/websocket、google/uuid）的状态
  - 执行模式: AFK
  - 涉及文件: `why.md`, `how.md`
  - 完成标准: 汇报中写明范围一致；ADR-2 的状态与 how.md 一致（已采纳）
  - 验证方式: 只读核对

## P1. 数据层、配置与外站设置
- [√] P1.1 迁移 `00010_external_bridge.sql`（how.md §1）；新建 `queries/external.sql`；修改 `queries/media.sql`（`InsertMediaItem` 新参数、`CanIdentityAccessMedia`、`ListOrphanNonLibraryMedia`、`GetUserMediaForSend` 所需查询，见 §6.2）；新增 `GetMessageForRelay`；`make sqlc`；`testutil.Truncate` 加入新表；修正所有调用点，使其编译通过（媒体库调用传 `library` / NULL）
  - 执行模式: AFK
  - 涉及文件: `backend/migrations/00010_external_bridge.sql`, `backend/queries/external.sql`, `backend/queries/media.sql`, `backend/queries/conversation.sql`, `backend/internal/store/*`（生成）, `backend/internal/testutil/testutil.go`, `backend/internal/media/service.go`（仅调用点）
  - 完成标准: 所有返回 `media_items` 行的查询都返回 `store.MediaItem`（how.md §6.2，汇报列出改过的查询）；新增迁移集成测试（how.md「测试与部署」：在临时独立数据库里 00009 → 00010 → 单步 down → 00010，媒体库数据保留）并通过；`go build ./...` 和现有测试通过。**禁止对开发库或共享测试库执行 `make migrate-down`**（它会回滚全部迁移）
  - 验证方式: `make sqlc && cd backend && go build ./... && cd .. && make test && make migrate && make test-integration`
- [√] P1.2 环境变量（how.md §2.1）的读取与校验。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/config/config.go`, `backend/internal/config/config_test.go`
  - 完成标准: 新变量有默认值；`EXTERNAL_WANFENG_HTTP_PROXY` 不是合法的 http/https/socks5 URL 时报错；`USER_MEDIA_MAX_UPLOAD_MB`、`EXTERNAL_TIMEOUT_SECONDS`、`EXTERNAL_WANFENG_NEW_IDENTITY_PER_HOUR` 必须为正数；`EXTERNAL_WANFENG_PROTOCOL_OVERRIDES` 不是合法 JSON 时报错
  - 验证方式: `cd backend && go test ./internal/config/...`
- [√] P1.3 `internal/external/settings.go`：外站设置的单行表缓存、`Plan()`、后台 `GET/PUT /api/admin/external/settings`（校验范围、审计日志 `external.settings.update`）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/settings.go`, `backend/internal/external/settings_handler.go`, `backend/internal/external/*_test.go`, `backend/internal/api/routes.go`
  - 完成标准: 首次读取时插入默认行；越界返回 400；PUT 后 `Plan()` 立即返回新值；未登录的管理员请求返回 401
  - 验证方式: `make test && make test-integration`（新增 `settings_integration_test.go`）

### 本轮实施记录（仅 0.1、P1.1、P1.2、P1.3）

- 0.1：范围一致；ADR-2 状态为「已采纳」。本轮不新增晚风依赖。
- P1.1：新增四张外站表和媒体来源/归属字段；补齐外站、媒体与消息转发查询、生成 store、清表与媒体库调用点。完整媒体行查询为 `ListMediaItemsByFolder`、`GetMediaItem`、`InsertMediaItem`、`UpdateMediaItem`、`PickMediaForBot`、`GetUserMediaForSend`，均返回 `store.MediaItem`。新增迁移往返测试与媒体查询权限/焚毁/转发字段集成测试。
- P1.2 RED：`go test ./internal/config/... -run TestExternalConfig -count=1` 编译成功后失败：缺少外站配置；非法代理、非正数、非法 JSON 均返回 nil。GREEN：`go test ./internal/config/...` 通过。
- P1.3 RED：`TEST_DATABASE_URL=本机测试库 go test -tags integration ./internal/external/... -run TestExternalSettingsAPI -count=1` 编译成功后失败：`status=404 body=404 page not found`。补充取消回归 RED：`-run TestExternalSettingsSaveSurvivesRequestCancellation -count=1` 返回 `context canceled`。实现后默认行、范围上下界、401、保存后 Plan/通知立即更新、重建缓存持久化、updated_by、审计及并发保存/读取均通过。
- 实现期失败（不计作 RED）：首次 build 因 sqlc 将 `EXISTS OR EXISTS` 推导为 `*bool` 失败，改用等价的外层 EXISTS 保持原 bool 契约；首次 API 测试编译因把 `admin.CreateUser` 返回的 AdminUser 当 UUID 使用失败，改为 `.ID` 后获得上述真实 RED。
- 设计细节调整：迁移测试使用本包 Provider 的 `UpTo(9)` / `UpTo(10)` 与 `Down(all=false)`，固定测试版本，避免未来新增 00011 后误测回滚目标；不改生产迁移入口。设置写入与审计采用脱离 HTTP 取消、各限时 5 秒的上下文。缓存 `Plan()` 返回 Settings；后续 P2/P4 再由 Bridge 转换成 match.ExternalPlan，本轮不提前实施匹配器。
- Claude 阶段复核：退出码 0，is_error=false，session_id=`eef21eb2-4899-4965-9da4-e50adc121dd9`；无 P0/P1。迁移固定版本、HTTP 取消与并发验证建议已落实；同步回调不可重入的契约已注释。其余 P2：回滚会清空非媒体库消息引用/保留影子身份，留 P9 说明；潜在索引与后台非媒体库操作范围留后续按实际规模/产品边界评估；不为意外清空设置表补恢复，不提前扩展 P3 地址守卫或配置语义。
- 最终验证：`make sqlc` 通过；`cd backend && go build ./...` 通过；`cd backend && go test ./internal/config/...` 通过；`make test` 通过；`make migrate` 通过（开发库应用 00010）；`make test-integration` 通过（最后一轮退出码 0）；`make lint` 通过；`git diff --check` 通过。
- 补充验证（backend 下，TEST_DATABASE_URL 为 `postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable`）：`go test -tags integration ./internal/external/... -count=1`、`go test -tags integration ./internal/external/... -race -count=1`、`go test -tags integration ./internal/media/... -run TestUserMediaQueriesRespectOwnershipAndBurn -count=1`、`go test -tags integration -p 1 -count=1 ./internal/external/... ./internal/media/... ./migrations -race` 均通过；最后一条结果分别为 external 9.445s、media 6.407s、migrations 1.327s。
- 本轮不执行 P2 及之后任务；不生成 P10 的全方案 QA 文件，不归档、不提交。

## P2. 匹配状态机
- [√] P2.1 先写测试（RED）：how.md「测试与部署」中 `match` 列出的全部场景，使用假的 `ExternalMatcher` 和 `WithClock`。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/match/matcher_external_test.go`（新建）, `backend/internal/match/match_external_integration_test.go`（新建，`ExternalFound` 建会话、订阅和 `match.found` 部分）
  - 完成标准: 新测试编译通过并失败，失败原因是功能未实现（不是编译错误）
  - 验证方式: `cd backend && go test ./internal/match/... -run External`（汇报 RED 输出）
- [√] P2.2 实现 how.md §3：`matchPhase`、代次 `attempt`、`ExternalMatcher` / `ExternalPlan` / `ExternalRequest` / `ExternalPeer` / `ExternalHooks` / 错误值、`SetExternalMatcher`、`tryExternal`（含热关闭）、`cmdExternalFailed`、`cmdClaimAttempt`、`cmdRequeueExternal` 与意图版本 `intents`、`ExternalFound`（认领 → 事务 → `Activate(convID, publish)`，publish 在 Bridge 的 link 锁内执行；拒绝激活时结束会话）、移出条目时取消外站、`tryFallback` 的两种模式，依赖 P2.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/match/matcher.go`, `backend/internal/match/service.go`
  - 完成标准: P2.1 的测试全部通过；现有 `match` 测试（含 `matcher_fallback_test.go`）**不改**也全部通过；匹配 goroutine 内没有数据库、网络调用，也没有调用 `do()`
  - 验证方式: `cd backend && go test ./internal/match/... -race && cd .. && make test-integration`

### P2 实施记录（仅 P2.1、P2.2）

- 文件：新增 `backend/internal/match/matcher_external_test.go`、`match_external_integration_test.go`；修改 `matcher.go`、`service.go`。原有匹配测试（含 `matcher_fallback_test.go`）未修改。
- P2.1 RED：先写测试，再补设计要求的类型/字段声明与空实现以保证可编译；`cd backend && go test ./internal/match/... -run External -count=1` 编译成功后退出 1（8.601s），关键输出「外站匹配未开始」；同命令加 `-tags integration` 并设置本机 TEST_DATABASE_URL，编译成功后退出 1（11.523s），集成场景同样因外站匹配未开始失败。随后才实现状态推进和建会话行为。
- P2.2：实现三阶段与唯一 attempt；Start/Cancel 在独立 goroutine 执行；`cmdExternalFailed` 忽略过期结果；`cmdClaimAttempt` 校验队列、身份、代次、阶段、截止时间；`seq/inflight` 保护失败回队与清理，成功完成单独交还计数；原有 `cmdClaim` 语义不变。外站关闭/未注册保持原兜底时机，外站开启后从兜底阶段起点尝试，失败重试仍沿用原 after。
- ExternalFound：认领 → 事务建会话/成员及 OnCreated → 提交 → Activate 内 publish（订阅双方、仅给用户推送 found）。拒绝激活时以 peer_left 结束会话、不发布 found、按版本回队并返回 ErrActivationRejected；结束失败保留该错误标识并记录 Error。事务阶段按现有 onPair/onBotPair 写法限制为 5 秒。
- 补充 RED：`go test ./internal/match/... -run TestExternalClaimAfterLegacyRequeueRestoresIntent -count=1` 退出 1，复现「旧流程回队后的外站认领发生 panic: runtime error: invalid memory address or nil pointer dereference」；认领时恢复已被旧流程出队清理的意图记录后通过。`TEST_DATABASE_URL=本机测试库 go test -tags integration ./internal/match/... -run TestExternalFoundBoundsDatabaseContext -count=1` 退出 1，复现「ExternalFound 数据库阶段没有 5 秒超时」；补齐超时后通过。
- GREEN/验证：`go test ./internal/match/... -run External -count=1` 通过；`go test ./internal/match/... -race -count=1` 最终通过（2.000s）；`TEST_DATABASE_URL=本机测试库 go test -tags integration ./internal/match/... -run External -race -count=1` 最终通过（1.874s）。覆盖多轮在途、旧流程失败回队、同一时刻取消/重入、超时未 tick 的认领拒绝、提交/激活/发布顺序、公开资料、实际订阅、事务回滚和拒绝激活。
- 设计偏差：无。补充意图恢复、数据库超时及结束失败日志均为本阶段可靠性修正；未扩展既有匹配器退出机制，既有 do 在 Run 停止后的阻塞风险留给 P4 关闭顺序处理。拒绝激活时数据库不可用仍可能导致结束失败，当前返回组合错误并记录日志，保持设计要求的回队规则。
- 最终验证：`make test`、`make test-integration`、`make lint`（go vet、staticcheck、前端 eslint）、`git diff --check` 均通过；原有 `matcher_test.go`、`matcher_fallback_test.go`、`match_integration_test.go` 的 diff 为空。未执行迁移。
- Claude 阶段复核：首审发现的意图空指针与数据库阶段超时已修复并补回归；最终只读复核退出码 0、is_error=false，session_id=`7511762f-e92a-4328-8dde-e8e9ae09a178`，无 P0/P1。恢复首审会话曾因 `No conversation found with session ID` 退出 1，已改用新会话完成复核。非阻断建议：意图恢复测试可进一步贴近本地配对失败回队路径、补恢复后无取消的成功回队断言；既有外站条目被本地认领后失败回队可能等待原外站超时才兜底，本轮不扩大设计。拒绝激活后 End 失败及关闭时 do 阻塞限制见上条。
- 本轮不进入 P3，不修改依赖，不提交、不归档。

## P3. 外站客户端
- [√] P3.1 移植晚风包到 `backend/internal/external/wanfeng/`（how.md §4.1）；`go.mod` 新增两个依赖；编写 `PORTED_FROM.md`；实现 §4.1 列出的 4 项本地改动并各自补测试（见 how.md「测试与部署」中 `external/wanfeng` 一项）。TDD（本地改动部分）
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/wanfeng/*`, `backend/go.mod`, `backend/go.sum`
  - 完成标准: 包内单元测试（含原有的 `e2e_media_test.go`）全部通过且不访问网络（`live_smoke_test.go` 默认跳过）；`git diff backend/go.mod` 只新增 gorilla/websocket v1.5.3、google/uuid v1.6.0，以及 `golang.org/x/sync` 从 indirect 提升为直接依赖（版本不变）；其他 indirect 变化逐条在汇报中列出；`PORTED_FROM.md` 列出来源 commit 和全部本地改动；与 liao 原文件对比（`diff -r`）除 PORTED_FROM.md 列出的改动外没有差异；`make lint` 通过（必要时按目录在 staticcheck 配置中排除并写明原因）
  - 验证方式: 分三步分别执行、分别汇报：① `cd backend && go test ./internal/external/wanfeng/... -race`（必须通过）；② `diff -r /Users/zcw/project/liao/internal/upstream/wanfeng backend/internal/external/wanfeng`（预期有差异、返回非零，逐条核对差异都在 PORTED_FROM.md 中）；③ `make lint`（必须通过）
- [√] P3.2 地址守卫 `backend/internal/external/netguard.go`（how.md §4.3）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/netguard.go`, `backend/internal/external/netguard_test.go`
  - 完成标准: how.md「测试与部署」中 `external/netguard` 列出的场景都有测试并通过（`Clone()` 那一项在 P3.1 完成后补）
  - 验证方式: `cd backend && go test ./internal/external/ -run NetGuard -race`
- [√] P3.3 `backend/internal/external/chat2019/` 客户端（how.md §4.2），使用 P3.2 的守卫。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/chat2019/*.go`, `*_test.go`
  - 完成标准: how.md「测试与部署」中 `external/chat2019` 列出的场景都有测试并通过；sign 帧的 `userip` / `useraddree` 为空字符串、不含 `matchPreference`；非法 WS 地址、非相对媒体路径被拒绝
  - 验证方式: `cd backend && go test ./internal/external/chat2019/... -race`

### P3 实施记录（仅 P3.1、P3.2、P3.3）

- P3.1 文件：`backend/internal/external/wanfeng/` 全部 26 个来源 Go 文件；新增 `port_extensions_test.go`、`PORTED_FROM.md`、目录级 `staticcheck.conf`；修改 `backend/go.mod` / `go.sum`。来源 HEAD 为 `5fe3a8ebbd1590ec008dde462bce6c8673d6268f`，来源目录无工作区修改且本轮只读。22 个 Go 文件逐字节不变；仅 `ip_ban.go`、`client.go`、`adapter.go`、`httpx.go` 有生产差异，分别对应允许的封禁判断导出、连接/失败回调、显式文本/媒体命令、Clone 保留重定向检查。每项均有新增测试，原 e2e 和 live 测试原样保留。与 how.md 偏差：无。
- P3.1 RED：`cd backend && go test ./internal/external/wanfeng/... -run TestPort -count=1` 编译成功后退出 1（3.277s）：封禁识别返回 false；Text/MediaCommand 空 payload；Clone 丢失 CheckRedirect 且访问了重定向目标；连接就绪/停止、队列满、下发失败、媒体搬运失败未通知。先补新 API 的声明/空实现以取得可编译 RED，再实现行为。日志 `/tmp/anonchat-p3-wanfeng-red.log`。
- P3.1 规定三步分别执行：① `cd backend && go test ./internal/external/wanfeng/... -race` 通过（5.003s，含原 e2e_media）；② `diff -r /Users/zcw/project/liao/internal/upstream/wanfeng backend/internal/external/wanfeng` 预期退出 1，差异仅四项指定改动及三个新增文件，逐条对应 `PORTED_FROM.md`；③ `make lint` 通过。补充 `go test ./internal/external/wanfeng/... -run TestPort -race -count=1` 通过（1.650s）；`go test ./internal/external/wanfeng/... -run TestLiveHandshakeAndLogin -v -count=1` 明确 SKIP，未开启真实联调。
- 依赖：只新增 `github.com/gorilla/websocket v1.5.3`、`github.com/google/uuid v1.6.0`；原 `golang.org/x/sync v0.24.0` 转直接依赖，其他 indirect 无变化；go.sum 仅新增 gorilla 两条校验（uuid 校验已有）。首次 lint 因原样移植的 `e2e_media_test.go` 未用字段触发 U1000，以及新 chat2019 测试的数字状态码触发 ST1013 失败；前者按目录关闭 U1000 并说明原因，后者改用 http 状态常量，最终通过。没有修改来源测试来消除告警。
- P3.2 文件：新增 `backend/internal/external/netguard.go`、`netguard_test.go`。提供 Policy、NetGuard 及受控 HTTP/coder WS/Gorilla 拨号器；直连不继承环境代理，检查全部解析结果并直接拨已检查 IP，保留原 Host/TLS SNI。代理必须有主机白名单，每个请求及重定向检查；禁止重定向或最多三次，仅 HTTP(S)。Gorilla 对 HTTPS 代理显式使用 TLS CONNECT（原生只支持 HTTP/SOCKS5）。覆盖回环/私有/链路本地/组播/未指定/CGNAT、混合 DNS、二次解析改变、实际拨号 IP、重定向、两种 WS、TLS SNI、代理认证和 Clone。与 how.md 偏差：无。
- P3.2 RED：`cd backend && go test ./internal/external/ -run NetGuard -count=1` 编译成功后退出 1（0.574s）：`地址守卫未实现`、`代理缺少白名单未拒绝`。GREEN 同命令通过（0.364s）；`go test ./internal/external/ -run NetGuard -race -count=1` 最终通过（1.693s）；`go vet ./internal/external/`、`go tool staticcheck ./internal/external/` 均通过。
- P3.3 文件：新增 `backend/internal/external/chat2019/client.go`、`client_test.go`、`media.go`、`media_test.go`。使用 coder/websocket；必须注入守卫 HTTP 客户端/WS 拨号器，无默认裸客户端。实现取 WS 地址/失败回退及协议校验、隐私 sign、串行写入及 10 秒写超时、700 秒读超时、30 秒 ping、forceout、关闭一次回调。媒体客户端通过同一守卫访问图片服务器，缓存十分钟，上传保留原字节及规定表单/请求头/Cookie，下载使用成功端口优先及指定候选列表、大小限制和相对路径校验。测试均使用 httptest；验证 sign 精确字段（空 IP/地址、无 matchPreference）、匹配/取消帧、异常/主动关闭、实际 ping、上传下载、缓存、端口回退、非法地址/路径及上限。与 how.md 偏差：无。
- P3.3 RED：`cd backend && go test ./internal/external/chat2019/... -count=1` 编译成功后退出 1（0.362s）：`Dial未实现: not implemented`、`未使用fallback: not implemented`、`Upload未实现: not implemented`。日志 `/tmp/anonchat-p3-chat-red.log`。实现后 `go test ./internal/external/chat2019/... -race -count=1` 通过（31.476s，含真实 30 秒 ping）；日志 `/tmp/anonchat-p3-chat-green.log`。
- 实施期错误（不计 RED）：首次 chat 测试在并行地址守卫的 WSDialContext 尚未提供时编译失败，等待该接口就绪后取得上述真实 RED；一次在项目根执行 Go 包命令提示 `cannot find main module`，随后在 backend 正确执行通过。
- 最终整体验证：`make test`、`make test-integration`（本机 PostgreSQL `127.0.0.1:55432/anonchat_test`）、`make lint`、`git diff --check` 均通过；日志分别为 `/tmp/anonchat-p3-test.log`、`/tmp/anonchat-p3-integration.log`、`/tmp/anonchat-p3-final-lint.log`。全量测试中 chat2019 分别为 30.566s / 30.411s，晚风为 4.017s / 4.043s。代码整理仅 gofmt 新文件，无额外重构。
- 本轮只记录自身实施与验证证据，未调用 Claude 子进程；全部测试不访问真实外站。未进入 P4，未执行迁移，未提交、未归档，未触碰已有无关改动。

## P4. Bridge（文本链路）
- [√] P4.1 外站账号绑定（chat2019 本地生成；晚风 `BindingStore` 数据库实现，token 加解密）、影子身份取得或创建与资料映射、晚风新账号注册限速。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/binding.go`, `backend/internal/external/peer.go`, 对应测试
  - 完成标准: 同一 `(provider, remote_uid)` 多次调用返回同一影子身份；资料映射符合 how.md §5.4；数据库里 `identity_token_enc` 不是明文；没有 `SETTINGS_ENC_KEY` 时晚风不可用
  - 验证方式: `make test && make test-integration`
- [√] P4.2 Bridge 核心：`provider` / `session` 接口、两个站点的适配（晚风文本只走 `TextCommand`）、选站（先随机选站再复用）、容量、冷却、link 状态机（含 `linkActivating`、入站闸门与每 link 串行入站队列、`Activate` 在 link 锁内调用 publish、所有关闭路径先在锁内标记关闭）、代次校验（`latestAttempt` / `cancelledUpTo`、取消后关闭 link 不复用）、帧处理（含晚风 `code=0`）、入站文本 `SendAs`、`OnMessage` 出站文本（再次确认会话与发送者）、`OnConversationEnded`、连接中断结束会话、热关闭 `ApplySettings`、`Status()`。TDD（用假的 `session`）
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/bridge.go`, `backend/internal/external/provider_chat2019.go`, `backend/internal/external/provider_wanfeng.go`, `backend/internal/external/frames.go`, 对应测试
  - 完成标准: how.md「测试与部署」中 Bridge 列出的文本相关场景都有测试并通过；`OnMessage` 对非外站会话不查库
  - 验证方式: `cd backend && go test ./internal/external/... -race`
- [√] P4.3 装配：`cmd/server/main.go` 和 `routes.go` 创建 Bridge、`SetExternalMatcher`、`AddObserver`、进程退出时 `Close()`；`GET /api/admin/external/status`；外站会话端到端集成测试（假 session：配对 → 双向文本 → 对方离开 → 会话结束）
  - 执行模式: AFK
  - 涉及文件: `backend/cmd/server/main.go`, `backend/internal/api/routes.go`, `backend/internal/external/status_handler.go`, `backend/internal/external/bridge_integration_test.go`
  - 完成标准: 总开关关闭时，现有全部测试不改即通过；端到端集成测试通过；后台 PUT 设置后 `ApplySettings` 被调用
  - 验证方式: `make test && make test-integration && make lint`

### P4 实施记录（仅 P4.1、P4.2、P4.3）

- P4.1 文件：新增 `binding.go`、`peer.go` 及各自单元/集成测试。chat2019 使用随机 32 位小写十六进制 UID 和现有随机昵称；事务 advisory lock 保证同身份并发创建复用绑定。晚风实现 BindingStore，用 `secretbox` 加解密 token；缺少 32 字节密钥时不可用。注册使用 Bridge 共享令牌桶，已有绑定不消耗新账号额度。影子身份按 provider/remoteUID 事务串行创建并复用，更新昵称记录、最近出现时间和性别/年龄/省份公开资料。与 how.md 偏差：无。
- P4.1 RED：`cd backend && go test ./internal/external/ -run 'TestBindingWithoutKey|TestIdentityLimiter|TestPeerProfileMapping' -count=1` 编译成功后退出 1（0.458s）：无密钥 Get/Save 返回 nil、限额 10 时 100 次并发全获准、资料映射为空。带本机 TEST_DATABASE_URL 的 `go test -tags integration ./internal/external/ -run 'TestExternalAccountBinding|TestExternalChatBinding|TestExternalPeer' -count=1` 编译成功后退出 1（0.608s）：chat2019 空 UID、peer 空身份。日志 `/tmp/anonchat-p4-accounts-red-unit.log`、`/tmp/anonchat-p4-accounts-red-integration.log`。
- P4.1 GREEN：上述单元范围加 `-race` 通过（1.677s），集成范围加 `-race` 通过（1.622s）；补充清空绑定兼容断言后按文件运行 integration race 通过（1.614s），单元 race 通过（1.476s）；相关文件 go vet/staticcheck 通过。最终再由整包及整项目验证覆盖。
- P4.2 文件：新增 `bridge.go`、`frames.go`、`bridge_test.go`、`provider_chat2019.go`、`provider_wanfeng.go`、两个适配单元测试及 `provider_wanfeng_connection_test.go`。Bridge 维护 latestAttempt/cancelledUpTo（1 小时无活动清理）、每身份 Start 串行锁、每 link 状态锁；每轮先随机选可用站点再检查空闲连接复用，自己的空闲连接不占新名额，旧匹配连接不可复用。覆盖容量、失败切换、站点/身份冷却、匹配取消、屏蔽后继续匹配和热关闭。
- P4.2 激活/收发：ExternalFound 独立 goroutine 执行，事务 OnCreated 写 external_conversations；Activate 在 link 锁内再次校验有效性、登记 convID→link 并调用 publish。每 link 单独串行入站队列，ExternalFound 成功返回才投递开闸事件，pending 上限 50；入站经 SendAs、稳定 SHA1 UUID、跳过限流、截断 1000 字，过滤自回显/非当前对方。晚风 code=0 媒体失败转换为影子文本、code=16 结束；chat2019 code=16 仅记录编号。OnMessage 仅内存查找，outbox 消费使用 GetMessageForRelay 再验会话和发送者。尚不实现 P5 媒体搬运。
- P4.2 适配：晚风文本只调用 TextCommand，媒体接口只构造 MediaCommand；匹配/取消/离开经 ParseClientFrame+BuildCommand。等 ConnReady 且先下发 showCity=false 后才允许匹配；连接生命周期脱离 open 请求上下文；第一次 ConnLost 即停止客户端。绑定失效删除持久绑定并触发身份冷却；不依赖内部重连。与 how.md 偏差：无。补充即时失效原子标记解决同步回调可能发生在持有 link 锁的 session 调用内部的问题，清理仍先在 link 锁内标记 closed，再于锁外关闭连接/结束会话；Activate 同时检查即时标记。关闭等待入站/出站、在途激活及异步断线/结束回调全部完成。
- P4.2 RED：`go test ./internal/external/ -run TestBridge -count=1` 编译成功后退出 1（0.419s）：Plan 未启用、StartMatch 返回没有可用外站。`go test ./internal/external -run TestProvider -count=1` 编译成功后退出 1（0.561s）：发送帧/命令数为 0、等待就绪未实现。日志 `/tmp/anonchat-p4-bridge-red.log`。
- 补充回归 RED：`go test ./internal/external/ -run TestBridgeDisconnectInvalidatesActivationImmediately -count=1` 退出 1（0.376s）：断线已送达仍发布 found；增加即时失效标记后通过。`go test ./internal/external/ -run TestBridgeNewAttemptReplacesOldMatchingLinkAtCapacity -count=1` 退出 1（0.378s）：容量满时新代次返回没有可用外站；替换旧匹配连接后通过。`go test ./internal/external -run TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost -count=1` 退出 1（0.384s）：selfID 为远端 UID 而非 ClientConfig.IdentityID；分开保存本地回显 ID 与远端协议 UID 后通过。所有 RED 都是可编译后的行为失败。
- P4.2 GREEN：`go test ./internal/external/ -run TestBridge -race -count=1` 通过（1.441s）；`go test ./internal/external -run TestProvider -race -count=1` 最终通过（1.411s）；`go test ./internal/external/ -run 'TestBridge|TestProvider' -race -count=1` 在收尾改动后再次通过（1.387s）。真实移植客户端的适配测试使用本地 ECDH/加密 WS 假上游，未访问真实站点。
- P4.3 文件：新增 `bootstrap.go`、`bootstrap_test.go`、`status_handler.go`、`bridge_integration_test.go`、`internal/api/external_status_integration_test.go`；修改 `internal/api/routes.go`、`cmd/server/main.go`、`internal/testutil/testutil.go`。统一创建守卫客户端、不在启动时连外站；无论 bot 是否启用都 SetExternalMatcher、AddObserver、OnChange。GET status 使用 RequireAdmin，输出 §7.1 的 providers 数组、计数、online/queue。退出先关闭 Bridge 并等待在途工作，再取消独立 worker context，避免匹配器提前停止。与 how.md 偏差：无。
- P4.3 RED：`go test ./internal/external -run TestConfiguredBridge -count=1` 退出 1（0.444s）：守卫未装配、非法协议覆盖未拒绝；本机 TEST_DATABASE_URL 下 `go test -tags integration ./internal/api -run TestExternalStatusIsAuthenticatedAndSettingsReachBridge -count=1` 退出 1（0.468s）：未登录返回 404 而非 401。日志 `/tmp/anonchat-p4-bootstrap-red.log`、`/tmp/anonchat-p4-status-red.log`。端到端测试先编写，待 Bridge 可编译后直接 GREEN，不把 fixture 错误记成 RED。
- P4.3 GREEN：`go test ./internal/external -run TestConfiguredBridge -race -count=1` 通过（1.358s）；本机 TEST_DATABASE_URL 下 API status 同范围 `-race` 通过（6.680s）；`go test -tags integration ./internal/external -run TestBridgeEndToEndWithFakeSession -count=1` 通过（0.606s），加 `-race` 通过（2.100s）；`go test ./cmd/server -count=1` 通过（0.337s）；`go vet ./internal/external ./internal/api ./cmd/server`、同范围 `go tool staticcheck` 通过。端到端在独立临时数据库验证假 session 配对→真实 WS found→双向文本落库/广播→peer_left→leaveRoom，结束后清理临时库。
- 实施期错误（不计 RED）：新 API 骨架/并行文件未就绪曾导致编译失败；bootstrap 测试最初漏接 DownloadMedia 的第三个返回值；测试网关漏传 AccessLogger；临时数据库 fixture 最初使用保留旧库名的 ConnString，改成 stdlib.OpenDB(*cfg.ConnConfig) 并清理空临时库；一次显式测试仅 integration 文件的包未带标签，以及从根目录执行 Go 包命令导致 cannot find main module，均已纠正。没有用这些错误替代行为 RED。
- 整体验证：`go test ./internal/external/... -race -count=1` 通过；`TEST_DATABASE_URL=本机 go test -tags integration -p 1 ./internal/external/... ./internal/api/... -race -count=1` 通过（external 8.198s、chat2019 31.492s、wanfeng 5.176s、api 6.854s）；`make test`、`make test-integration`、`make lint` 通过。所有数据库验证仅本机 `127.0.0.1:55432`；没有运行 migrate-down，没有改依赖或晚风移植原文件。
- 本轮不进入 P5；不提交、不归档，不调用外部阶段复核子进程。上述为自身实施与验证证据。

- 收尾验证：`make test`、`make test-integration`、`make lint` 再次通过，日志 `/tmp/anonchat-p4-final-test.log`、`/tmp/anonchat-p4-final-integration.log`、`/tmp/anonchat-p4-final-lint.log`；`git diff --check` 通过。P4.1/P4.2/P4.3 已勾选，P5 及之后保持未执行。

## P5. 用户媒体与外站媒体
- [√] P5.1 `media.Service`：抽出 `saveFile`、`SaveUserItem`、`SaveExternalItem`、`GetUserMediaForSend`、`OpenItem`（how.md §6.1）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/media/service.go`, `backend/internal/media/*_test.go`
  - 完成标准: 现有媒体库测试不改即通过；三种来源的存储键前缀、`source`、`owner_identity_id` 正确；超过上限返回 `media_rejected`
  - 验证方式: `make test && make test-integration`
- [√] P5.2 `POST /api/media/upload` 与限流规则 `media_upload_per_minute`（how.md §6.3）；访问规则（上传者在发送前可以访问）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/media/handler.go`, `backend/internal/moderation/ratelimit_rules.go`, `backend/internal/api/routes.go`, 对应测试
  - 完成标准: 未登录返回 401；非白名单类型返回 `media_rejected`；超过频率返回 `rate_limited`；上传者在发送前能 GET `/api/media/{id}`，其他人返回 404
  - 验证方式: `make test && make test-integration`
- [√] P5.3 `msg.send` 支持媒体（how.md §6.4）：`mediaId` 字段、归属校验、`RequireOneToOne`。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/conversation/pipeline.go`, `backend/internal/conversation/*_test.go`
  - 完成标准: 1v1 会话（match/bottle/private）可以发自己的图片和视频，对方收到的 `msg.new` 带 `mediaUrl`；他人的 mediaId、kind 不一致、聊天室和故事书都返回 `invalid_request`；bot 发媒体的现有测试不改即通过
  - 验证方式: `make test && make test-integration`
- [√] P5.4 清理任务 `orphan_media`（how.md §6.6）。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/jobs/cleaner.go`, `backend/internal/jobs/cleaner_integration_test.go`, `backend/queries/retention.sql` 或 `backend/queries/media.sql`, `backend/internal/conversation/pipeline.go`（外键失败的错误映射）
  - 完成标准: 按 how.md §6.6 的加锁协议实现；超过 24 小时且没有未焚毁消息引用的非媒体库媒体被删除（行和文件）；媒体库条目、24 小时内的条目、仍被引用的条目都保留；并发「发送 vs 清理」测试通过（消息的 `media_id` 不会被置空）；`msg.send` 遇到外键失败返回「媒体已失效」
  - 验证方式: `make test-integration`
- [√] P5.5 Bridge 媒体中转（how.md §6.5）：chat2019 上传下载、晚风 `MediaBridge` 实现（地址白名单、随机 token 登记与一次性使用）、晚风媒体只走 `MediaCommand`、出站失败（含 `OnCommandFailed`）推送 `error`；再加一条「真实移植客户端 + 伪造晚风上游」的完整链路测试。TDD
  - 执行模式: AFK
  - 涉及文件: `backend/internal/external/media.go`, `backend/internal/external/provider_*.go`, 对应测试
  - 完成标准: 入站图片和视频以影子身份发出、`kind` 与嗅探结果一致；出站图片能上传到伪造的外站服务器并发出正确的帧；回显不重复下载；出站失败时用户收到 `media_rejected`；伪造的 `anonchat-` token（文本里的、未登记的、其他 link 的）不会触发媒体读取或发送；完整链路测试通过
  - 验证方式: `cd backend && go test ./internal/external/... -race && cd .. && make test-integration`


### P4 修正与 P5 实施记录（本轮仅至 P5.5）

- [√] P4 修正 1：`external/frames.go` 的 code=15 仅在 matching 处理匹配、idle 退出迟到房间；activating/inRoom 只记不含正文的 debug 日志。`p4_corrections_test.go` 覆盖四种状态。与 how.md 偏差：无。
- [√] P4 修正 2：`external/bridge.go`、`frames.go`、`provider_wanfeng.go` 统一分类失败；媒体失败只计一次 mediaFailed，推送规定的 `media_rejected` /「对方站点暂不支持该媒体，发送失败」；文本失败只计 textFailed 和日志。队列满按事件 kind 分类，晚风回调按 cmd.Media 分类，媒体配文的文本失败按文本处理。与 how.md 偏差：无。
- P5.1 文件：`media/service.go`、新增 `media/user_service_integration_test.go`。SaveItem 仅抽出文件写入/嗅探/白名单/提交为 saveFile，保留原插入数据库和失败删除文件逻辑；新增 SaveUserItem、SaveExternalItem、GetUserMediaForSend、OpenItem，用户独立限额，路径按 source/YYYYMM 分区。与 how.md 偏差：无。
- P5.2 文件：`media/handler.go`、`moderation/ratelimit_rules.go`、`api/routes.go`、`testutil/testutil.go`；新增 `media/user_upload_integration_test.go`、`moderation/media_upload_test.go`。身份认证与验证复用 chatProtected，单文件上传、嗅探与大小限制、每身份默认 10/min 动态限流；P1 的发送前预览查询直接复用。与 how.md 偏差：无。
- P5.3 文件：`conversation/pipeline.go`、`conversation/service.go`、新增 `conversation/user_media_integration_test.go`；媒体依赖在 routes/testutil 装配。校验 source=user、owner、kind、UUID；RequireOneToOne 只放行 match/bottle/private；媒体 FK 23503 映射精确「媒体已失效，请重新上传」。原 bot 媒体测试未修改。与 how.md 偏差：无（本项目 HandleSend 位于 pipeline.go）。
- P5.4 文件：`jobs/cleaner.go`、新增 `jobs/orphan_media_integration_test.go`、`cmd/server/main.go`（共享媒体 Storage 装配）。复用 P1 已有的两条锁定/删除 SQL，无需修改查询或重新生成 store。READ COMMITTED 每批事务、(created_at,id) 游标、500 行/批、最多 20 批、提交后删文件；清理放在保留期删除消息之前，刚释放的媒体下一轮清理。覆盖 501 条引用后的孤儿、10001 条批次上限、双方先持锁、第二语句重新检查、提交后删文件及失败继续。与 how.md 偏差：无。
- P5.5 文件：新增 `external/media.go`、`media_test.go`、`media_integration_test.go`；修改 `external/bridge.go`、`frames.go`、`provider_wanfeng.go`、`bootstrap.go`。出站先验证数据库消息归属再读取媒体；chat2019 上传/下载；晚风只走 MediaCommand，每 link 随机 16 字节 hex token、一次性消费、入站只认本 link 登记；按 BaseURL 解析后检查媒体主机白名单，身份专用 Clone/Cookie；MediaURLKey 一小时缓存避免重复下载，重复入站 URL 复用文件但生成新的单次 token。配文在媒体后发送。入站 kind 由保存时嗅探决定（含伪装后缀的图片与视频）；关闭取消在途下载，跨 attempt 的迟到下载不能登记。真实移植 wanfeng.Client + 本地 ECDH/WS/HTTP 假上游覆盖配对、双向文本、入站/出站图片、缓存、上传失败回调与断线。与 how.md 偏差：无。

#### TDD RED 证据

- P4 两修正：`cd backend && go test ./internal/external -run TestP4 -count=1` 编译成功、退出 1（0.453s）。activating/inroom 报「活动会话被退出: leaves=1 resolves=0」；文本报「文本失败不应推送media_rejected」；媒体报旧文案「外站消息发送失败」。日志 `/tmp/anonchat-p5-p4-red.log`。随后目标 race 测试通过（1.476s）。
- P5.1/P5.2：先新增接口骨架与测试，真实行为失败为 SaveUserItem 返回 `not_found: 媒体不存在`、未装配上传路由返回 `405 want401`（0.599s）；日志 `/tmp/anonchat-p5-media-red.log`。限流测试报「上传限流规则未按契约注册」（0.325s），日志 `/tmp/anonchat-p5-limit-red.log`。
- P5.3：`TEST_DATABASE_URL=本机库 go test -tags integration ./internal/conversation -run TestUserMedia -count=1` 编译成功退出 1（0.551s）；6 种 1v1 图片/视频均报「V1 只支持文本消息」，RequireOneToOne 返回 nil。日志 `/tmp/anonchat-p5-3-red.log`。
- P5.4：`TEST_DATABASE_URL=本机库 go test -tags integration ./internal/jobs -run TestOrphanMedia -count=1` 编译成功退出 1（0.682s）；孤儿删除数为 0、501 引用后的孤儿仍在、文件删除未调用、批次上限未执行。日志 `/tmp/anonchat-p5-orphan-red.log`。
- P5.5：`cd backend && go test ./internal/external -run TestMediaUnknown -count=1` 编译成功退出 1（0.583s）；「未登记token必须作为普通文本: []」。日志 `/tmp/anonchat-p5-bridge-red.log`。
- 实施期非 RED：媒体接口尚未实现时，其他并行测试曾编译失败，不作为行为 RED。共享测试库并行 Truncate 曾导致管理员登录 500/用户名冲突、引用计数异常；停止并行数据库测试后，以下串行全量及 race 验证覆盖并通过。一次本地测试路径错误与失败断言持锁造成测试中断，修正测试后通过，不计产品失败。

#### 最终验证（所有数据库命令仅连接本机 anonchat_test）

- `make test`：通过，全后端单元测试。日志 `/tmp/anonchat-p5-test.log`。
- `make test-integration`：通过，全后端集成测试，Makefile 使用 `-p 1` 串行运行。日志 `/tmp/anonchat-p5-integration.log`。
- `make lint`：通过，go vet、staticcheck、前端 eslint。日志 `/tmp/anonchat-p5-lint.log`。
- `cd backend && go test ./internal/external/... -race -count=1`：通过；external 1.464s、chat2019 31.461s、wanfeng 5.098s。日志 `/tmp/anonchat-p5-external-race.log`。
- `cd backend && TEST_DATABASE_URL=本机库 go test -tags integration -p 1 ./internal/external/... ./internal/media/... ./internal/conversation/... ./internal/jobs/... ./internal/bot/... ./internal/api/... -race -count=1`：全部通过；external 8.320s、chat2019 31.455s、wanfeng 5.187s、media 7.073s、conversation 2.167s、jobs 2.273s、bot 70.949s、persona 3.124s、api 6.973s。日志 `/tmp/anonchat-p5-final-race.log`。
- 随后仅补强 external 重复入站缓存与视频嗅探测试：`cd backend && TEST_DATABASE_URL=本机库 go test -tags integration ./internal/external -race -count=1` 通过（7.280s）；日志 `/tmp/anonchat-p5-external-final.log`。`go test ./internal/external -run 'TestMedia|TestP4' -race -count=1 -timeout 30s` 通过（1.415s）；`go vet ./internal/external/... && go tool staticcheck ./internal/external/...` 通过。
- 子任务定向验证：media 全包集成 race 通过（7.324s）；conversation 定向 TestUserMedia 通过（0.554s）、集成 race 通过（2.120s）；jobs 定向 TestOrphanMedia race 通过（1.780s）；moderation/media/cmd-server 单元与相关 vet/staticcheck 通过。子任务后的改动均由上述最终命令重新覆盖。
- `git diff --check`：通过。原有媒体、bot、match 测试未修改；本轮没有依赖、迁移或 SQL 变动，没有访问真实外站，没有调用 Claude 子进程，没有提交。P6 及之后条目保持未勾选；不归档、不生成 P10 的全方案 QA 文件。

## P6. 前端
- [√] P6.1 用户发媒体：`uploadMedia`、`sendMessage` 的媒体参数、`useChat.sendMedia`、`Composer` 的 `onSendMedia`、匹配/漂流瓶/私聊页面接入、`media_rejected` 提示（how.md §7.2）。TDD
  - 执行模式: AFK
  - 涉及文件: `frontend/src/lib/api/*`, `frontend/src/lib/ws/*`, `frontend/src/lib/useChat.ts`, `frontend/src/components/Composer.tsx`, 匹配/漂流瓶/私聊聊天页, 对应测试
  - 完成标准: 不传 `onSendMedia` 时没有附件按钮（聊天室、故事书）；选择文件后先上传再发送；上传失败有提示且按钮恢复可用
  - 验证方式: `make web-test && cd frontend && pnpm build`
- [√] P6.2 后台「外站接入」页面（设置表单 + 状态表 + 导航和路由）；bot 设置页「匹配时间」旁加说明（ADR-6）；举报详情页显示媒体（已支持就只补测试）。TDD（表单校验纯函数）
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/admin/pages/ExternalPage.tsx`, `frontend/src/features/admin/externalForm.ts`, `frontend/src/features/admin/externalApi.ts`, 后台路由和导航文件, `frontend/src/features/admin/BotPanel.tsx`, 举报详情相关文件, 对应测试
  - 完成标准: 越界的值在前端就提示；保存后刷新显示新值；状态表展示 how.md §7.1 的字段
  - 验证方式: `make web-test && cd frontend && pnpm build`

### P6 实施与验证记录（仅至 P6.2）

- bot 测试：`backend/internal/bot/bot_integration_test.go` 仅按本轮授权在发送「在吗」前新增等待开场模型调用的一行，其他断言未改。初次普通循环通过 1 次后第 2 次失败；再次普通循环同样 1 次通过、第 2 次失败；race 循环 2 次通过、第 3 次失败。均为第 125 行收集 msg.new 时 `failed to get reader: context deadline exceeded`，不是原先的两次模型调用超时。证据 `/tmp/anonchat-p6-bot-20.log`、`/tmp/anonchat-p6-bot-20-retry.log`、`/tmp/anonchat-p6-bot-20-race.log`。现有 sendMessage/readUntil 等 ack 时会丢弃先到的开场 msg.new，开场图片可能因此不进入后续收集循环；已向用户说明并请求额外授权，本轮没有自行扩大测试改动。
- P6.1 文件：`frontend/src/lib/api/client.ts`、`lib/ws/client.ts`、`lib/useChat.ts`、`components/Composer.tsx`，匹配/漂流瓶/私聊聊天页及对应测试；新增 `lib/api/media.test.ts`、`lib/useChat.test.tsx`、`features/chatMedia.test.tsx`。上传使用 FormData 与同源 Cookie；成功后才发送含 kind/mediaId 的消息，重试保留媒体字段；会话切换后不发送迟到上传。附件按钮按回调显示、限制 accept、上传时禁用、失败提示并恢复。聊天室/故事书不传回调。无 ID 的 media_rejected 已有统一 error 事件与 App 提示，按约定未改。与 how.md 偏差：无。
- P6.2 文件：新增 `features/admin/externalApi.ts`、`externalForm.ts`、`pages/ExternalPage.tsx` 及测试；修改 `AdminPage.tsx`、`AdminLayout.tsx`、`AdminRoutes.test.tsx`、`BotPanel.tsx`、`pages/ReportDetailPage.tsx` 并新增举报页面测试。纯函数校验范围与后端一致；全量保存、返回值刷新、状态 10 秒刷新且不覆盖编辑、卸载停止轮询；展示 §7.1 全部字段与计数，关闭警示及 Bot 说明按要求加入。
- P6.2 必要配套：`backend/queries/moderation.sql`、生成 `internal/store/moderation.sql.go`、`internal/moderation/report.go`、新增 `report_media_integration_test.go`。原举报快照没有 kind/mediaId，无法只靠页面展示媒体；最小补齐上下文查询和快照字段，图片/视频走已有后台媒体文件接口，保留文本、焚毁、顺序和 reported 语义。与 how.md 的文件范围偏差：增加上述后端字段与测试，以实现 §7.2 要求的真实媒体展示，不增加新接口。

#### P6 TDD RED

- P6.1：`cd frontend && pnpm test src/lib/api/media.test.ts src/lib/useChat.test.tsx src/lib/ws/client.test.ts src/components/Composer.test.tsx` 编译成功后退出 1，7 失败 / 15 通过：缺 uploadMedia/sendMedia、媒体仍发送 text、附件按钮不存在。实现后同命令 22 测试通过；日志 `/tmp/anonchat-p6-1-red.log`、`/tmp/anonchat-p6-1-green.log`。
- P6.2 纯函数：`pnpm exec vitest run src/features/admin/externalForm.test.ts` 退出 1：空白 onlineThreshold 返回空错误列表；日志 `/tmp/anonchat-p6-admin-red.log`。页面测试初始失败为缺少「在线人数阈值」控件（2 失败）；日志 `/tmp/anonchat-p6-admin-page-red.log`。举报页面先因未渲染图片失败，再补实现。
- 举报后端：`cd backend && TEST_DATABASE_URL=本机库 go test -tags integration ./internal/moderation -run TestReportSnapshotPersistsMediaKindsAndIDs -count=1` 编译成功后退出 1（0.459s），真实图片/视频快照缺少 kind/mediaId；日志 `/tmp/anonchat-p6-report-red.log`。测试 fixture 首次缺 client_msg_id 的失败不计作 RED。

#### P6 验证

- bot 完整 20 次验证（backend 下）：`for i in $(seq 1 20); do TEST_DATABASE_URL='postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable' go test -tags integration ./internal/bot -run '^TestMatchFallbackEndToEnd$' -count=1; done`（实际日志逐次附 RUN 序号）：9 次通过、11 次失败，失败均在第 125 行等待 msg.new 时 context deadline exceeded。**未达到 20/20，不能声明 bot 验证通过**；日志 `/tmp/anonchat-p6-bot-full20.log`。未修改用户未授权的消息收集逻辑。
- `make web-test`：通过，44 文件 / 217 测试，随后 typecheck 与 ESLint 通过；日志 `/tmp/anonchat-p6-web-test.log`。
- `cd frontend && pnpm build`：通过，TypeScript 与 Vite 生产构建成功；日志 `/tmp/anonchat-p6-build.log`。
- `cd frontend && pnpm test src/features/chatMedia.test.tsx`：通过，覆盖三种 1v1 页面真实上传/WS 消息与房间/故事书没有附件；日志 `/tmp/anonchat-p6-1-pages.log`。
- `cd frontend && pnpm exec vitest run src/features/admin`：通过，17 文件 / 113 测试；`pnpm exec tsc -b --noEmit`、相关文件 ESLint 通过。最终全量 web-test 已覆盖这些文件。
- `make sqlc`：通过。
- `cd backend && TEST_DATABASE_URL=本机库 go test -tags integration ./internal/moderation -run TestReport -race -count=1`：通过，最终重跑 1.429s；日志 `/tmp/anonchat-p6-report-final.log`。
- `cd backend && go test ./internal/moderation/... -race -count=1`：通过（1.826s）；`go vet ./internal/moderation/...`、`go tool staticcheck ./internal/moderation/...`：通过。
- `git diff --check`：通过。数据库仅连接本机 anonchat_test；没有访问真实外站、调用 Claude 子进程、提交或归档。P7 及之后保持未执行。

## P7. 探测工具
- [√] P7.1 `backend/cmd/probe-external`（how.md §8）；参数解析和汇总输出用 httptest 伪造的上游测试；**不实际连接真实站点**
  - 执行模式: AFK
  - 涉及文件: `backend/cmd/probe-external/main.go`, `backend/cmd/probe-external/main_test.go`
  - 完成标准: `go build ./cmd/probe-external` 成功；`-state` 文件可以重复使用、权限为 0600、拒绝仓库内路径、打印的帧不含 token 等敏感字段；测试通过
  - 验证方式: `cd backend && go test ./cmd/probe-external/... && go build ./cmd/probe-external`

### bot 测试修正与 P7 实施记录（本轮仅至 P7.1）

- bot 文件：仅修改 `backend/internal/bot/bot_integration_test.go` 的 `TestMatchFallbackEndToEnd`，保留开场模型调用 waitFor，将其后的 sendMessage 换成相同信封（含 id=s1、conversationId、clientMsgId、kind=text、body=在吗）的 sendWS，不等待 ack，由既有循环处理所有 msg.new。辅助函数和其他断言未修改。与用户修法及 how.md 偏差：无；这是测试时序修复，不改生产代码。RED 复用 P6 已记录的完整 20 次结果：9 通过 / 11 失败，失败为收集 msg.new 时 context deadline exceeded；本轮不再新增复现测试。
- P7.1 文件：新增 `backend/cmd/probe-external/main.go`（入口、参数）、`runner.go`（多身份流程和汇总）、`clients.go`（两站客户端适配）、`state.go`（绑定持久化）、`output.go`（帧脱敏）；测试为 `main_test.go`、`runner_test.go`、`wanfeng_test.go`。
- 实现：支持 -provider、-n、-match、-send-image、-proxy、-state。读取与服务端相同的外站 endpoint/protocol/media-host 环境变量与默认值；不读取数据库、不启动 Bridge。chat2019 使用现有 Dial/Media 和 NoRedirect NetGuard，永远直连；晚风使用真实移植 Client/APIClient、主机白名单 NetGuard 和 MemoryBindingStore。-proxy 只允许用于晚风（符合 §4.3 的 chat2019 永远直连约束）。先关闭位置公开再匹配；晚风媒体只走 MediaCommand，首次注册后从绑定显式设置上传 UID；匹配成功可发一张图片，到时间或断线退出。
- 状态保护：默认 ~/.anonchat-probe/state.json；身份 probe-N 跨运行保持稳定，绑定更新时即时持久化。文件 0600、专用目录 0700，临时文件 + Sync + rename；拒绝仓库内、通过符号链接指向仓库、状态文件本身为符号链接的路径，拒绝损坏 JSON 或权限过宽的既有文件/目录，不擅自 chmod 共享父目录。读取/保存错误不打印状态内容。
- 输出：JSON Lines 帧和逐身份 summary（connectMs、connected、matched、matchMs、errorType）；递归删除 token/identityToken/sid/pub/cookie/authorization 等敏感键，所有字符串至多保留 200 个字符正文加省略标记；库日志丢弃，错误只打印类型与固定说明，不输出原始第三方错误。并发输出串行化，入站队列溢出有错误类型。与 how.md 偏差：无；构建产物放到 /tmp，避免在工作区遗留二进制。

#### P7 RED 与实施期检查

- `cd backend && go test ./cmd/probe-external -count=1`：编译成功后退出 1，TestOptions 返回 not implemented、TestFrameRedaction 泄露测试用敏感字段且正文未截断、TestStatePersistenceAndProtection 返回 not implemented；日志 `/tmp/anonchat-p7-red.log`。
- `cd backend && go test ./cmd/probe-external -run 'TestProbe|TestOptions|TestFrame|TestState' -count=1`：编译成功后退出 1，chat2019 流程 not implemented，超时/守卫流程缺 summary；日志 `/tmp/anonchat-p7-flow-red.log`。随后实现流程并通过。
- 晚风本地链路测试首次暴露：伪造 Cookie 未设 Path=/（测试 fixture 修正），新注册账号上传缺 userId（探测适配器 ConnReady 时显式设置绑定 UID 后修正）；权限测试使用独立 0700 目录；这些不计上述骨架 RED。首次 staticcheck 报测试内 HTTP 503 字面量 ST1013，改为 http.StatusServiceUnavailable 后通过。

#### 本轮最终验证

- backend 下普通循环：`for i in $(seq 1 20); do TEST_DATABASE_URL='postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable' go test -tags integration ./internal/bot -run '^TestMatchFallbackEndToEnd$' -count=1 || exit 1; done`：**20/20 通过**；日志 `/tmp/anonchat-p7-bot20-final.log`（逐次附 RUN 序号）。
- backend 下 race 循环：`for i in $(seq 1 10); do TEST_DATABASE_URL='postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable' go test -tags integration ./internal/bot -run '^TestMatchFallbackEndToEnd$' -race -count=1 || exit 1; done`：**10/10 通过**；日志 `/tmp/anonchat-p7-bot-race10-final.log`。最终相同信封版本已覆盖；补 id=s1 前曾另跑普通 20/20、race 10/10 通过，不作为最终版本替代证据。
- `cd backend && go test ./cmd/probe-external/... -count=1`：通过（1.188s）；日志 `/tmp/anonchat-p7-test-final.log`。
- `cd backend && go test ./cmd/probe-external/... -race -count=1`：通过（2.240s）；日志 `/tmp/anonchat-p7-race-final.log`。覆盖参数/默认路径、递归脱敏和中文截断、状态复用与权限、仓库及 symlink 拒绝、损坏状态、chat2019 双身份连接/匹配/上传/发图、匹配超时和实际守卫拒绝私网；真实晚风加密握手/WS 客户端两次复用同一账号并发图、Cookie、上传失败、断线不重连，以及本地 HTTP 代理 + WS CONNECT。
- `cd backend && go build -o /tmp/anonchat-probe-external ./cmd/probe-external`：通过，未运行生成的工具。
- `cd backend && go vet ./cmd/probe-external/...`、`go tool staticcheck ./cmd/probe-external/...`：通过。
- `git diff --check`：通过。仅 bot 测试连接本机 anonchat_test，探测工具测试全部用 httptest；没有实际连接真实外站，没有调用 Claude 子进程、提交、迁移或归档。当时 P8 及之后尚未执行；后续阶段证据见下方 P8–P10 收尾记录。

## P8. 安全检查
- [√] P8.1 按 how.md §0 与「安全与性能」逐条核对：代次校验、媒体归属校验、文本不会变成媒体、内部 token 一次性且按 link 登记、MIME 嗅探、地址守卫（含重定向和克隆客户端）、token 加密存储、日志不含 token/Cookie/正文全文、隐私字段（不发送 IP、省份、本站昵称）、热关闭、匹配 goroutine 内无 IO 且不调用 `do()`、`OnMessage` 快速返回

### P8–P10 收尾记录

- P8.1：新增 `p8-review.md` 与 `p8-external-coverage.md`，逐条记录 how.md §0、安全性能和测试部署场景；新增 `backend/internal/external/p8_bridge_test.go` 覆盖不可用站点、双不可用、真实会话结束复用、IP 冷却、容量下调、单站关闭、迟到旧 Start。发现并修复 external 日志泄露：新增 `external/logging.go` 白名单日志 handler，Bridge/ConfiguredBridge 装配时应用；新增 `logging_test.go` 先 RED 后 GREEN，未修改 wanfeng 移植文件。与 how.md 偏差：无。P8 逐场景覆盖、代码位置、遗漏处理和证据详见上述两文档。
- P8 验证：`go test ./internal/external -run TestExternalLogs -race -count=1`（RED `/tmp/anonchat-p8-logging-red.log`，GREEN `/tmp/anonchat-p8-logging-green.log`）；新增 Bridge 六组定向 race（1.417s）、`go vet ./internal/external`、`staticcheck ./internal/external`；`make lint`（含全仓 vet/staticcheck 与前端 lint）通过；`go test ./internal/external/... -race -count=1` 通过；`rg -n 'identityToken|Cookie' backend/internal/external --glob '!*_test.go'` 仅命中协议字段、绑定/请求实现和注释，无日志调用打印这些值。未访问真实外站。
- P9.1：更新 `helloagents/main/wiki/{glossary,protocol,data,deploy}.md`、`helloagents/main/project.md`、`deploy/README.md`、`deploy/.env.example`；补充外站术语、媒体/外站协议、00010 数据与清理、所有新环境变量、probe 用法和状态文件保护、单站渐进启用、关闭总开关与 00010→00009 回滚及代理 DNS 剩余风险。所有文档链接到本方案；外站媒体 `owner_identity_id` 按代码记录为 NULL。与 how.md 偏差：无。验证：相对链接 Python 检查通过，`git diff --check` 通过。
- P10.1：完整门禁命令一次执行全部通过；集成测试明确 `-p 1 -count=1`，仅连接本机 `anonchat_test`。生成 `qa-review.json`，gate_status=passed。P11 人工任务未执行。
  - 执行模式: AFK
  - 涉及文件: 本次改动的全部文件
  - 完成标准: 每一条都给出对应的代码位置或测试名；发现的问题已修复并补回归测试
  - 验证方式: 人工核对 + `make lint` + `cd backend && go vet ./...` + `rg -n "identityToken|Cookie" backend/internal/external --glob '!*_test.go'` 逐条确认没有打印到日志

## P9. 文档
- [√] P9.1 更新知识库与部署文档：`wiki/glossary.md`（how.md「领域语言」）、`wiki/protocol.md`（`msg.send` 的 `mediaId`、`POST /api/media/upload`、后台外站接口）、`wiki/data.md`（新表和 `media_items` 变更）、`wiki/deploy.md` 和 `deploy/README.md`、`deploy/.env.example`（新环境变量、启用步骤、回滚注意事项）、`project.md`（模块列表加入 `external`）
  - 执行模式: AFK
  - 涉及文件: `helloagents/main/wiki/*.md`, `helloagents/main/project.md`, `deploy/README.md`, `deploy/.env.example`
  - 完成标准: 文档与代码事实一致，并链接到本方案包
  - 验证方式: 只读核对

## P10. 最终验证
- [√] P10.1 运行全部验证命令（集成测试显式加 `-count=1`）；生成 `qa-review.json`（格式参照 `helloagents/main/plan/202611092002_bot-persona-memory/qa-review.json`）
  - 执行模式: AFK
  - 涉及文件: `qa-review.json`
  - 完成标准: 全部命令通过，无失败或重跑；qa-review.json 的 `gate_status` 为 `passed`
  - 验证方式: `make sqlc && make test && (cd backend && TEST_DATABASE_URL='postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable' go test -tags integration -p 1 -count=1 ./...) && make lint && make web-test && (cd frontend && pnpm build)`

## P11. 上线前人工确认（HITL，Codex 不执行）
- [ ] P11.1 在部署机（cch_hk）上运行 `probe-external`，分别测试 chat2019 和晚风：能否连接、能否配上、配上耗时、图片收发、chat2019 code 16 的含义、多身份同时连接时有无风控
  - 执行模式: HITL
  - 涉及文件: 无（结果记录到 `helloagents/main/wiki/deploy.md`）
  - 完成标准: 用户确认探测结果，并决定各站点的初始连接上限
  - 验证方式: 人工
- [ ] P11.2 发布后在后台逐个打开站点，观察状态页
  - 执行模式: HITL
  - 涉及文件: 无
  - 完成标准: 用户确认
  - 验证方式: 人工
