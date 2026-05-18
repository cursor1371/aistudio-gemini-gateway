# syntax=docker/dockerfile:1.7

# =============================================================================
# AI Studio Gemini Gateway Dockerfile
# -----------------------------------------------------------------------------
# 设计目标：
# 1. 多阶段构建，减小最终镜像体积
# 2. 构建阶段启用 BuildKit cache，加速 GitHub Actions 构建
# 3. 运行阶段使用 alpine，兼顾小体积与可写工作目录
# 4. 支持程序启动时：
#    - 若 /app/config.yaml 存在，则直接使用
#    - 若不存在，则根据环境变量生成/覆盖配置启动
# =============================================================================

# Go 版本
ARG GO_VERSION=1.22.12

# Alpine 版本
ARG ALPINE_VERSION=3.20

# 最终输出二进制名称
ARG APP_NAME=aistudio-gemini-gateway

# =============================================================================
# 构建阶段
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG APP_NAME

# 构建工作目录
WORKDIR /src

# 安装构建期基础依赖：
# - ca-certificates：部分网络场景下更稳妥
# - tzdata：便于构建期时间处理一致
RUN apk add --no-cache ca-certificates tzdata

# -----------------------------------------------------------------------------
# 先复制 go.mod / go.sum
# 这样可以最大化利用 Docker 层缓存：
# 当源码变化但依赖未变时，无需重新下载依赖
# -----------------------------------------------------------------------------
COPY --link go.mod go.sum ./

# 下载依赖，并启用 BuildKit 缓存：
# - /go/pkg/mod：模块缓存
# - /root/.cache/go-build：编译缓存
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go mod download

# -----------------------------------------------------------------------------
# 再复制完整源码
# -----------------------------------------------------------------------------
COPY --link . .

# 交叉编译目标
ARG TARGETOS
ARG TARGETARCH

# 注入构建元信息
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

# 使用纯 Go 静态构建，避免运行时依赖 libc
ENV CGO_ENABLED=0

# -----------------------------------------------------------------------------
# 构建服务二进制
#
# 说明：
# 1. 当前项目入口位于 ./cmd/server
# 2. -trimpath：去除本地路径，减小二进制并提升可复现性
# 3. -buildvcs=false：避免额外 VCS 信息注入
# 4. -ldflags="-s -w"：去掉符号表和调试信息，减小体积
# 5. 通过 -X 注入版本、提交号、构建时间
# -----------------------------------------------------------------------------
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
      -o /out/${APP_NAME} \
      ./cmd/server

# =============================================================================
# 运行阶段
# =============================================================================
FROM alpine:${ALPINE_VERSION} AS runtime

ARG APP_NAME

# -----------------------------------------------------------------------------
# 安装运行期基础组件：
# - ca-certificates：支持 TLS / HTTPS 上游访问
# - tzdata：日志时间更稳定
#
# 同时创建非 root 用户，减少运行风险
# -----------------------------------------------------------------------------
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && \
    adduser -S -G app -h /app app && \
    mkdir -p /app && \
    chown -R app:app /app

# 运行工作目录
WORKDIR /app

# 复制最终二进制
COPY --from=builder /out/${APP_NAME} /usr/local/bin/${APP_NAME}

# 使用非 root 用户运行
USER app

# -----------------------------------------------------------------------------
# 面向轻量容器平台的默认运行参数
# -----------------------------------------------------------------------------
# TZ=UTC
#   - 统一时区，便于日志采集与排障
#
# GOMAXPROCS=1
#   - 适配 0.1C / 小核容器，避免 Go runtime 过度并行
#
# GOMEMLIMIT=384MiB
#   - 在 512M 容器内给 Go runtime 一个明确软上限
#   - 预留一部分给系统、网络缓冲、TLS、日志等
#
# AIGW_SERVER_HOST=0.0.0.0
#   - 容器平台必须监听所有网卡，否则外部无法访问
#
# AIGW_SERVER_PORT=8080
#   - 默认端口
# -----------------------------------------------------------------------------
ENV TZ=UTC \
    GOMAXPROCS=1 \
    GOMEMLIMIT=384MiB \
    AIGW_SERVER_HOST=0.0.0.0 \
    AIGW_SERVER_PORT=8080

# 暴露容器监听端口
EXPOSE 8080

# -----------------------------------------------------------------------------
# 启动说明：
# 1. 若 /app/config.yaml 存在，则程序优先读取它
# 2. 若不存在，则程序会根据环境变量生成/覆盖配置启动
# -----------------------------------------------------------------------------
ENTRYPOINT ["/usr/local/bin/aistudio-gemini-gateway"]
CMD ["-config", "/app/config.yaml"]
