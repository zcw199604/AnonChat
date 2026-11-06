# AnonChat 部署说明

> 以代码为准，最后同步于 V1。

本目录包含生产部署所需的 Compose 编排、Dockerfile、Caddy 配置、备份脚本与敏感词库挂载目录。

## 1. 准备

1. 服务器安装 Docker 与 Docker Compose。
2. 进入 `deploy/`，复制环境变量示例并填写真实值：

   ```sh
   cp .env.example .env
   # 编辑 .env：SITE_ADDRESS、ACME_EMAIL、POSTGRES_PASSWORD、ALLOWED_ORIGINS、
   # VERIFICATION_MODE、PHONE_ENC_KEY/PHONE_HMAC_KEY、SMS_* 等
   ```

3. 生成手机号密钥（VERIFICATION_MODE≠off 时必填）：

   ```sh
   openssl rand -base64 32   # 分别填入 PHONE_ENC_KEY 与 PHONE_HMAC_KEY
   ```

4. 放置敏感词库：把 `words.txt` 放到 `deploy/wordlist/words.txt`（格式见该目录 README）。该文件不入库。

## 2. 启动

```sh
docker compose up -d --build
docker compose ps
```

- `backend` 启动时自动执行数据库迁移；`/healthz` 用于健康检查。
- `web`（Caddy）对外发布 80/443；域名会自动申请 HTTPS，`:80` 表示前面有负载均衡。
- 前面的负载均衡/CDN：设置 `UPSTREAM_TRUSTED_PROXIES=private_ranges`（或具体 CIDR），Caddy 才会采信
  `X-Forwarded-For`；直接对公网暴露时请保持留空（不信任任何上游），否则用户可伪造来源 IP。
- 实名短信：`VERIFICATION_MODE=off` 时允许默认 `SMS_PROVIDER=console`（不发送短信）；一旦设为
  `optional|required`，生产环境必须改用真实服务商，否则启动失败。
- 只有 `web` 对外暴露端口，`postgres`、`backend`、`backup` 仅在内网。
- 媒体库：`backend` 挂载 `media` 卷到 `/data/media`，并设置 `MEDIA_ROOT=/data/media`；后台媒体库上传的
  图片/视频都在这个卷里，删容器不会丢。备份策略需要额外覆盖该卷（见第 4 节）。

## 3. 创建首个管理员

```sh
docker compose exec -it backend /server admin create --username admin
# 交互式输入两次密码（≥12 位，不回显）
```

## 4. 备份与恢复演练

- 自动备份：`backup` 服务每天 03:00 执行 `pg_dump -Fc` 到 `backups` 卷，仅保留最近 7 份。
- 手动触发一次备份：

  ```sh
  docker compose exec backup /backup/run.sh
  ```

- 恢复演练（恢复到新库）：

  ```sh
  # 1) 取最新备份文件
  docker compose exec backup ls -1t /backups/anonchat-*.dump | head -1
  # 2) 在 postgres 中新建一个库
  docker compose exec postgres createdb -U anonchat anonchat_restore
  # 3) 用 pg_restore 恢复到新库
  docker compose exec backup sh -c 'pg_restore -d anonchat_restore /backups/<dump文件名>'
  # 4) 抽查数据后删除演练库
  docker compose exec postgres dropdb -U anonchat anonchat_restore
  ```

  建议另将 `backups` 卷内容定期同步到异地存储（由运维配置）。
- 媒体文件（`media` 卷）不在 `pg_dump` 备份范围内，需要单独同步/快照。

## 5. 回滚

1. 先回滚数据库迁移：`docker compose exec backend /server migrate down`（或回滚到目标版本）。
2. 再把镜像切回旧版本 tag 并重启：修改镜像引用后 `docker compose up -d`。
3. 迁移必须提供 down 脚本（`backend/migrations/*.sql` 均含 `-- +goose Down`）。

## 6. AI 机器人（Bot）与媒体库配置

启用 bot 引擎需要先配置模型接入（OpenAI 兼容协议，DeepSeek / OpenRouter / 通义等均可）：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BOT_ENABLED` | `false` | `true` 时启动 bot 引擎（worker、匹配兜底、漂流瓶循环）；管理接口始终可用 |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI 兼容接口地址（不带 `/chat/completions`） |
| `LLM_API_KEY` | 空 | `BOT_ENABLED=true` 时必填 |
| `LLM_MODEL` | 空 | 默认模型；`BOT_ENABLED=true` 时必填，单个 bot 可在后台覆盖 |
| `LLM_TIMEOUT` | `60s` | 单次请求超时 |
| `LLM_SEND_CACHE_KEY` | `true` | 是否发 `prompt_cache_key`；服务商拒绝未知参数时设为 `false` |
| `BOT_MATCH_FALLBACK_AFTER` | `10s` | 排队多久仍无真人配对时由 bot 兜底 |
| `BOT_BOTTLE_MIN_AGE` | `30m` | 瓶子投出多久仍没人回复，bot 才会去回 |
| `BOT_TIMEZONE` | `Asia/Shanghai` | 提示词里显示当前时间用的时区 |
| `MEDIA_ROOT` | `./data/media` | 媒体文件根目录（compose 已覆盖为 `/data/media`，启动时自动创建） |
| `MEDIA_MAX_UPLOAD_MB` | `100` | 单个媒体文件大小上限；单次上传请求体上限为其 10 倍 |

**配置来源与热切换（how.md §15）**

- 上表里的 bot 相关环境变量**只用于首次初始化**：服务第一次启动时若 `bot_settings` 表为空，就用这些值写入一行；
  之后一律以管理后台「Bot → 全局设置」为准，改环境变量不再生效。
- 后台改完立即生效（无需重启）：总开关、Base URL、模型、API Key、超时、`prompt_cache_key` 开关、
  匹配兜底时间、漂流瓶最短瓶龄、时区都会在保存后刷新到引擎；未保存时每 30 秒自动重新读取一次。
- **API Key 的优先级**：后台保存的 key（数据库，AES-256-GCM 密文）> 环境变量 `LLM_API_KEY` > 无。
  后台只显示 `••••abcd` 这样的末 4 位与来源，任何接口、日志、审计都不会出现明文或密文。
- **API Key 与 Base URL 绑定**：保存 key 时会记下当时的 Base URL（规范化：去首尾空白与末尾 `/`）。
  之后只有 Base URL 与记录一致时才使用该 key；数据库里的 key 只在"当前 Base URL == 保存时的地址"时生效，
  环境变量 `LLM_API_KEY` 只在"当前 Base URL == `LLM_BASE_URL`"时生效。改地址后旧 key 自动失效
  （后台显示"Base URL 已变更，请重新填写 API Key"），必须重新填写 key 才能保存新地址
  （或明确清除 key）；「测试连接」换地址时也必须同时填新 key，不会把已保存的 key 发到别的地址。
- 新增 `SETTINGS_ENC_KEY`：后台保存 API Key 的加密密钥（base64 编码的 32 字节），生成方式：

  ```sh
  openssl rand -base64 32
  ```

  未配置时后台**不能保存** API Key（返回 `invalid_request`），其余设置照常可保存；已保存的 key 若缺少该密钥也无法解密。
  请离线备份这个值。
- 后台还有「测试连接」按钮（每个管理员每分钟最多 10 次），会用已保存的配置发一次极短请求验证连通性。

注意：

- 不配置 `BOT_ENABLED`/`LLM_*` 时，媒体库与 bot 管理接口仍然可用，只是不会有 bot 参与聊天。
- `LLM_*` 里的密钥属于敏感信息，只能放在 `deploy/.env`（不入库）；日志中不会输出密钥与消息正文。
- 媒体文件在 `media` 卷里，异地备份时需要一并备份（例如把该卷同步到对象存储）。

## 7. 上线前人工清单（对应任务 12.3，HITL）

- 域名与服务器；ICP 备案与公安联网备案；互联网信息服务安全评估。
- 用户协议、隐私政策、社区规范（如实说明 7 天保留、阅后即焚语义与日志留存）。
- 生产敏感词库（`deploy/wordlist/words.txt`）。
- 短信服务商开通、签名与模板审核，填写 `SMS_*`；实现对应 `SMSProvider`。
- 生成并离线备份 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY`。
- 创建首个管理员；配置异地备份。

> 注意：当前仓库仅实现 `console` 短信提供方（仅开发用），生产环境禁止 `console`；接入真实服务商前，
> 生产环境无法完成启动（配置校验会拒绝 console）。这属于任务 5.x / 12.3 的 HITL 事项。
