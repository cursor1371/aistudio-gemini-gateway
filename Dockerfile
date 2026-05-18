# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.22.12
ARG ALPINE_VERSION=3.20
ARG APP_NAME=aistudio-gemini-gateway

# =========================
# 构建阶段
# =========================
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG APP_NAME

WORKDIR /src

# 保留基础证书与时区数据，便于构建阶段某些依赖场景使用
RUN apk add --no-cache ca-certificates tzdata

# 先复制 go mod 文件，最大化利用 Docker 层缓存
COPY --link go.mod go.sum* ./

# 依赖下载使用 BuildKit cache mount，提高 CI 构建性能
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go mod download

# 再复制源码，避免源码变化导致 go mod download 缓存失效
COPY --link . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

ENV CGO_ENABLED=0

# 构建静态二进制：
# - trimpath：减少路径信息，缩小二进制
# - buildvcs=false：避免额外 VCS 元信息注入
# - -s -w：去掉符号表和调试信息，缩小体积
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
      -o /out/${APP_NAME} \
      ./main.go

# =========================
# 运行阶段
# =========================
FROM alpine:${ALPINE_VERSION} AS runtime

ARG APP_NAME

# 安装运行时必须组件：
# - ca-certificates：若后续有 TLS 访问能力可直接支持
# - tzdata：日志时间与时区一致性更好
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && \
    adduser -S -G app -h /app app && \
    mkdir -p /app && \
    chown -R app:app /app

WORKDIR /app

# 复制二进制
COPY --from=builder /out/${APP_NAME} /usr/local/bin/${APP_NAME}

USER app

# 面向轻量容器平台的默认运行参数：
# 1. 监听 0.0.0.0，适配容器网络
# 2. 默认端口 8080
# 3. GOMAXPROCS=1，避免 Go runtime 过度并行
# 4. GOMEMLIMIT=384MiB，控制 Go 堆内存增长
ENV TZ=UTC \
    GOMAXPROCS=1 \
    GOMEMLIMIT=384MiB \
    AIGW_SERVER_HOST=0.0.0.0 \
    AIGW_SERVER_PORT=8080

EXPOSE 8080

# 不内置 config.yaml：
# - 若运行时挂载 /app/config.yaml，则直接使用
# - 若不存在，则程序会按“环境变量渲染/覆盖”策略启动
ENTRYPOINT ["/usr/local/bin/aistudio-gemini-gateway"]
CMD ["-config", "/app/config.yaml"]
