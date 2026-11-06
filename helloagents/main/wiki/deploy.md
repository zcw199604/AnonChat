# 部署、备份、回滚与上线清单

> 以代码为准，最后同步于 V1。详细步骤另见 `deploy/README.md`。

## 组成

- `deploy/backend.Dockerfile`：`golang:1.27.3-bookworm` 构建 → `gcr.io/distroless/static-debian12:nonroot` 运行（非 root，仅 `/server`）。
- `deploy/web.Dockerfile`：`node:24` 构建前端 → `caddy:2` 运行（`/srv` 静态资源 + 反向代理后端）。
- `deploy/Caddyfile`：`{$SITE_ADDRESS}`（域名自动 HTTPS 或 `:80` 仅反代）；`servers { trusted_proxies static {$UPSTREAM_TRUSTED_PROXIES:} }`（默认留空=不信任上游，防伪造 XFF；前有 LB/CDN 时设 `private_ranges`）；`/api/*`、`/ws` 反代 `backend:8080`；`/metrics` 返回 404；CSP/HSTS/安全头；`/assets/*` 长缓存、index no-cache、SPA 回退；`encode zstd gzip`。
- `deploy/docker-compose.yml`：固定子网 `172.28.0.0/24`；postgres（健康检查+数据卷、不对外端口）、backend（env_file、read_only、cap_drop ALL、`server healthcheck`、`TRUSTED_PROXIES`=子网、敏感词库只读挂载）、web（80/443/443udp、caddy 卷）、backup（每天 03:00 pg_dump，保留 7 份）。
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

## 上线前人工清单（task.md 12.3，HITL）

- 域名与服务器；ICP 备案、公安联网备案、互联网信息服务安全评估。
- 用户协议、隐私政策、社区规范（如实说明 7 天保留、阅后即焚语义、日志留存）。
- 生产敏感词库（`deploy/wordlist/words.txt`）。
- 短信服务商开通/签名/模板审核并填写 `SMS_*`，实现对应 `SMSProvider`（仅 VERIFICATION_MODE≠off 时必须；off 模式生产允许默认 console 且不发送短信）。
- `openssl rand -base64 32` 生成并离线备份 `PHONE_ENC_KEY`/`PHONE_HMAC_KEY`。
- 创建首个管理员；配置异地备份。
