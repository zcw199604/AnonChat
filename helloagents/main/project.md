# AnonChat 项目说明

> 以代码为准。最后同步：外站匹配接入与用户媒体（202611131925）。

## 技术栈

- 后端：Go 1.27（`go.mod` 声明 `go 1.27`，`toolchain go1.27.3`）；标准库 `net/http`（`ServeMux` 方法+路径模式，无 Web 框架）。
- WebSocket：本站与 chat2019 使用 `github.com/coder/websocket`；晚风移植包使用 `github.com/gorilla/websocket v1.5.3`，新增 `github.com/google/uuid v1.6.0` 用于晚风和探测工具。`golang.org/x/sync` 仅提升为直接依赖，未升级版本。
- 数据库：PostgreSQL 16/17 + `github.com/jackc/pgx/v5`（pgxpool）；SQL 代码生成 sqlc；迁移 goose（SQL 嵌入二进制）。
- 加密/密码：`crypto/aes`(AES-256-GCM)、`crypto/hmac`(HMAC-SHA256)、`golang.org/x/crypto/bcrypt`、项目内 `internal/platform/secretbox`（AES-256-GCM，晚风绑定 token）、`golang.org/x/term`（CLI 密码输入）。
- 日志：`log/slog`（JSON）。
- 前端：React 19 + TypeScript + Vite；状态 Zustand；路由 React Router；样式 Tailwind CSS v4；字体 `@fontsource-variable/inter@5.3.0`（打包进静态资源）；测试 Vitest + Testing Library；E2E Playwright（仅 chromium）。
- 工具（`go get -tool` 声明，`go tool` 调用）：sqlc、staticcheck。

## 目录结构

```
backend/
├── cmd/server/        # server：serve | migrate up|down | admin create | healthcheck
├── cmd/e2eseed/       # 仅 APP_ENV=test：从 stdin 读密码创建 E2E 管理员与可用激活码（--out 写 .seed.json）
├── cmd/probe-external/ # 运维人工外站探测；不连接数据库，私有状态文件复用晚风账号
├── migrations/        # goose SQL（embed），含 Down
├── queries/           # sqlc 查询 SQL
└── internal/
    ├── api/           # 路由与中间件装配
    ├── config/        # 环境变量加载与校验
    ├── platform/db/   # pgxpool 连接池
    ├── platform/httpx/# JSON 响应、错误码、中间件、ClientIP、Origin
    ├── store/         # sqlc 生成代码
    ├── identity/      # 匿名身份：签发/校验/重置、昵称、Cookie、中间件
    ├── verify/        # 手机号实名：验证码、AES-GCM/HMAC、SMSProvider、中间件
    ├── realtime/      # WS 网关：握手、Hub/Broker/Presence、协议、路由分发
    ├── conversation/  # 会话/成员/消息、发送管道、阅后即焚、历史、屏蔽
    ├── match/         # 随机匹配（A）
    ├── external/      # 外站设置/绑定/影子身份/Bridge/地址守卫及 chat2019、wanfeng 客户端
    ├── media/         # 媒体库、用户上传和外站媒体；MIME嗅探、存储与访问控制
    ├── room/          # 聊天室（B）
    ├── bottle/        # 漂流瓶（C）
    ├── privatechat/   # 私密链接（D）
    ├── moderation/    # 限流、敏感词(AC)、屏蔽/举报/封禁
    ├── admin/         # 管理后台（会话/举报/封禁/统计/审计/按匿名编号查身份）
    ├── profile/       # 资料常量(consts.json+embed)/校验/更新/昵称/公开视图
    ├── vip/           # 激活码生成/规范化/哈希/兑换(事务+行锁)/批次管理
    ├── feed/          # 公告/广播/匿名动态/在线统计
    ├── story/         # 故事书：发起/认领/结束判定/收录同意/作品与点赞
    ├── jobs/          # 保留期清理
    └── testutil/      # 集成测试夹具（-tags integration）
frontend/
├── src/{app,components,features/*,lib/{api,ws},stores,test}
├── src/lib/codename.ts        # 1v1 匿名代号（sha256 前 4 位）
├── src/lib/profileConsts.ts   # 与后端 consts.json 一致的省份/标签常量
├── public/manifest.webmanifest public/icon.svg   # PWA（无 Service Worker）
├── e2e/                       # Playwright E2E（10 个用例）
├── e2e/visual.capture.ts      # 视觉截图脚本（不带 testMatch，需 --config visual.config.ts）
├── visual.config.ts
└── playwright.config.ts
deploy/                # Dockerfile、Caddyfile、Compose、备份、敏感词库挂载
```

## 常用 make 命令

- `make test`：后端单元测试（`go test ./...`）
- `make test-integration`：后端集成测试（`TEST_DATABASE_URL` + `-tags integration -p 1`）
- `make web-test`：前端 `pnpm test && typecheck && lint`
- `make lint`：`go vet` + `staticcheck` + `pnpm lint`
- `make build`：`go build ./...` + `pnpm build`
- `make migrate` / `make migrate-down`：迁移 up / 回滚到空库
- `make sqlc`：sqlc 生成
- `make e2e`：Playwright E2E
- `cd frontend && pnpm capture:visual`：桌面/移动端视觉截图（输出到 `e2e/__screenshots__/`，不入库）
- `make db-up` / `make db-down`：开发库（依赖 Docker；本机未安装时可跳过）

## 环境变量索引

| 变量 | 说明 | 默认 |
|---|---|---|
| `APP_ENV` | development \| test \| production | development |
| `HTTP_ADDR` | HTTP 监听地址 | :8080 |
| `DATABASE_URL` | PostgreSQL 连接串（必填） | — |
| `ALLOWED_ORIGINS` | Origin 白名单（逗号分隔；生产必填） | 开发默认 localhost:5173 |
| `TRUSTED_PROXIES` | 可信代理 CIDR（才采信 X-Forwarded-For） | 空 |
| `MESSAGE_RETENTION` | 消息保留期 | 168h |
| `AUDIT_RETENTION` | 审计/举报快照保留期 | 4320h |
| `BOTTLE_TTL` | 漂流瓶/私密链接保留期 | 168h |
| `RETENTION_INTERVAL` | 清理任务间隔 | 10m |
| `VERIFICATION_MODE` | off \| optional \| required | off |
| `SMS_PROVIDER` | console 或服务商（仅 VERIFICATION_MODE≠off 时生产禁止 console） | console |
| `UPSTREAM_TRUSTED_PROXIES` | Caddy 信任的上游代理（留空=不信任，防伪造 XFF；LB 场景设 private_ranges） | 空 |
| `PHONE_ENC_KEY` | 手机号 AES-256-GCM 密钥（base64 32 字节） | — |
| `PHONE_HMAC_KEY` | 手机号 HMAC 密钥（base64 32 字节） | — |
| `WORDLIST_PATH` | 敏感词库文件（生产必填） | 空 |
| `ADMIN_IP_ALLOWLIST` | 管理后台 IP 白名单（CIDR） | 空 |
| `RATE_LIMIT_MULTIPLIER` | 限流上限倍率（生产必须 1） | 1 |
| `MATCH_RELAX_AFTER` | 匹配等待放宽阈值 | 15s |
| `MATCH_GRACE` | 匹配断线宽限期 | 30s |
| `STORY_MIN_DURATION` | 故事有效场次最短时长（仅 `APP_ENV=test` 允许 < 5m） | 5m |
| `SETTINGS_ENC_KEY` | base64 32字节；后台敏感设置及晚风 token 加密，无密钥时晚风不可用 | 空 |
| `USER_MEDIA_MAX_UPLOAD_MB` | 用户图片/视频上传上限，正整数 MiB | 20 |
| `EXTERNAL_TIMEOUT_SECONDS` | 外站 HTTP 请求超时，正整数秒 | 30 |
| `EXTERNAL_CHAT2019_RAND_SERVER_URL` | chat2019 WS 服务发现地址 | `http://v1.chat2019.cn/Act/WebService.asmx/getRandServer?ServerInfo=serversdeskry&_=` |
| `EXTERNAL_CHAT2019_FALLBACK_WS` | 服务发现失败时的 WS 地址（ws/wss） | 空 |
| `EXTERNAL_CHAT2019_IMG_SERVER_URL` | chat2019 图片服务器发现地址 | `http://v1.chat2019.cn/asmx/method.asmx/getImgServer` |
| `EXTERNAL_WANFENG_BASE_URL` | 晚风 HTTP Base URL | `https://wanfengniming.store` |
| `EXTERNAL_WANFENG_WS_URL` | 晚风 WS 地址；空时由 Base URL 推导 | 空 |
| `EXTERNAL_WANFENG_HTTP_PROXY` | 晚风出口代理（http/https/socks5）；chat2019 永远直连 | 空 |
| `EXTERNAL_WANFENG_PROTOCOL_OVERRIDES` | 晚风协议覆盖 JSON | 空 |
| `EXTERNAL_WANFENG_MEDIA_HOSTS` | 逗号分隔媒体主机白名单；同时允许 Base URL / WS 主机 | Base URL 主机 |
| `EXTERNAL_WANFENG_NEW_IDENTITY_PER_HOUR` | 晚风新账号全局令牌桶容量与每小时补充量，正整数 | 10 |
| `VITE_*` | 前端构建期变量（当前未使用） | — |

`TEST_DATABASE_URL`、`E2E_DATABASE_URL` 仅测试/E2E 使用，非应用运行配置。

## 本包新增的关键约定

- 匿名优先：1v1 会话的 `nickname` 由服务端置空，前端显示匿名代号；故事会话显示角色名；房间保留昵称。
- 匹配硬条件（清流 / VIP 性别 / VIP 同省）双向判定、不随时间放宽；非 VIP 的偏好在服务端被忽略。
- 激活码：16 位 Crockford Base32（80 bit），库中只存 sha256，兑换事务内 `SELECT … FOR UPDATE`，失败统一 `invalid_code`。
- 新增错误码：`vip_required`、`invalid_code`、`no_broadcast_card`、`story_taken`、`story_not_valid`、`not_found`。
- 前端视觉：强调色只有品牌紫 `#8B7CF6` 与辅助粉 `#E88BC4`，唯一阴影 `--shadow-soft`，动画 ≥150ms 且无弹跳，`prefers-reduced-motion` 下全部关闭。

## Bot 人设与长期记忆

参见 [实施方案](plan/202611092002_bot-persona-memory/how.md) 与 [任务状态](plan/202611092002_bot-persona-memory/task.md)。

- `bot/persona` 纯解析 ST V1/V2/V3 JSON、PNG 和 `.krp`；PNG/ZIP使用标准库，仅将既有版本 `BurntSushi/toml` 提升为直接依赖。上传 Python 和命令不执行。
- `single` 默认只运行心核；`dual` 先运行智核，以 JSON 文本调用 recall_memory、get_time、lookup_lore 白名单工具，失败降级单核。所有模型调用按 reply/reason/memory/retry 计量和限额。
- 提示词分 bot 稳定前缀、会话记忆段、每轮结尾；支持宏、备选开场白、常驻/触发世界书。跑偏检测最多重写一次，不检查或修改 AI 身份表述。
- 画像、事件、好感度和情绪按 `(bot, 对方匿名身份)` 隔离；worker 每60秒串行抽取，排除已焚毁消息。记忆默认最后互动后30天删除，后台可设1–365天；身份吊销时下次清理删除。
- 表情走普通 image 消息，受 `conv_media_limit` 约束，每轮最多一张，新消息打断则跳过；用户侧接口不新增 bot、记忆或智核字段。
- 后台沿用侧边栏路由、卡片和弹窗，支持导入、五分区编辑、表情、双核配置和记忆查看/确认删除；复制角色卡保留可编辑字段，不复制表情媒体，新卡来源 manual。所有 Bot 字符串留在管理懒加载包。
- 迁移 `00009_bot_persona_memory.sql` 随 backend 启动自动执行，不新增服务或环境变量。部署前备份数据库与媒体卷；真实模型人工验收仍由任务 P11.2 跟踪。

## 外站匹配与用户媒体

依据：[外站匹配接入与用户媒体方案](plan/202611131925_external-match-bridge/how.md)。部署和人工探测步骤见 [部署说明](wiki/deploy.md)，后台设置及媒体协议见 [协议](wiki/protocol.md)。

- 外站总开关与两站开关默认关闭，关闭时保留既有匹配和 bot 兜底行为；启用后由 matcher 统一计时，按「本站 → 外站 → 兜底」推进，所有外站请求以唯一 attempt 隔离。
- `external.Bridge` 实现 ExternalMatcher 和 conversation.Observer；匹配 goroutine 不执行数据库/网络 IO，OnMessage 只查内存映射，消息出站消费时重新核对持久化消息的会话与发送者。外站入站一律经 SendAs。
- `external/wanfeng` 相对 liao 的允许移植改动记录在其 `PORTED_FROM.md`；直连解析检查后拨号到已检查IP，晚风代理模式以配置的主机白名单约束目标，chat2019 禁止重定向。
- 用户上传只可在匹配/漂流瓶/私聊发送自己的 image/video；三种媒体来源复用 media_items。外站文本只走 TextCommand，媒体只走 MediaCommand 和 link 内登记的一次性 token，普通文本不能触发文件读取。
- `cmd/probe-external` 仅供人工执行，支持 provider/n/match/send-image/proxy/state；默认状态文件 `~/.anonchat-probe/state.json`，不允许放入仓库。自动化测试只使用本地伪造上游，不执行真实站点探测。
