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

## 5. 回滚

1. 先回滚数据库迁移：`docker compose exec backend /server migrate down`（或回滚到目标版本）。
2. 再把镜像切回旧版本 tag 并重启：修改镜像引用后 `docker compose up -d`。
3. 迁移必须提供 down 脚本（`backend/migrations/*.sql` 均含 `-- +goose Down`）。

## 6. 上线前人工清单（对应任务 12.3，HITL）

- 域名与服务器；ICP 备案与公安联网备案；互联网信息服务安全评估。
- 用户协议、隐私政策、社区规范（如实说明 7 天保留、阅后即焚语义与日志留存）。
- 生产敏感词库（`deploy/wordlist/words.txt`）。
- 短信服务商开通、签名与模板审核，填写 `SMS_*`；实现对应 `SMSProvider`。
- 生成并离线备份 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY`。
- 创建首个管理员；配置异地备份。

> 注意：当前仓库仅实现 `console` 短信提供方（仅开发用），生产环境禁止 `console`；接入真实服务商前，
> 生产环境无法完成启动（配置校验会拒绝 console）。这属于任务 5.x / 12.3 的 HITL 事项。
