package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/internal/common"
	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/internal/observability"
)

// Middleware 是标准 net/http 中间件签名。
type Middleware func(http.Handler) http.Handler

type contextKey string

const (
	// requestIDHeader 是客户端可传入或服务端自动生成的请求 ID 响应头。
	requestIDHeader = "X-Request-ID"

	// accessResultKey 是鉴权结果写入 context 的键。
	accessResultKey contextKey = "httpapi_access_result"
)

// AccessResultFromContext 从上下文读取鉴权结果。
func AccessResultFromContext(ctx context.Context) (*access.Result, bool) {
	if ctx == nil {
		return nil, false
	}
	value := ctx.Value(accessResultKey)
	result, ok := value.(*access.Result)
	return result, ok && result != nil
}

// Chain 按顺序组合中间件。
// middlewares[0] 最先处理请求（最外层），middlewares[n-1] 最后（最靠近 handler）。
func Chain(final http.Handler, middlewares ...Middleware) http.Handler {
	if final == nil {
		final = http.NotFoundHandler()
	}
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] != nil {
			final = middlewares[i](final)
		}
	}
	return final
}

// RequestID 中间件：
// 1. 若请求头已带 X-Request-ID，则沿用
// 2. 否则自动生成
// 3. 写回响应头
// 4. 写入 context，供后续链路使用
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
			if requestID == "" {
				requestID = common.GenerateRequestID()
			}

			ctx := common.WithRequestID(r.Context(), requestID)
			r = r.WithContext(ctx)
			w.Header().Set(requestIDHeader, requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// Recover 中间件：统一兜底 panic，返回 Gemini 风格错误 JSON。
func Recover(logger observability.Logger) Middleware {
	if logger == nil {
		logger = observability.NoopLogger{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "http panic recovered",
						"request_id", common.RequestIDFromContext(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", recovered,
					)
					writeGeminiError(w, http.StatusInternalServerError, "内部服务错误")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit 对请求体施加最大读取限制。
// 超过限制后 io.ReadAll 会返回 http.MaxBytesError。
func BodyLimit(maxBytes int64) Middleware {
	if maxBytes <= 0 {
		maxBytes = 32 << 20 // 32 MiB
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && !methodLikelyBodyless(r.Method) {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog 记录 HTTP 访问日志。
// 该中间件是否挂载由 Server 装配层根据 logging.access-log 配置决定。
// Query 参数会自动脱敏，避免日志中泄露 API Key。
func AccessLog(logger observability.Logger) Middleware {
	if logger == nil {
		logger = observability.NoopLogger{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			wrapped := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			logger.InfoContext(r.Context(), "http access",
				"request_id", common.RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"query", common.RedactValues(r.URL.Query()),
				"status_code", wrapped.statusCode,
				"bytes_written", wrapped.bytesWritten,
				"latency_ms", time.Since(startedAt).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// Auth 中间件：
// 1. manager 为 nil 或未配置任何 provider 时，直接放行（兼容无鉴权嵌入式场景）
// 2. 认证成功后把 access.Result 写入 context，供后续 handler 读取
// 3. 认证失败返回 Gemini 风格错误 JSON
func Auth(manager *access.Manager) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if manager == nil || len(manager.Providers()) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			result, authErr := manager.Authenticate(r.Context(), r)
			if authErr == nil {
				if result != nil {
					ctx := context.WithValue(r.Context(), accessResultKey, result)
					r = r.WithContext(ctx)
				}
				next.ServeHTTP(w, r)
				return
			}

			statusCode := authErr.HTTPStatusCode()
			if statusCode <= 0 {
				statusCode = http.StatusUnauthorized
			}
			writeGeminiError(w, statusCode, authErr.Message)
		})
	}
}

// CORS 中间件：
// 根据服务启动时固定的 CORSConfig 处理跨域。
// 支持 allow-list / * 通配 / allow-credentials / OPTIONS 预检快速返回。
func CORS(cfg config.CORSConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				if allowedOrigin := resolveAllowedOrigin(origin, cfg); allowedOrigin != "" {
					w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
					w.Header().Add("Vary", "Origin")
				}
			}

			allowedMethods := strings.Join(nonEmptyStrings(cfg.AllowedMethods), ", ")
			if allowedMethods != "" {
				w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
			}

			allowedHeaders := strings.Join(nonEmptyStrings(cfg.AllowedHeaders), ", ")
			if allowedHeaders == "" {
				allowedHeaders = strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
			}
			if allowedHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			}

			exposeHeaders := strings.Join(nonEmptyStrings(cfg.ExposeHeaders), ", ")
			if exposeHeaders != "" {
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
			}

			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if cfg.MaxAgeSeconds > 0 {
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAgeSeconds))
			}

			// OPTIONS 预检请求直接返回，不进入后续 handler。
			if strings.EqualFold(r.Method, http.MethodOptions) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteError 将任意 error 转换为 Gemini 风格 JSON 错误响应。
// 该函数可被 handler 直接复用。
// 支持：
// 1. http.MaxBytesError -> 413
// 2. GatewayError -> 对应 HTTPStatus + SafeMessage + Retry-After
// 3. 其他 error -> 500
func WriteError(w http.ResponseWriter, err error) {
	if err == nil {
		writeGeminiError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// 请求体过大。
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) && maxBytesErr != nil {
		writeGeminiError(w, http.StatusRequestEntityTooLarge, "请求体过大")
		return
	}

	// 统一错误体系：GatewayError。
	type httpStatusProvider interface {
		HTTPStatus() int
		SafeMessage() string
	}

	type retryAfterProvider interface {
		RetryAfterDuration() time.Duration
	}

	var provider httpStatusProvider
	if errors.As(err, &provider) && provider != nil {
		status := provider.HTTPStatus()
		msg := provider.SafeMessage()

		// 若错误对象携带 Retry-After 信息，输出到响应头。
		var retryProvider retryAfterProvider
		if errors.As(err, &retryProvider) && retryProvider != nil {
			if retryAfter := retryProvider.RetryAfterDuration(); retryAfter > 0 {
				seconds := int(retryAfter / time.Second)
				if retryAfter%time.Second != 0 {
					seconds++
				}
				if seconds <= 0 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
			}
		}

		writeGeminiError(w, status, msg)
		return
	}

	writeGeminiError(w, http.StatusInternalServerError, err.Error())
}

// =========================
// statusRecorder：支持状态码捕获的 ResponseWriter 包装器
// =========================

type statusRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (w *statusRecorder) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *statusRecorder) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += n
	return n, err
}

// Unwrap 允许 ResponseController 等探测底层能力。
func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush 保留底层 Flusher 能力。对 SSE / 流式输出至关重要。
func (w *statusRecorder) Flush() {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack 保留底层 Hijacker 能力。对 WebSocket Upgrade 至关重要。
func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	if w.statusCode == 0 {
		w.statusCode = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

// Push 保留 HTTP/2 Server Push 能力（若底层支持）。
func (w *statusRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

// =========================
// 内部工具函数
// =========================

// methodLikelyBodyless 判断 HTTP 方法是否通常不携带请求体。
func methodLikelyBodyless(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

// resolveAllowedOrigin 根据 CORS 配置判断请求 Origin 是否被允许。
func resolveAllowedOrigin(origin string, cfg config.CORSConfig) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	allowed := nonEmptyStrings(cfg.AllowedOrigins)
	if len(allowed) == 0 {
		return ""
	}

	hasWildcard := false
	for _, item := range allowed {
		if item == "*" {
			hasWildcard = true
			break
		}
	}
	if hasWildcard {
		if cfg.AllowCredentials {
			// 凭证模式下不能直接返回 *，应回显原始 Origin。
			return origin
		}
		return "*"
	}

	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), origin) {
			return origin
		}
	}
	return ""
}

// nonEmptyStrings 过滤空字符串。
func nonEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// writeGeminiError 输出 Gemini 风格 JSON 错误响应。
func writeGeminiError(w http.ResponseWriter, statusCode int, message string) {
	if statusCode <= 0 {
		statusCode = http.StatusInternalServerError
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(statusCode)
		if message == "" {
			message = "internal server error"
		}
	}

	body := map[string]any{
		"error": map[string]any{
			"code":    statusCode,
			"message": message,
			"status":  geminiStatus(statusCode),
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		data = []byte(`{"error":{"code":500,"message":"internal server error","status":"INTERNAL"}}`)
		statusCode = http.StatusInternalServerError
	}

	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Del("Content-Length")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}

// geminiStatus 将 HTTP 状态码映射为 Gemini 标准 status 字符串。
func geminiStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	default:
		if statusCode >= 500 {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}
