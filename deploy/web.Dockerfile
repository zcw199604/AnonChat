# 前端生产镜像：Node 构建静态资源，再用 Caddy 提供服务并反向代理后端。
# 构建上下文为仓库根目录：docker build -f deploy/web.Dockerfile .

# -------- 构建阶段 --------
FROM node:24-bookworm-slim AS build
RUN corepack enable
WORKDIR /app

COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY frontend/ ./
RUN pnpm build

# -------- 运行阶段 --------
FROM caddy:2
COPY --from=build /app/dist /srv
COPY deploy/Caddyfile /etc/caddy/Caddyfile
