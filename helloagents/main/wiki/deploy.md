# 部署、备份、回滚与上线清单

> 以代码为准。最后同步：后台可配置 bot 开关与模型接入（202611062351 / B7）。详细步骤另见 `deploy/README.md`。

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

1. 先 `docker compose exec backend /server migrate down`（迁移均含 Down）。
2. 再切回旧镜像 tag 并 `docker compose up -d`。

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
