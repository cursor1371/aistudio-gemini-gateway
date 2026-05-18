package wsrelay

import (
	"context"
	"net/http"
	"time"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/service"
)

// Logger 是 wsrelay 包内部使用的最小日志接口。
// 由外部通过 Options.Logger 注入；若未注入则使用空实现。
type Logger interface {
	DebugContext(ctx context.Context, msg string, attrs ...any)
	InfoContext(ctx context.Context, msg string, attrs ...any)
	WarnContext(ctx context.Context, msg string, attrs ...any)
	ErrorContext(ctx context.Context, msg string, attrs ...any)
}

type noopLogger struct{}

func (noopLogger) DebugContext(ctx context.Context, msg string, attrs ...any) {}
func (noopLogger) InfoContext(ctx context.Context, msg string, attrs ...any)  {}
func (noopLogger) WarnContext(ctx context.Context, msg string, attrs ...any)  {}
func (noopLogger) ErrorContext(ctx context.Context, msg string, attrs ...any) {}

// ProviderFactory 用于从 WS 握手请求中构建 Provider 身份描述。
// 通常由 registry.IdentityExtractor 驱动。
type ProviderFactory func(ctx context.Context, r *http.Request, authResult *access.Result) (*service.RuntimeProvider, error)

// ConnectedHandler 是 Provider 连接成功后的回调。
type ConnectedHandler func(ctx context.Context, provider *service.RuntimeProvider)

// DisconnectedHandler 是 Provider 断开后的回调。
type DisconnectedHandler func(ctx context.Context, provider *service.RuntimeProvider, cause error)

// TouchedHandler 是 Provider 活跃触达回调。
// 用于把 WebSocket 层的 pong / read 活动同步到 Registry 的 LastSeenAt。
type TouchedHandler func(ctx context.Context, provider *service.RuntimeProvider)

// Options 是 Manager 的构造参数。
// Manager 构造完成后不可变。
type Options struct {
	// Path 是 WS 握手路径，默认 /v1/ws。
	Path string

	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	MaxMessageSize   int64

	PingInterval time.Duration
	PongTimeout  time.Duration

	// AccessManager 用于 WS 握手鉴权；为 nil 时跳过鉴权。
	AccessManager *access.Manager
	// OriginCheck 用于 WS 升级前 Origin 校验；为 nil 时放行。
	OriginCheck func(*http.Request) bool
	// ProviderFactory 用于从握手请求中提取 Provider 身份。
	ProviderFactory ProviderFactory

	Logger Logger

	// 生命周期回调，由 Registry 监听使用。
	OnConnected    ConnectedHandler
	OnDisconnected DisconnectedHandler
	OnTouched      TouchedHandler

	// Now 可注入自定义时钟，主要用于测试。
	Now func() time.Time
}

// HTTPRequest 是通过 WS 发给 Provider 的 HTTP 风格请求。
type HTTPRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

// HTTPResponse 是 Provider 返回的 HTTP 风格响应。
type HTTPResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// StreamEvent 是流式响应事件。
// Type 标识事件类型；Err 非 nil 时表示流式过程出错。
type StreamEvent struct {
	Type    MessageType
	Status  int
	Headers http.Header
	Payload []byte
	Err     error
}
