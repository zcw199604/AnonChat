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
# 预建媒体目录：distroless 没有 shell，只能在构建阶段创建后带属主复制过去。
RUN mkdir -p /out/media
# 预建 SCF 池目录：晚风隧道托管模式需要写 pool.json。
RUN mkdir -p /out/scf

# -------- scfproxy 构建阶段（CLI 与云函数代码包固定同一版本） --------
# 云函数代码包始终使用 linux/amd64，与宿主机架构无关。
FROM golang:1.27.3-bookworm AS scfproxy-builder
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn
RUN apt-get update \
  && apt-get install -y --no-install-recommends zip \
  && rm -rf /var/lib/apt/lists/*
RUN go mod download github.com/zcw199604/scfproxy@v0.1.0 \
  && mkdir -p /out \
  && cd /go/pkg/mod/github.com/zcw199604/scfproxy@v0.1.0 \
  && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/scfproxy ./cmd/scfproxy \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /out/scf-server ./cmd/scf-server \
  && cp deploy/scf_bootstrap /out/scf_bootstrap \
  && chmod 755 /out/scf_bootstrap \
  && cd /out \
  && zip -q scf-server.zip scf-server scf_bootstrap

# -------- 运行阶段 --------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
# media 命名卷首次挂载时会继承镜像中该目录的属主，nonroot(65532) 才能写入。
COPY --from=build --chown=65532:65532 /out/media /data/media
# SCF 池命名卷同理；scfproxy CLI 与云函数代码包用于晚风隧道托管模式。
COPY --from=build --chown=65532:65532 /out/scf /data/scf
COPY --from=scfproxy-builder /out/scfproxy /usr/local/bin/scfproxy
COPY --from=scfproxy-builder /out/scf-server.zip /app/scf-server.zip

USER nonroot:nonroot
EXPOSE 8080

# distroless 无 curl，用内置 healthcheck 子命令
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/server", "healthcheck"]

ENTRYPOINT ["/server"]
CMD ["serve"]
