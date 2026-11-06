# 任务清单: AnonChat 匿名聊天平台（V1 + V2）

目录: `helloagents/main/plan/202611061637_anonchat-v1-v2/`

执行顺序：0 → V1（1–14）→ V1 验收 → V2（15–23）。模块内的任务严格按 RED → GREEN → REFACTOR+VERIFY 的顺序执行。

通用验证命令（任务 1.x 完成后可用）：
- 后端单元测试：`make test`（即 `cd backend && go test ./...`）
- 后端集成测试：`make test-integration`（需要先 `make db-up`）
- 前端测试：`make web-test`（即 `cd frontend && pnpm test && pnpm typecheck && pnpm lint`）
- E2E 测试：`make e2e`

---

## 并行子代理标注（可选）

- 并行组 A（任务 2 完成后）: 任务 [5.1, 5.2]；允许写入: `backend/internal/moderation/wordfilter*`, `backend/internal/moderation/ratelimit*`；冲突域: 无；验证: `cd backend && go test ./internal/moderation/...`
- 并行组 B（任务 4 完成后）: 任务 [7, 8, 9]；允许写入: 分别是 `backend/internal/{room,bottle,privatechat}/`、各自的 `queries/*.sql` 文件；冲突域: `backend/internal/api/routes.go`（每个模块只追加自己的路由注册，合并时串行处理）；验证: `make test && make test-integration`
- 不可并行任务: [1.x, 3.x, 4.x, 13.x, 14.x, 22.x, 23.x]；原因: 骨架、网关和消息管道是共同依赖；安全检查和最终验证需要在集成后执行

---

## 0. 方案边界与前置条件
- [√] 0.1 确认实施只覆盖 why.md 的范围内条目，范围外条目不进入实现
  - 执行模式: AFK
  - 涉及文件: `why.md`, `how.md`, `task.md`
  - 完成标准: 范围内和范围外的描述在三个文件中一致，没有矛盾
  - 验证方式: 只读核对方案包三个文件
- [-] 0.2 在本机安装容器运行时（OrbStack 或 Docker Desktop），确保 `docker compose version` 可用
  - 执行模式: HITL（需要用户安装）
  - 涉及文件: 无
  - 完成标准: `docker compose version` 输出版本号
  - 验证方式: `docker compose version`
  - 备注: 本机未安装 Docker；开发与集成测试改用本机 PostgreSQL 16（127.0.0.1:55432），compose 相关验证（12.x）在安装 Docker 后补做
- [√] 0.3 用户确认 ADR-004（阅后即焚语义）和 ADR-005（实名开关）
  - 执行模式: HITL
  - 涉及文件: `how.md`
  - 完成标准: 两个 ADR 的状态改为"已采纳"，或按用户意见修订
  - 验证方式: 只读核对 how.md ADR 状态

---

  - 备注: 2026-11-06 用户明确同意 ADR-004（阅后即焚只对界面生效，服务端保留 7 天）与 ADR-005（实名验证可配置开关），状态已改为"已采纳"
# V1：可公开上线的最小完整版本

## 1. 工程骨架
- [√] 1.1 创建后端骨架：`go mod init`、`cmd/server`（子命令 `serve`/`migrate`/`admin`）、`internal/config`（加载环境变量并校验，缺少必填项时报错退出）、`platform/httpx`（JSON 响应、错误码、RequestID/日志/Recover/安全头中间件）、`GET /healthz` 和 `/readyz`；根目录创建 `Makefile`、`.gitignore`、`.editorconfig`
  - 执行模式: AFK
  - 涉及文件: `backend/go.mod`, `backend/cmd/server/main.go`, `backend/internal/config/*`, `backend/internal/platform/httpx/*`, `Makefile`, `.gitignore`, `.editorconfig`
  - 完成标准: `go build ./...` 成功；配置校验有单元测试；`curl localhost:8080/healthz` 返回 200
  - 验证方式: `cd backend && go vet ./... && go test ./internal/config/... && go build ./...`；手工 curl
  - TDD: config 校验部分按 TDD；其余为脚手架，TDD-EXEMPT，原因：没有业务行为，替代验证为构建加 healthz 检查
  - 备注: pi(deepseek-flash) 实施，宿主复核：go vet/test/staticcheck 通过
- [√] 1.2 创建前端骨架：Vite + React + TS + Tailwind v4 + React Router + Zustand + Vitest + ESLint；开发服务器把 `/api` 和 `/ws` 代理到 `:8080`（保持同源）
  - 执行模式: AFK
  - 涉及文件: `frontend/*`
  - 完成标准: `pnpm build`、`pnpm test`、`pnpm typecheck`、`pnpm lint` 全部通过
  - 验证方式: `make web-test && cd frontend && pnpm build`
  - TDD: TDD-EXEMPT，原因：纯脚手架；替代验证为构建和示例测试通过
  - 备注: TypeScript 锁定 6.0.3（typescript-eslint 不支持 TS7）；pnpm-workspace 中绕过 minimumReleaseAge 的配置已在第 2 批要求移除
- [√] 1.3 创建 `deploy/docker-compose.dev.yml`（PostgreSQL 17）；接入 goose（迁移 embed 进二进制）和 sqlc；编写 V1 全部表的首个迁移（见 how.md 数据模型），包含 down 脚本；`make db-up`、`make migrate`、`make sqlc`。依赖 0.2、1.1
  - 执行模式: AFK
  - 涉及文件: `deploy/docker-compose.dev.yml`, `backend/migrations/00001_init.sql`, `backend/sqlc.yaml`, `backend/internal/platform/db/*`, `backend/internal/store/*`, `Makefile`
  - 完成标准: 在空库上 `migrate up` → `down` → `up` 全部成功；`sqlc generate` 不报错
  - 验证方式: `make db-up && make migrate && cd backend && go run ./cmd/server migrate down && go run ./cmd/server migrate up`
  - TDD: TDD-EXEMPT，原因：schema 定义，没有业务逻辑；替代验证为 up/down/up 往返

  - 备注: 迁移 down→up 往返通过（17 张业务表）；sqlc generate 通过
## 2. 匿名身份与实名验证
- [√] 2.1 RED：在 `identity` 中编写测试：首次 `POST /api/identity` 设置 `__Host-anon` Cookie，且数据库只存哈希；`/me` 返回昵称和头像种子，不含 ID；`reset` 后旧令牌返回 401；昵称格式为"形容词+动物#4位数"；同一 IP 每小时超过 10 次时返回 `rate_limited`（这里先用一个桩限流器）
  - 执行模式: AFK
  - 涉及文件: `backend/internal/identity/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration` 显示对应用例失败
- [√] 2.2 GREEN：实现 `identity` 服务、HTTP handler、`RequireIdentity` 中间件（同时校验身份是否被吊销）和 `access_logs` 写入。依赖 2.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/identity/*`, `backend/queries/identity.sql`, `backend/internal/api/routes.go`
  - 完成标准: 2.1 的测试全部通过
  - 验证方式: `make test && make test-integration`
  - 备注: 换身份接口绕过每 IP 创建上限、lastSeen 缓存无上限两项问题在第 3 批修正
- [√] 2.3 RED：在 `verify` 中编写测试：`VERIFICATION_MODE=required` 时，未验证身份调用受保护接口返回 `verification_required`；验证码 5 分钟过期，错误 5 次后失效；手机号以密文和 HMAC 形式存储，脱敏格式正确；同一手机号 60 秒内重发返回 `rate_limited`；同一手机号再次绑定时复用同一个 `verified_accounts` 记录
  - 执行模式: AFK
  - 涉及文件: `backend/internal/verify/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 2.4 GREEN：实现 `verify` 服务、`SMSProvider` 接口和 `console` 实现（只在开发环境把验证码打到日志里，生产环境禁止使用这个实现）、AES-GCM 和 HMAC 工具、`RequireVerified` 中间件。依赖 2.3
  - 执行模式: AFK
  - 涉及文件: `backend/internal/verify/*`, `backend/queries/verify.sql`
  - 完成标准: 2.3 的测试全部通过；`APP_ENV=production` 时配置 `SMS_PROVIDER=console` 会导致启动失败
  - 验证方式: `make test && make test-integration`
  - 备注: 验证码尝试次数存在并发绕过、off 模式仍开放验证接口两项问题在第 3 批修正
- [√] 2.5 REFACTOR+VERIFY：整理身份和验证模块，复跑全部测试。依赖 2.4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/identity/*`, `backend/internal/verify/*`
  - 完成标准: 行为不变，测试全部通过
  - 验证方式: `make test && make test-integration`

## 3. 实时网关
- [√] 3.1 RED：在 `realtime` 中编写测试：没有 Cookie 或 Origin 不在白名单时握手被拒绝；握手成功后收到 `hello`；`ping` 收到 `pong`；超过 16KB 的帧会断开连接；60 秒没有任何帧会断开连接（测试中注入较短的超时）；写缓冲满的慢客户端会被断开；对未知 `t` 返回 `invalid_request`；`Hub.Kick(identityID)` 会推送 `kicked` 并关闭连接
  - 执行模式: AFK
  - 涉及文件: `backend/internal/realtime/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `cd backend && go test ./internal/realtime/...`
- [√] 3.2 GREEN：实现协议编解码、连接读写泵、`Hub`/`Broker`/`Presence` 接口及其内存实现、按消息类型分发的路由表（各业务模块注册自己的处理函数）。依赖 3.1、2.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/realtime/*`
  - 完成标准: 3.1 的测试全部通过；`go test -race` 通过
  - 验证方式: `cd backend && go test -race ./internal/realtime/...`
  - 备注: 慢客户端同步关闭阻塞广播方、Presence 多连接未做引用计数两项问题在第 4 批修正
- [√] 3.3 REFACTOR+VERIFY。依赖 3.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/realtime/*`
  - 完成标准: 测试全部通过，`-race` 无报告
  - 验证方式: `cd backend && go test -race ./internal/realtime/...`

## 4. 会话与消息管道
- [√] 4.1 RED：在 `conversation` 中编写测试：成员发送 `msg.send` 后落库，先回 `ack`，再广播给其他在线成员；重复的 `clientMsgId` 不会重复落库，返回同一个 messageId；非成员发送返回 `not_member`；已结束的会话返回 `conversation_ended`；超过 1000 字符返回 `invalid_request`；历史分页接口支持 `before`/`after`；阅后即焚会话在 `msg.read` 后 10 秒推送 `msg.burned`，历史接口也不再返回该消息；屏蔽关系下，被屏蔽者的消息不会推送给屏蔽者，历史中也不出现；对外载荷不包含 `identity_id`
  - 执行模式: AFK
  - 涉及文件: `backend/internal/conversation/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 4.2 GREEN：实现会话和成员仓储、发送管道（预留 moderation 钩子：限流和敏感词先用空实现）、`msg.read`、焚毁调度器、`GET /api/conversations`、`GET /api/conversations/{id}/messages`。依赖 4.1、3.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/conversation/*`, `backend/queries/conversation.sql`, `backend/internal/api/routes.go`
  - 完成标准: 4.1 的测试全部通过
  - 验证方式: `make test && make test-integration`
  - 备注: 附带修复日志中间件不支持 Hijack 导致 WS 无法升级；焚毁扫描缺索引、历史分页屏蔽过滤在 SQL 外两项在第 5 批修正
- [√] 4.3 RED → GREEN：在 `jobs` 中实现保留期清理。先写测试：超过 7 天的消息被分批删除；已结束且消息为空的会话被清理；过期的漂流瓶和私密链接被清理；180 天前的 access_logs 被清理；未过期的数据不受影响（测试中注入时钟）。再实现调度器（每 10 分钟执行，周期可配置）。依赖 4.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/jobs/*`, `backend/queries/retention.sql`
  - 完成标准: 测试先失败后通过；清理按每批 5,000 行执行
  - 验证方式: `make test-integration`
- [√] 4.4 REFACTOR+VERIFY。依赖 4.3
  - 执行模式: AFK
  - 涉及文件: `backend/internal/conversation/*`, `backend/internal/jobs/*`
  - 完成标准: 测试全部通过
  - 验证方式: `make test && make test-integration`

## 5. 内容安全
- [√] 5.1 RED → GREEN → VERIFY：敏感词过滤。测试覆盖：全角转半角、大小写统一、去除插入的空格和符号后能命中；`block` 级返回拦截，`mask` 级替换为 `*`；10 万条词库加 1000 字消息的过滤耗时 < 1ms（benchmark）。实现基于 AC 自动机，词库从 `WORDLIST_PATH` 文件加载，仓库内只提交一个示例词库。依赖 1.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/moderation/wordfilter*.go`, `backend/internal/moderation/testdata/*`
  - 完成标准: 测试和 benchmark 达标
  - 验证方式: `cd backend && go test -bench=. ./internal/moderation/...`
  - 备注: benchmark 10 万词 + 1000 字约 11.9µs/次
- [√] 5.2 RED → GREEN → VERIFY：限流。测试覆盖令牌桶和固定窗口两种语义、按 key 隔离、过期 key 会被回收。实现 `RateLimiter` 接口及内存版本，并接入 how.md 频率限制表中的所有限流点（身份创建、消息、匹配、漂流瓶、私密链接、密码错误、短信、管理员登录）。依赖 1.1；接入部分依赖 2、4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/moderation/ratelimit*.go`，以及各限流点的调用处
  - 完成标准: 单元测试通过；集成测试中超限请求返回 `rate_limited`
  - 验证方式: `make test && make test-integration`
- [√] 5.3 RED：编写屏蔽、举报、封禁的测试：屏蔽后双方互相不可见；举报时生成快照（被举报消息及前后各 10 条），会话被清理后快照仍在；封禁身份、IP 或实名账号后，握手和 REST 请求都返回 `banned`，在线连接收到 `kicked`；换身份后 IP 封禁依然生效；临时封禁到期后自动失效
  - 执行模式: AFK
  - 涉及文件: `backend/internal/moderation/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 5.4 GREEN：实现屏蔽 API、举报 API、`BanChecker`（内存缓存 30 秒，封禁变更时主动失效），并把敏感词和限流接入消息管道。依赖 5.1、5.2、5.3、4.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/moderation/*`, `backend/queries/moderation.sql`, `backend/internal/conversation/pipeline.go`
  - 完成标准: 5.3 的测试和 4.1 的测试全部通过
  - 验证方式: `make test && make test-integration`

  - 备注: 封禁缓存无上限在第 6 批修正；IP 封禁目前按单个地址精确匹配（IPv6 用户可在 /64 内换地址），列为已知限制，V2 风控时再评估按网段封禁
## 6. A 随机匹配
- [√] 6.1 RED：编写匹配器单元测试：按先进先出配对；有屏蔽关系的组合不配对；排除最近 3 个聊过的对象，只剩这些人时放宽；`match.cancel` 后出队；同一身份重复排队时以最后一次为准。再编写 WS 集成测试：两个客户端都收到 `match.found` 并能互发消息；`match.next` 后对方收到 `peer_skipped`；断线 30 秒内（测试中注入较短的宽限期）重连能恢复会话，超时后对方收到 `peer_left`
  - 执行模式: AFK
  - 涉及文件: `backend/internal/match/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `cd backend && go test ./internal/match/...` 和 `make test-integration`
- [√] 6.2 GREEN：实现 `Matcher` 接口及其内存实现（单 goroutine 加通道）、WS 处理函数、断线宽限计时器、创建会话时写入 `kind=match`。依赖 6.1、4.2、5.4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/match/*`
  - 完成标准: 6.1 的测试全部通过，`-race` 无报告
  - 验证方式: `cd backend && go test -race ./internal/match/... && make test-integration`

  - 备注: 匹配器"等待超时放宽"只在有人入队时才重新评估、配对检查逐对查库（O(n²) 次 SQL）两项在第 7 批修正
## 7. B 聊天室
- [√] 7.1 RED：编写测试：`GET /api/rooms` 返回种子房间和在线人数；`room.join` 后会推送 `room.presence`，并能收发消息；达到上限时返回 `room_full`；`room.leave` 和断线都会让在线人数减少；在线人数推送合并为每秒最多一次；历史分页最多回溯到 7 天前；房间内发言间隔 ≥ 1 秒
  - 执行模式: AFK
  - 涉及文件: `backend/internal/room/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 7.2 GREEN：实现 `room` 模块；用迁移写入官方房间种子数据（闲聊大厅、深夜树洞、游戏、学习打卡、情感、职场，共 6 个）；加入房间时 upsert 成员记录并复用已有的 memberId。依赖 7.1、4.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/room/*`, `backend/migrations/00003_seed_rooms.sql`（00002 已被焚毁索引占用）, `backend/queries/room.sql`
  - 完成标准: 7.1 的测试全部通过
  - 验证方式: `make test && make test-integration`

  - 备注: 满员检查与加入非原子（并发可超员）在第 7 批修正
## 8. C 漂流瓶
- [√] 8.1 RED：编写测试：投放内容超过 500 字或当天超过 10 次时被拒绝；捞到的瓶子满足不是自己的、未过期、没捞过、无屏蔽关系这几个条件；被捞起的瓶子 10 分钟内没有处理会自动回到池中；扔回后别人可以捞到；回复后创建 `kind=bottle` 的会话，投放者在线时收到 `bottle.reply`，`/mine` 中的未读数正确；池中没有瓶子时返回 `empty_sea`；并发捞取不会让两个人拿到同一个瓶子
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bottle/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 8.2 GREEN：实现 `bottle` 模块，捞取使用 `SELECT ... FOR UPDATE SKIP LOCKED` 加随机偏移。依赖 8.1、4.2、5.4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/bottle/*`, `backend/queries/bottle.sql`
  - 完成标准: 8.1 的测试全部通过
  - 验证方式: `make test && make test-integration`

  - 备注: 新增漂流瓶举报接口（how.md 已补充）
## 9. D 私密链接
- [√] 9.1 RED：编写测试：创建链接时 TTL 超出 1–168 小时范围会被拒绝；令牌只以哈希形式存储；第二人加入成功，第三人返回 `link_full`；创建者本人打开链接是幂等的；密码错误返回 `bad_password`，连续错误触发限流；过期后返回 `link_expired`；销毁后双方收到 `conv.ended(destroyed)`，历史接口返回 `conversation_ended`；阅后即焚行为与 4.1 一致
  - 执行模式: AFK
  - 涉及文件: `backend/internal/privatechat/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 9.2 GREEN：实现 `privatechat` 模块，密码使用 bcrypt。依赖 9.1、4.2、5.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/privatechat/*`, `backend/queries/privatechat.sql`
  - 完成标准: 9.1 的测试全部通过
  - 验证方式: `make test && make test-integration`

## 10. 管理后台（后端）
- [√] 10.1 RED：编写测试：`server admin create` 创建的管理员可以登录，密码错误 5 次后被限流；未登录访问 `/api/admin/*` 返回 401；举报列表和详情包含快照和脱敏手机号；处理举报时选择 `ban` 会创建封禁并踢掉在线连接；撤销封禁后立即恢复访问；每个写操作都生成一条 `admin_audit_logs`；`/stats` 的返回字段齐全；身份 Cookie 无法访问管理接口
  - 执行模式: AFK
  - 涉及文件: `backend/internal/admin/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test-integration`
- [√] 10.2 GREEN：实现 `admin` 模块（会话、举报、封禁、统计、审计）和 CLI 子命令（交互式输入密码，不允许通过命令行参数传入）；支持可选的 `ADMIN_IP_ALLOWLIST`。依赖 10.1、5.4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/admin/*`, `backend/cmd/server/main.go`, `backend/queries/admin.sql`
  - 完成标准: 10.1 的测试全部通过
  - 验证方式: `make test && make test-integration`
- [√] 10.3 REFACTOR+VERIFY：后端 V1 全量检查。依赖 6–10
  - 执行模式: AFK
  - 涉及文件: `backend/**`
  - 完成标准: 单元测试、集成测试、`-race`、`go vet`、`staticcheck` 全部通过
  - 验证方式: `make test && make test-integration && cd backend && go test -race ./... && go vet ./... && staticcheck ./...`

  - 备注: 宿主复核发现 TestReportCreatesSnapshot 在 -race 下 15 次失败 2 次；govulncheck 报告 Go 1.27.1 标准库 7 个可达漏洞（1.27.3 修复）——两项在第 9 批修正
## 11. 前端
- [√] 11.1 RED → GREEN → VERIFY：WS 客户端 `lib/ws`。测试覆盖：断线后按指数退避重连（1s、2s、4s…，最长 30s，带抖动）；发件箱在收到 `ack` 前显示"发送中"，超时显示"失败"并可以重试；重连后对每个已订阅会话按最后一条消息 ID 补拉历史，并按 messageId 去重；收到 `kicked` 后不再重连；每 25 秒发送一次 ping。依赖 1.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/lib/ws/*`, `frontend/src/lib/api/*`
  - 完成标准: Vitest 测试先失败后通过
  - 验证方式: `make web-test`
- [√] 11.2 应用壳：身份初始化（`POST /api/identity`）、顶部栏（昵称、头像、换身份）、首页的四个入口、实名验证弹窗（`verification_required` 时弹出）、全局错误提示（错误码映射为中文）、移动端适配。头像由 `avatarSeed` 在本地生成（确定性的 SVG 色块图案，不依赖外部服务）。依赖 11.1、2.4
  - 执行模式: AFK
  - 涉及文件: `frontend/src/app/*`, `frontend/src/features/home/*`, `frontend/src/components/*`, `frontend/src/stores/identity.ts`
  - 完成标准: 页面可以正常渲染；错误码映射和头像生成函数有单元测试
  - 验证方式: `make web-test`；E2E 测试见 11.8
  - TDD: 纯函数部分按 TDD；页面部分 TDD-EXEMPT，原因：UI 布局，替代验证为 11.8 的 E2E
- [√] 11.3 共享聊天组件：消息列表（虚拟滚动、上拉加载更多、发送状态、阅后即焚倒计时并在焚毁后移除）、输入框（字数限制、表情选择器、输入中状态节流）、举报弹窗（勾选消息、选择原因）、屏蔽确认。依赖 11.1
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/chat/*`
  - 完成标准: 组件测试覆盖发送状态切换、焚毁后移除、举报时勾选的消息 ID 正确传给接口
  - 验证方式: `make web-test`
  - 备注: 未实现虚拟滚动，改为每会话内存上限 500 条（宿主同意的偏离）
- [√] 11.4 A 随机匹配页面：开始匹配和取消、等待动画、匹配成功后进入聊天、下一个和离开、对方离开的提示。依赖 11.3、6.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/match/*`
  - 完成标准: 页面与 WS 事件联通
  - 验证方式: 11.8 的 E2E 流程 A
  - TDD: TDD-EXEMPT，原因：UI 编排；替代验证为 E2E
- [√] 11.5 B 聊天室页面：房间列表（在线人数）、房间聊天、在线人数实时更新、满员提示。依赖 11.3、7.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/rooms/*`
  - 完成标准: 页面与接口联通
  - 验证方式: 11.8 的 E2E 流程 B
  - TDD: TDD-EXEMPT，原因同 11.4
  - 备注: 房间成员屏蔽/举报入口为顶部"成员选择"下拉，而非逐条消息菜单（已接受的偏离）
- [√] 11.6 C 漂流瓶页面：投瓶、捞瓶（扔回或回复）、我的瓶子（未读角标）、漂流瓶会话页。依赖 11.3、8.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/bottles/*`
  - 完成标准: 页面与接口联通
  - 验证方式: 11.8 的 E2E 流程 C
  - TDD: TDD-EXEMPT，原因同 11.4
  - 备注: 后端未提供每日剩余次数，页面不显示
- [√] 11.7 D 私密链接页面：创建表单（密码、有效期、阅后即焚）、复制链接、`/p/:token` 加入页（输入密码）、私密聊天页（焚毁提示、销毁按钮、"对方可截屏"的提示）。依赖 11.3、9.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/private/*`
  - 完成标准: 页面与接口联通
  - 验证方式: 11.8 的 E2E 流程 D
  - TDD: TDD-EXEMPT，原因同 11.4
- [√] 11.8 管理后台页面（`/admin`，懒加载）：登录、举报列表和详情（快照）、处理（驳回或封禁）、封禁列表和撤销、统计卡片、审计日志。依赖 11.2、10.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/admin/*`
  - 完成标准: 页面与接口联通
  - 验证方式: 11.9 的 E2E 流程 E
  - TDD: TDD-EXEMPT，原因同 11.4
- [√] 11.9 Playwright E2E：流程 A（两个上下文匹配、聊天、跳过）、流程 B（两人进同一房间、在线数为 2、互相收到消息）、流程 C（甲投瓶、乙捞起回复、甲看到回复）、流程 D（甲创建带密码的阅后即焚链接、乙加入、消息读后消失、第三人加入失败、销毁）、流程 E（举报、管理员封禁、被封禁方被踢出且无法重连）。依赖 11.4–11.8
  - 执行模式: AFK
  - 涉及文件: `frontend/e2e/*`, `frontend/playwright.config.ts`, `Makefile`
  - 完成标准: 五条流程在开发环境全部通过
  - 验证方式: `make e2e`

  - 备注: @playwright/test 1.64.1；宿主独立运行 make e2e 5/5 通过；pi 报告 --repeat-each=3 15/15 通过；新增 RATE_LIMIT_MULTIPLIER（生产强制为 1）与 cmd/e2eseed（仅 APP_ENV=test）
## 12. 生产部署
- [√] 12.1 编写 `backend.Dockerfile`（多阶段构建，运行时使用 distroless 镜像，非 root 用户）、`web.Dockerfile`（先构建前端，再使用 Caddy 镜像）、`Caddyfile`（自动 HTTPS、`/api` 和 `/ws` 反向代理、安全响应头、静态资源长缓存、SPA 回退到 index.html、不对外暴露 `/metrics`）、`docker-compose.yml`（postgres 带健康检查和数据卷、backend 依赖 postgres 健康、web、backup）、`.env.example`（所有配置项都有注释，不含真实值）、备份脚本（每天 03:00 执行 `pg_dump -Fc`，保留 7 份）。依赖 11.9
  - 执行模式: AFK
  - 涉及文件: `deploy/*`
  - 完成标准: 本机执行 `docker compose up -d --build` 后所有服务健康；`https://localhost` 可以访问（使用 Caddy 本地 CA）；`/healthz` 返回 200；响应头包含 CSP 和 HSTS；手动触发一次备份能生成 dump 文件，并且可以用 `pg_restore` 恢复到一个新库
  - 验证方式: `cd deploy && docker compose up -d --build && docker compose ps`；`curl -kI https://localhost`；执行备份和恢复演练
  - TDD: TDD-EXEMPT，原因：部署配置；替代验证为容器健康检查、响应头检查和备份恢复演练
  - 备注: 部署文件已完成，并通过静态检查（compose YAML 解析、caddy adapt、在本机执行构建命令、healthcheck 子命令）；docker build、compose up、HTTPS、备份恢复演练需要先安装 Docker；S1/S2 已在第 13 批修复（实名关闭时生产可以启动；新增 UPSTREAM_TRUSTED_PROXIES，默认不信任任何上游）
  - 备注: 2026-11-06 在 cch_hk（Debian 13，Docker 29.8）实际验证通过：两个镜像构建成功（backend 24.6MB、web 92.2MB），compose 以生产配置启动后 backend 和 postgres 健康检查均通过；经 Caddy 访问时安全响应头、静态资源缓存、/metrics 返回 404、SPA 回退、Origin 拦截、Cookie 属性、WS 握手 101 / 非法来源 403 均符合预期；X-Forwarded-For 能透传到 access_logs；备份→pg_restore 恢复演练的数据与源库一致。因 80/443 被 nginx-proxy-manager 占用，web 只绑定 127.0.0.1:18088（服务器本地的 docker-compose.override.yml）；尚未配置域名和 HTTPS
- [-] 12.2 在 compose 环境中运行 E2E（`E2E_BASE_URL=https://localhost`）。依赖 12.1
  - 执行模式: AFK
  - 涉及文件: `frontend/playwright.config.ts`, `Makefile`
  - 完成标准: 五条 E2E 流程在 compose 环境中全部通过
  - 验证方式: `make e2e-compose`
  - 备注: 依赖 Docker，暂缓
- [?] 12.3 上线前人工事项清单：域名和服务器；ICP 备案和公安联网备案；互联网信息服务安全评估；用户协议、隐私政策、社区规范文本（需要如实说明 7 天保留、阅后即焚语义和日志留存）；生产环境敏感词库；短信服务商开通、签名和模板审核，并填写 `SMS_*` 密钥；生成生产环境的 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY` 并离线备份；创建首个管理员；配置异地备份。依赖 12.1
  - 执行模式: HITL（需要用户、法务或运营处理）
  - 涉及文件: `deploy/.env`（只放在服务器上，不提交）、`frontend/src/features/legal/*`（文本由用户提供）
  - 完成标准: 清单逐项勾选完成，或明确标注为"暂不适用"（例如面向海外上线时）
  - 验证方式: 用户确认

  - 备注: 人工事项，等待用户处理（备案、评估、法律文本、词库、短信服务商、密钥、域名）；另外短信服务商的 SMSProvider 实现需要在选定服务商后补做
## 13. V1 安全检查
- [√] 13.1 执行安全检查：`govulncheck ./...`；`pnpm audit --prod`；人工核对以下各项——Cookie 属性、Origin 校验、所有令牌是否只存哈希、对外载荷中没有 `identity_id`、日志中没有手机号、令牌和密码明文、请求体和帧大小限制、所有限流点都已接入、管理接口的鉴权和审计、仓库中没有 `.env` 和密钥、`console` 短信实现在生产环境被禁止、CSP 已生效
  - 执行模式: AFK
  - 涉及文件: V1 全部改动文件
  - 完成标准: 没有高危漏洞；人工核对项全部满足；发现的问题已修复并复测
  - 验证方式: `cd backend && govulncheck ./...`；`cd frontend && pnpm audit --prod`；`git ls-files | rg -i '\.env$|secret|\.pem$'` 无输出

  - 备注: govulncheck 未发现可达漏洞，pnpm audit 未发现漏洞，14 项人工核对均附有证据
## 14. V1 知识库与验收
- [√] 14.1 创建知识库：`helloagents/main/project.md`（技术栈、目录、常用命令）、`wiki/overview.md`（架构图和模块职责）、`wiki/protocol.md`（REST、WS 协议和错误码）、`wiki/data.md`（数据模型和保留期）、`wiki/glossary.md`（how.md 中的领域语言表）、`wiki/deploy.md`（部署、备份、回滚、上线清单），内容以实际代码为准
  - 执行模式: AFK
  - 涉及文件: `helloagents/main/project.md`, `helloagents/main/wiki/*`
  - 完成标准: 文档与代码一致（路由、协议类型、表结构都能在代码中找到）
  - 验证方式: 只读核对，对照 `backend/internal/api/routes.go`、`realtime` 协议常量和 migrations
  - 备注: protocol.md 与 routes.go 逐条核对（36/36）
- [ ] 14.2 V1 最终验收：完整执行全部测试、E2E、compose 部署和安全检查
  - 执行模式: AFK
  - 涉及文件: 无新增
  - 完成标准: 全部命令通过
  - 验证方式: `make test && make test-integration && make web-test && make e2e && make e2e-compose`
- [ ] 14.3 用户验收 V1，确认后进入 V2
  - 执行模式: HITL
  - 涉及文件: 无
  - 完成标准: 用户确认
  - 验证方式: 用户确认

---

# V2：规模化与体验增强

## 15. Redis 与多实例
- [ ] 15.1 RED：多实例集成测试。使用两个后端实例加 Redis（在 `docker-compose.dev.yml` 中增加 redis），测试覆盖：连到实例 1 的用户和连到实例 2 的用户可以匹配并聊天；房间在线人数在两个实例上一致；限流按全局计数；在实例 1 上封禁，连在实例 2 上的连接也会被踢出
  - 执行模式: AFK
  - 涉及文件: `backend/internal/redisx/*_test.go`, `deploy/docker-compose.dev.yml`
  - 完成标准: 在 `REALTIME_BACKEND=redis` 下测试失败
  - 验证方式: `make test-integration-redis`
- [ ] 15.2 GREEN：实现 `redisx` 中的 Broker（基于 Pub/Sub，按会话 ID 分频道，在节点本地扇出）、Presence（有序集合加心跳过期）、Matcher（Lua 脚本原子配对，屏蔽和最近聊过的过滤在 Lua 外做二次校验，失败时重新入队）、RateLimiter（Lua 令牌桶）；增加 `REALTIME_BACKEND` 开关；生产 compose 增加 redis 服务。依赖 15.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/redisx/*`, `backend/internal/config/*`, `deploy/docker-compose.yml`
  - 完成标准: 15.1 的测试通过；`REALTIME_BACKEND=memory` 下原有的全部测试仍然通过
  - 验证方式: `make test && make test-integration && make test-integration-redis`

## 16. 匹配偏好
- [ ] 16.1 RED → GREEN → VERIFY：`match.start` 支持 `{tags[], lang}`（使用预设标签表，最多选 3 个）。测试覆盖：标签有交集的用户优先配对；等待超过 10 秒自动回退为随机匹配；内存和 Redis 两种实现行为一致。前端增加标签选择器。依赖 15.2
  - 执行模式: AFK
  - 涉及文件: `backend/internal/match/*`, `backend/internal/redisx/matcher*`, `frontend/src/features/match/*`
  - 完成标准: 测试先失败后通过；E2E 流程 A 仍然通过
  - 验证方式: `make test && make test-integration-redis && make e2e`

## 17. 图片消息
- [ ] 17.1 RED：编写测试：只允许 jpg/png/webp/gif 且 ≤5MB，按文件头判断类型而不是扩展名；长边超过 2048 时缩放；重新编码后不含 EXIF；`Moderator` 返回拒绝时消息不投递；审核中时接收方看到占位；图片只能通过 5 分钟有效的签名 URL 访问，并且只有会话成员能拿到；7 天后对象和记录都被删除
  - 执行模式: AFK
  - 涉及文件: `backend/internal/media/*_test.go`
  - 完成标准: 测试失败，原因是功能未实现
  - 验证方式: `make test && make test-integration`
- [ ] 17.2 GREEN：实现 `media` 模块（MinIO 客户端、上传接口 `POST /api/media`、重新编码、`Moderator` 接口及 `noop`/`manual` 实现，第三方审核接口留给 17.4）；消息管道支持 `kind=image`；compose 增加 minio 服务；保留期任务清理图片对象。依赖 17.1
  - 执行模式: AFK
  - 涉及文件: `backend/internal/media/*`, `backend/migrations/00003_media.sql`, `backend/internal/conversation/pipeline.go`, `backend/internal/jobs/*`, `deploy/*`
  - 完成标准: 17.1 的测试全部通过
  - 验证方式: `make test && make test-integration`
- [ ] 17.3 前端支持图片发送：选择、压缩预览、上传进度、审核中占位、点击查看大图。依赖 17.2
  - 执行模式: AFK
  - 涉及文件: `frontend/src/components/chat/*`
  - 完成标准: E2E 增加流程"发送图片后对方能看到"并通过
  - 验证方式: `make e2e`
- [ ] 17.4 接入第三方内容审核服务（服务商由用户选择并提供密钥），实现 `Moderator`
  - 执行模式: HITL（需要选择服务商并提供凭据）
  - 涉及文件: `backend/internal/media/moderator_<vendor>.go`
  - 完成标准: 使用服务商提供的沙箱或测试样本验证通过和拒绝两条路径
  - 验证方式: 集成测试（跑沙箱环境）

## 18. 用户自建房间
- [ ] 18.1 RED → GREEN：测试覆盖：创建公开或私密房间（每个身份最多同时拥有 3 个；`VERIFICATION_MODE≠off` 时要求已实名）；私密房间需要邀请令牌加密码才能加入；房主可以踢人（被踢者 24 小时内不能再加入）和禁言（禁言期间发送返回 `muted`）；连续 7 天没有消息的房间自动归档；房间名称和描述经过敏感词过滤。依赖 7.2、5.4
  - 执行模式: AFK
  - 涉及文件: `backend/internal/room/*`, `backend/migrations/00004_user_rooms.sql`, `backend/internal/jobs/*`
  - 完成标准: 测试先失败后通过
  - 验证方式: `make test && make test-integration`
- [ ] 18.2 前端：创建房间表单、邀请链接、房主管理菜单、"我的房间"。依赖 18.1
  - 执行模式: AFK
  - 涉及文件: `frontend/src/features/rooms/*`
  - 完成标准: E2E 增加流程"创建私密房间、邀请、踢人"并通过
  - 验证方式: `make e2e`

## 19. 人机验证
- [ ] 19.1 RED → GREEN：`Captcha` 接口（实现 `noop` 和服务商实现，服务商由用户在 Cloudflare Turnstile、极验、腾讯验证码中选择）。测试覆盖：开启后创建身份和发送短信时必须携带有效的 `captchaToken`；同一身份 10 分钟内触发 3 次 `rate_limited` 后，下一次关键操作要求验证（返回 `captcha_required`）。前端接入验证组件。依赖 2.4、5.2
  - 执行模式: AFK（服务商凭据部分为 HITL）
  - 涉及文件: `backend/internal/captcha/*`, `frontend/src/components/captcha/*`
  - 完成标准: 测试先失败后通过；`CAPTCHA_PROVIDER=noop` 时原有 E2E 全部通过
  - 验证方式: `make test && make test-integration && make e2e`

## 20. 管理增强
- [ ] 20.1 RED → GREEN：管理员 TOTP。可以绑定和确认；绑定后登录必须提供 TOTP；TOTP 密钥加密存储；允许 ±1 个时间步偏差；同一个验证码不能重复使用
  - 执行模式: AFK
  - 涉及文件: `backend/internal/admin/*`, `frontend/src/features/admin/*`
  - 完成标准: 测试先失败后通过
  - 验证方式: `make test && make test-integration`
- [ ] 20.2 RED → GREEN：敏感词在线管理。新增 `sensitive_words` 表和增删改查接口；修改后 5 秒内所有实例生效（Redis 模式下通过 Pub/Sub 通知各实例热更新，内存模式下直接替换）；文件词库和数据库词库合并使用
  - 执行模式: AFK
  - 涉及文件: `backend/internal/moderation/*`, `backend/internal/admin/*`, `backend/migrations/00005_sensitive_words.sql`, `frontend/src/features/admin/*`
  - 完成标准: 测试先失败后通过
  - 验证方式: `make test && make test-integration && make test-integration-redis`
- [ ] 20.3 房间治理（管理员可以归档房间、在任意房间禁言成员）和审计日志查询页面
  - 执行模式: AFK
  - 涉及文件: `backend/internal/admin/*`, `frontend/src/features/admin/*`
  - 完成标准: 集成测试覆盖归档后无法发送消息
  - 验证方式: `make test-integration`

## 21. PWA 与 Web Push
- [ ] 21.1 PWA：manifest、Service Worker（只缓存应用壳，不缓存 API 响应和消息内容）、安装提示
  - 执行模式: AFK
  - 涉及文件: `frontend/public/manifest.webmanifest`, `frontend/src/sw.ts`, `frontend/vite.config.ts`
  - 完成标准: Chrome DevTools 的 Application 面板显示可安装；离线时显示应用壳和"网络已断开"的提示
  - 验证方式: Playwright 离线模式测试和人工 DevTools 检查
  - TDD: TDD-EXEMPT，原因：平台配置；替代验证如上
- [ ] 21.2 RED → GREEN：Web Push。VAPID 密钥来自环境变量；订阅和取消订阅接口；漂流瓶收到新回复、私密会话收到新消息、且接收方当前不在线时推送；推送内容不包含消息正文（只显示"你有一条新消息"）；收到 410 时删除失效的订阅
  - 执行模式: AFK
  - 涉及文件: `backend/internal/push/*`, `backend/migrations/00006_push.sql`, `frontend/src/lib/push/*`
  - 完成标准: 测试先失败后通过
  - 验证方式: `make test && make test-integration`

## 22. 可观测性
- [ ] 22.1 Prometheus 指标：在线连接数、每秒消息数、发送管道耗时直方图、匹配队列长度、匹配等待时间、限流命中次数、清理任务的行数和耗时、数据库连接池状态。`/metrics` 只监听内部端口；compose 增加 `monitoring` profile（Prometheus + Grafana，附带一个预置面板）
  - 执行模式: AFK
  - 涉及文件: `backend/internal/metrics/*`, `deploy/monitoring/*`, `deploy/docker-compose.yml`
  - 完成标准: 指标测试断言关键指标会递增；`docker compose --profile monitoring up` 后面板有数据；从外部访问 `/metrics` 返回 404
  - 验证方式: `make test`；`curl -k https://localhost/metrics` 返回 404；人工查看 Grafana
- [ ] 22.2 压测：使用 k6 脚本模拟 5,000 个 WebSocket 连接，其中 50 个房间、每秒 1,000 条消息，持续 10 分钟。依赖 15.2、22.1
  - 执行模式: AFK
  - 涉及文件: `loadtest/*.js`
  - 完成标准: 消息 P95 延迟 ≤ 300ms；没有连接异常断开；内存平稳没有持续增长
  - 验证方式: `k6 run loadtest/ws.js` 的报告和 Grafana 截图

## 23. V2 安全检查与收尾
- [ ] 23.1 V2 安全检查：上传接口（类型、大小、解压炸弹、SSRF）、签名 URL 的有效期和权限、Redis 不对公网暴露且有密码、MinIO bucket 为私有、TOTP 密钥加密、推送内容不含正文、`govulncheck` 和 `pnpm audit`
  - 执行模式: AFK
  - 涉及文件: V2 全部改动文件
  - 完成标准: 没有高危问题；人工核对项全部满足
  - 验证方式: `cd backend && govulncheck ./...`；`cd frontend && pnpm audit --prod`；人工核对
- [ ] 23.2 同步知识库：更新 overview（多实例架构）、protocol（新增的类型和错误码）、data（V2 新增表、Redis 键、对象存储）、deploy（redis、minio、monitoring）、glossary（新增房主、禁言、归档等术语）
  - 执行模式: AFK
  - 涉及文件: `helloagents/main/project.md`, `helloagents/main/wiki/*`
  - 完成标准: 文档与代码一致
  - 验证方式: 只读核对
- [ ] 23.3 V2 最终验收：全部测试、Redis 多实例测试、E2E、compose 部署、压测报告
  - 执行模式: AFK
  - 涉及文件: 无新增
  - 完成标准: 全部通过
  - 验证方式: `make test && make test-integration && make test-integration-redis && make web-test && make e2e && make e2e-compose`
