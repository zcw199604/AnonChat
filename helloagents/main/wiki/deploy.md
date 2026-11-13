# 部署、备份、回滚与上线清单

> 以代码为准。最后同步：[外站接入与用户媒体](../plan/202611131925_external-match-bridge/how.md)（202611131925）。详细步骤另见 [deploy/README.md](../../../deploy/README.md)。

## 组成

- `deploy/backend.Dockerfile`：`golang:1.27.3-bookworm` 构建 → `gcr.io/distroless/static-debian12:nonroot` 运行（非 root，仅 `/server`）。
- `deploy/web.Dockerfile`：`node:24` 构建前端 → `caddy:2` 运行（`/srv` 静态资源 + 反向代理后端）。
- `deploy/Caddyfile`：`{$SITE_ADDRESS}`（域名自动 HTTPS 或 `:80` 仅反代）；`servers { trusted_proxies static {$UPSTREAM_TRUSTED_PROXIES:} }`（默认留空=不信任上游，防伪造 XFF；前有 LB/CDN 时设 `private_ranges`）；`/api/*`、`/ws` 反代 `backend:8080`；`/metrics` 返回 404；CSP/HSTS/安全头；`/assets/*` 长缓存、index no-cache、SPA 回退；`encode zstd gzip`。
- `deploy/docker-compose.yml`：固定子网 `172.28.0.0/24`；postgres（健康检查+数据卷、不对外端口）、backend（env_file、read_only、cap_drop ALL、`server healthcheck`、`TRUSTED_PROXIES`=子网、敏感词库只读挂载、媒体卷 `media:/data/media` 且 `MEDIA_ROOT=/data/media`）、web（80/443/443udp、caddy 卷）、backup（每天 03:00 pg_dump，保留 7 份）。
- `deploy/wordlist/`：敏感词库挂载目录（`words.txt` 不入库）。

## 启动

```sh
cd deploy && cp .env.example .env   # 填写真实值
docker compose up -d --build
docker compose ps
```

后端启动时自动执行迁移；`/healthz` 健康检查。

## 首个管理员

```sh
docker compose exec -it backend /server admin create --username admin
```

## 备份与恢复演练

- 自动：backup 服务每天 03:00 备份到 `backups` 卷，保留最近 7 份。
- 手动：`docker compose exec backup /backup/run.sh`。
- 恢复演练：新建库 `anonchat_restore`，`pg_restore -d anonchat_restore <dump>`，抽查后删除演练库（详见 deploy/README.md）。

## 回滚

1. 先备份数据库、媒体卷与加密密钥，确认目标版本。仅停用外站时关闭后台总开关，不必回滚数据库。
2. 必要的数据层回滚须停 backend，使用同版迁移文件及 goose 的目标版本能力；再同步切回兼容的前后端镜像。

不要用 `/server migrate down` 或 `make migrate-down` 单步回滚：当前实现会回滚全部迁移到空库。外站 `00010 → 00009` 的数据和文件处理见下文。

## 本包新增的部署相关事项（AI 机器人 + 媒体库）

- 迁移 `00005_bots.sql`：新增 `character_cards`、`media_folders`、`media_items`、`bots`、`bot_usage`，
  并给 `messages.media_id` 加外键（`ON DELETE SET NULL`）与部分索引。deploy 前请先备份；Down 可完整回滚。
- 新增媒体卷：`deploy/docker-compose.yml` 给 backend 挂载 `media:/data/media` 并设置 `MEDIA_ROOT=/data/media`
  （启动时自动创建目录）。`read_only` 根文件系统不影响卷的写入。
- **备份**：`pg_dump` 不含媒体文件，`media` 卷需要单独同步/快照到异地存储。
- 环境变量（全部写入 `deploy/.env.example`，也可不配）：`BOT_ENABLED`(false)、`LLM_BASE_URL`、
  `LLM_API_KEY`、`LLM_MODEL`、`LLM_TIMEOUT`(60s)、`LLM_SEND_CACHE_KEY`(true)、
  `BOT_MATCH_FALLBACK_AFTER`(10s)、`BOT_BOTTLE_MIN_AGE`(30m)、`BOT_TIMEZONE`(Asia/Shanghai)、
  `SETTINGS_ENC_KEY`、`MEDIA_ROOT`(默认 `./data/media`，compose 覆盖为 `/data/media`)、`MEDIA_MAX_UPLOAD_MB`(100)。
- **bot 相关环境变量只用于首次初始化**：启动时若 `bot_settings` 表为空则写入一行；之后以后台
  「Bot → 全局设置」为准（开关、Base URL、模型、API Key、超时、缓存 key、兜底时间、瓶龄、时区都可热切换，无需重启）。
  引擎始终创建并运行，开关关闭或配置不完整时自动不工作（不兜底、不回瓶、不回复）。
- 因此 `BOT_ENABLED=true` 不再要求 `LLM_API_KEY`/`LLM_MODEL`（可在后台补填）。
- `SETTINGS_ENC_KEY`（base64 的 32 字节，`openssl rand -base64 32`）用于加密后台保存的 API Key：
  未配置时后台不能保存 key（返回 `invalid_request`，其余设置照常），生产未配置只打 warn 日志；
  请离线备份，丢失后需在后台重新填写 key。
- **API Key 优先级**：数据库（后台保存的密文）> 环境变量 `LLM_API_KEY` > 无。接口只返回 `apiKeySet`/`apiKeyHint`/`apiKeySource`。
- 上线前人工事项：确认所选模型服务商支持 OpenAI 兼容协议与提示词前缀缓存；按需关闭 `LLM_SEND_CACHE_KEY`
  （部分服务商拒绝未知参数 `prompt_cache_key`）；媒体内容由运营方自行准备并遵守版权与内容合规要求。

## 既有部署相关事项

- 迁移 `00004_profile_vip_story.sql`：deploy 前请先备份（`pg_dump -Fc`）。该迁移含 `ALTER TYPE conversation_kind ADD VALUE 'story'`，goose 在单事务中执行成功（本机 PostgreSQL 16 已验证 up → down → up 全通过）；Down 会重建枚举类型。
- 新环境变量 `STORY_MIN_DURATION`（默认 `5m`）：故事有效场次的最短时长。只有 `APP_ENV=test` 允许小于 5 分钟；生产使用默认值即可，无需配置。
- 新增依赖 `@fontsource-variable/inter@5.3.0`：字体文件随前端构建产物打进 `/assets`，符合 Caddy CSP `default-src 'self'`（`font-src` 走 self），无需额外配置。
- PWA：`public/manifest.webmanifest` 与 `public/icon.svg` 随静态资源发布；未启用 Service Worker，因此不存在缓存失效问题。
- 激活码不在站内售卖：部署后由运营方在站外分发，管理员在后台「激活码」页生成批次并**当次下载 CSV**（明文只显示一次）。
- 审核人力：广播与作品都需人工审核（后台「广播审核」「作品审核」），上线前请安排值班。

## 上线前人工清单（task.md 12.3，HITL）

- 域名与服务器；ICP 备案、公安联网备案、互联网信息服务安全评估。
- 用户协议、隐私政策、社区规范（如实说明 7 天保留、阅后即焚语义、日志留存）。
- 生产敏感词库（`deploy/wordlist/words.txt`）。
- 短信服务商开通/签名/模板审核并填写 `SMS_*`，实现对应 `SMSProvider`（仅 VERIFICATION_MODE≠off 时必须；off 模式生产允许默认 console 且不发送短信）。
- `openssl rand -base64 32` 生成并离线备份 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY`。
- 创建首个管理员；配置异地备份。
- 本包新增 HITL：用户协议中的 VIP 与 UGC 条款、激活码分发渠道与经营合规、广播与作品的审核人员安排。

## 可配置限流升级（迁移 00008）

- 新增 `rate_limit_rules` 默认规则和 `identities(created_ip, created_at)` 索引。启动加载规则失败会终止启动；后台「限流规则」保存后在当前服务实例即时生效。
- 创建与换身份共享每 IP 5 次/24 小时滚动窗口，按成功创建数计数（含已撤销身份）；升级前过去 24 小时的记录立即纳入。超限默认封 IP 24 小时。
- 身份窗口最大值为 `AUDIT_RETENTION`；调小保留期前应先调小身份窗口，否则启动校验会拒绝已有的超长窗口。默认身份窗口为 24 小时，保留期不应短于它。
- `RATE_LIMIT_MULTIPLIER` 仅在运行时缩放频率规则与每 IP WS 总连接上限，不写库，也不缩放同 IP 同身份连接上限。WS 默认每 IP 3 条、同 IP 同身份 1 条，下调只限制后续连接。
- 自动封禁沿用持久化 bans；已有有效封禁不延期。`banSeconds=0` 不封、`-1` 永久。不支持封禁的规则只能配置 0。
- 频率计数与 WS 准入在进程内维护，自动封禁主体锁也为单实例范围；身份创建计数使用数据库事务锁。

## 外站接入与用户媒体（迁移 00010）

参见[方案与安全边界](../plan/202611131925_external-match-bridge/how.md)、[任务状态与人工 P11](../plan/202611131925_external-match-bridge/task.md)，以及[部署操作详解](../../../deploy/README.md#9-外站接入与用户媒体00010)。本文档记录操作方式，不代表已执行外站探测或发布。

- backend 启动自动应用 `00010_external_bridge.sql`；首次 `external_settings` 的总开关与两站开关均关闭。开关、在线阈值、本站等待、外站匹配时长、连接上限由后台「外站接入」保存后即时生效，不由环境变量启用。
- 用户图片/视频限匹配、漂流瓶、私聊；来源为 user/external 的媒体存于 `MEDIA_ROOT/user`、`MEDIA_ROOT/external`。超过 24 小时且没有未焚毁消息引用的非媒体库媒体由定时任务清理。部署前备份数据库和媒体卷。

### 环境变量

所有新增项列于 [deploy/.env.example](../../../deploy/.env.example)；服务启动时读取，改动须重启 backend。

| 变量 | 默认值与含义 |
|---|---|
| `USER_MEDIA_MAX_UPLOAD_MB` | 20，正整数 MB，用户单文件上限；媒体按真实 MIME 白名单嗅探 |
| `EXTERNAL_TIMEOUT_SECONDS` | 30，正整数秒且不超过时长范围，服务端外站 HTTP 超时，不是外站匹配时长 |
| `EXTERNAL_CHAT2019_RAND_SERVER_URL` | `http://v1.chat2019.cn/Act/WebService.asmx/getRandServer?ServerInfo=serversdeskry&_=`，WS 发现 |
| `EXTERNAL_CHAT2019_FALLBACK_WS` | 空，发现失败时备用地址，须为 ws/wss |
| `EXTERNAL_CHAT2019_IMG_SERVER_URL` | `http://v1.chat2019.cn/asmx/method.asmx/getImgServer`，媒体服务器发现，返回主机不得含路径 |
| `EXTERNAL_WANFENG_BASE_URL` | `https://wanfengniming.store`，须为合法 HTTP(S) 根地址 |
| `EXTERNAL_WANFENG_WS_URL` | 空，默认 BaseURL 推导 `/ws`；显式值须为 ws/wss，协议可覆盖 WSPath |
| `EXTERNAL_WANFENG_HTTP_PROXY` | 空，直连；可配置单一合法 http/https/socks5 URL（不含业务路径/query/fragment） |
| `EXTERNAL_WANFENG_PROTOCOL_OVERRIDES` | 空；非空必须是合法 Protocol 覆盖 JSON，装配时还校验字段类型 |
| `EXTERNAL_WANFENG_NEW_IDENTITY_PER_HOUR` | 10，正整数；全进程新账号令牌桶容量与每小时补充量，已有绑定复用不消耗 |
| `EXTERNAL_WANFENG_MEDIA_HOSTS` | 默认 BaseURL 主机；逗号分隔的媒体主机名，总白名单还合并 BaseURL 与显式 WSURL 主机 |
| `SETTINGS_ENC_KEY` | 复用 base64 32 字节密钥，项目 secretbox（AES-256-GCM）加密晚风 token；缺少时晚风不可用，chat2019 不受影响 |

`MEDIA_MAX_UPLOAD_MB=100` 继续控制媒体库及外站保存上限；`MEDIA_ROOT` 在 compose 中为 `/data/media`。密钥必须连同数据库备份离线保存，不能随升级重新生成。

chat2019 永远直连且禁止重定向；晚风直连先检查解析结果，再拨号到检查过的 IP。晚风代理模式按可信公网域名白名单限制 HTTP/WS/媒体和重定向，**无法控制代理侧 DNS**；域名解析遭篡改仍可能连到错误地址，这是代理模式剩余风险，须维护可信代理与 DNS。

### 人工探测和逐步启用

1. 总开关保持关闭，先在部署机以服务相同出口运行 probe-external。distroless 镜像只有 `/server`，须从同版本源码用 Go 1.27.3 编译独立 probe，或交叉编译到部署机目标 OS/架构后传入；构建、用法见[部署详解](../../../deploy/README.md#探测工具与启用顺序人工执行)。
2. 运行示例：`anonchat-probe-external -provider wanfeng -n 1 -match 30s -state "$HOME/.anonchat-probe/state.json" [-send-image /path/to/test.png] [-proxy http://proxy.example.com:8080]`；chat2019 同样可选发图，但不能传代理。方括号表示可选参数，不应原样输入。
3. 工具不连数据库；读取与服务相同的外站 endpoint、Protocol 覆盖和媒体主机环境变量，不自动读取 `.env`。代理显式通过 `-proxy`，连接及 HTTP 操作限时 10 秒，观察时长由 `-match` 决定。输出脱敏帧与每身份连接耗时、是否配上、配上耗时、错误类型；入站媒体不下载，图片收发效果需由人工结合对方界面核对。
4. 晚风绑定先用 MemoryBindingStore，再即时写到默认 `~/.anonchat-probe/state.json`，重复运行复用账号。状态含明文 token，文件 `0600`、专用目录 `0700`、临时文件加 rename；拒绝仓库内路径、符号链接绕过、宽权限与损坏状态，不会自动修改既有共享目录。保留并私下备份，不入库、不打印 token。
5. 人工确认后，后台只开一个站点并设低连接上限，再打开总开关。默认阈值 5、本站等待 10 秒、外站匹配 10 秒、两站上限各 20，默认容量不是实测安全值。
6. 观察每 10 秒刷新的状态表或管理员 `GET /api/admin/external/status`：可用原因、连接/匹配/会话/空闲、在线/排队、冷却、最近错误与失败计数，稳定后逐步放开容量和第二站。开启外站后 Bot「匹配时间」不生效，兜底时机由外站接入设置决定；关闭后恢复旧逻辑。

### 停用与回滚到 00009

- **关闭总开关立即生效**：停止正在匹配的外站连接、结束所有进行中的外站会话并关闭连接；不会结束已有本站真人/bot 会话。通常停用到此即可。
- 确需数据库回滚时，先停止写入、停 backend，备份数据库/媒体卷/密钥，由人工用同版 SQL 的 goose `down-to 9` 精确回到 `00009`，再同步回滚前后端；不要使用回滚到空库的 server/make down 入口。
- `00010` Down 会删除 `source <> 'library'` 的所有媒体行，消息媒体外键变为 NULL；保留媒体库行并恢复 folder 非空约束。四张外站表删除，绑定与外站映射丢失；既有影子身份（identities）、会话和消息保留。
- SQL 不删磁盘文件。确认备份、实际 MEDIA_ROOT 及无新写入后，人工清理 `MEDIA_ROOT/user` 和 `MEDIA_ROOT/external`（compose 媒体卷内 `/data/media/user`、`/data/media/external`），保留媒体库目录。新前端不能继续配旧后端上传/发媒体，须一并回退。
