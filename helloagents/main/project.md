# AnonChat 项目说明

> 以代码为准。最后同步：界面改版 + 资料与匹配偏好 + VIP 激活码 + 公告广播 + 故事书（202611062140）。

## 技术栈

- 后端：Go 1.27（`go.mod` 声明 `go 1.27`，`toolchain go1.27.3`）；标准库 `net/http`（`ServeMux` 方法+路径模式，无 Web 框架）。
- WebSocket：`github.com/coder/websocket`。
- 数据库：PostgreSQL 16/17 + `github.com/jackc/pgx/v5`（pgxpool）；SQL 代码生成 sqlc；迁移 goose（SQL 嵌入二进制）。
- 加密/密码：`crypto/aes`(AES-256-GCM)、`crypto/hmac`(HMAC-SHA256)、`golang.org/x/crypto/bcrypt`、`golang.org/x/term`（CLI 密码输入）。
- 日志：`log/slog`（JSON）。
- 前端：React 19 + TypeScript + Vite；状态 Zustand；路由 React Router；样式 Tailwind CSS v4；字体 `@fontsource-variable/inter@5.3.0`（打包进静态资源）；测试 Vitest + Testing Library；E2E Playwright（仅 chromium）。
- 工具（`go get -tool` 声明，`go tool` 调用）：sqlc、staticcheck。

## 目录结构

```
backend/
├── cmd/server/        # server：serve | migrate up|down | admin create | healthcheck
├── cmd/e2eseed/       # 仅 APP_ENV=test：从 stdin 读密码创建 E2E 管理员与可用激活码（--out 写 .seed.json）
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
| `VITE_*` | 前端构建期变量（当前未使用） | — |

`TEST_DATABASE_URL`、`E2E_DATABASE_URL` 仅测试/E2E 使用，非应用运行配置。

## 本包新增的关键约定

- 匿名优先：1v1 会话的 `nickname` 由服务端置空，前端显示匿名代号；故事会话显示角色名；房间保留昵称。
- 匹配硬条件（清流 / VIP 性别 / VIP 同省）双向判定、不随时间放宽；非 VIP 的偏好在服务端被忽略。
- 激活码：16 位 Crockford Base32（80 bit），库中只存 sha256，兑换事务内 `SELECT … FOR UPDATE`，失败统一 `invalid_code`。
- 新增错误码：`vip_required`、`invalid_code`、`no_broadcast_card`、`story_taken`、`story_not_valid`、`not_found`。
- 前端视觉：强调色只有品牌紫 `#8B7CF6` 与辅助粉 `#E88BC4`，唯一阴影 `--shadow-soft`，动画 ≥150ms 且无弹跳，`prefers-reduced-motion` 下全部关闭。
