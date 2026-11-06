# 后端生产镜像：多阶段构建，运行时使用 distroless（非 root，仅二进制）。
# 构建上下文为仓库根目录：docker build -f deploy/backend.Dockerfile .

# -------- 构建阶段 --------
FROM golang:1.27.3-bookworm AS build
WORKDIR /src

# 先复制依赖清单以利用缓存
COPY backend/go.mod backend/go.sum ./backend/
WORKDIR /src/backend
RUN go mod download

COPY backend/ ./
# 只构建 server 二进制（不构建 e2eseed 等测试工具）
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/server ./cmd/server

# -------- 运行阶段 --------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server

USER nonroot:nonroot
EXPOSE 8080

# distroless 无 curl，用内置 healthcheck 子命令
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/server", "healthcheck"]

ENTRYPOINT ["/server"]
CMD ["serve"]
