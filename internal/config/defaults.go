package config

import "gopkg.in/yaml.v3"

// ---------------------------------------------------------------------------
// 默认常量
// ---------------------------------------------------------------------------

const (
	DefaultHost                  = "127.0.0.1"
	DefaultPort                  = 8080
	DefaultServerReadTimeout     = "30s"
	DefaultServerWriteTimeout    = "0s" // 默认不限制，避免 SSE 长响应被截断
	DefaultServerIdleTimeout     = "120s"
	DefaultServerShutdownTimeout = "15s"

	DefaultWebSocketPath      = "/v1/ws"
	DefaultWSHandshakeTimeout = "10s"

	// DefaultWSReadTimeout 从 60s 提升为 300s (5 分钟)。
	// 确保长推理模型在产生首个 Token 前的漫长“思考期”内不会被误判断连。
	DefaultWSReadTimeout = "300s"

	DefaultWSWriteTimeout = "10s"
	DefaultWSPingInterval = "30s"

	// DefaultWSPongTimeout 从 60s 提升为 120s (2 分钟)。
	// 给 Provider 在高并发或主线程繁忙时留足回复 Pong 的余量。
	DefaultWSPongTimeout = "120s"

	DefaultWSMaxMessageSize int64 = 64 << 20 // 64 MiB

	DefaultRoutingStrategy    = "round_robin"
	DefaultSessionAffinityTTL = "1h"
	DefaultProviderCooldown   = "5m"

	// DefaultBootstrapTimeout 启动首包超时 (默认 60s)。
	// 从发出请求到收到第一个有效上游响应包（如 stream_start）的最长等待时间。
	DefaultBootstrapTimeout = "60s"

	// DefaultStreamIdleTimeout 流式空闲中断超时 (默认 90s)。
	// 流式传输过程中，连续两帧 chunk 之间的最长允许间隔。
	DefaultStreamIdleTimeout = "90s"

	// DefaultNonStreamTimeout 非流式总体执行超时 (默认 600s)。
	// 适用于 generateContent / countTokens 的单次完整执行时间上限。
	DefaultNonStreamTimeout = "600s"

	DefaultModelsSource = "embedded"

	DefaultGeminiAPIVersion   = "v1beta"
	DefaultGeminiSafetyMode   = "auto"
	DefaultGeminiThinkingMode = "auto"

	DefaultLoggingLevel  = "info"
	DefaultLoggingFormat = "text"

	DefaultOriginMode        = "allow-missing"
	DefaultCORSMaxAgeSeconds = 600
)

// ---------------------------------------------------------------------------
// 默认认证来源
// ---------------------------------------------------------------------------

var (
	DefaultHTTPHeaderNames = []string{"X-Goog-Api-Key", "X-Api-Key"}
	DefaultHTTPQueryNames  = []string{"key", "auth_token"}

	DefaultWSHeaderNames = []string{"X-Goog-Api-Key", "X-Api-Key"}
	DefaultWSQueryNames  = []string{"key", "auth_token"}
)

// ---------------------------------------------------------------------------
// 默认 CORS 头
// ---------------------------------------------------------------------------

var (
	DefaultCORSAllowedMethods = []string{"GET", "POST", "OPTIONS"}
	DefaultCORSAllowedHeaders = []string{
		"Authorization", "Content-Type",
		"X-Goog-Api-Key", "X-Api-Key",
		"X-Request-ID", "X-Session-ID",
	}
	DefaultCORSExposeHeaders = []string{
		"X-Request-ID", "Retry-After",
	}
)

// ---------------------------------------------------------------------------
// 默认 Provider 身份提取来源
// ---------------------------------------------------------------------------

var (
	DefaultProviderIDHeaderNames       = []string{"X-Provider-ID"}
	DefaultProviderIDQueryNames        = []string{"provider_id"}
	DefaultProviderLabelHeaderNames    = []string{"X-Provider-Label"}
	DefaultProviderLabelQueryNames     = []string{"provider_label", "label"}
	DefaultProviderTagsHeaderNames     = []string{"X-Provider-Tags"}
	DefaultProviderTagsQueryNames      = []string{"provider_tags", "tags"}
	DefaultProviderPriorityHeaderNames = []string{"X-Provider-Priority"}
	DefaultProviderPriorityQueryNames  = []string{"provider_priority", "priority"}
)

// ---------------------------------------------------------------------------
// 内置模型名称列表（仅用于配置校验时判断 alias target 是否合法）
// ---------------------------------------------------------------------------

// DefaultEmbeddedModelNames 仅用于配置校验。
// 该列表需要与 internal/models/embedded.go 保持一致。
var DefaultEmbeddedModelNames = []string{
	"gemini-2.5-pro",
	"gemini-2.5-flash",
	"gemini-2.5-flash-lite",
	"gemini-2.5-flash-image",
	"gemini-3.1-pro-preview",
	"gemini-3-flash-preview",
	"gemini-3.5-flash",
	"gemini-3.1-flash-lite",
	"gemini-pro-latest",
	"gemini-flash-latest",
	"gemini-flash-lite-latest",
}

// ---------------------------------------------------------------------------
// 工厂方法
// ---------------------------------------------------------------------------

// DefaultConfig 返回填充了全部默认值的完整配置。
func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.ApplyDefaults()
	cfg.Normalize()
	return cfg
}

// ExampleConfig 返回一个可直接输出为 YAML 模板的示例配置。
func ExampleConfig() *Config {
	cfg := DefaultConfig()

	cfg.Access.HTTP.Enabled = true
	cfg.Access.HTTP.Keys = []string{"replace-with-your-http-api-key"}

	cfg.WebSocket.Auth.Enabled = true
	cfg.WebSocket.Auth.Keys = []string{"replace-with-your-ws-api-key"}

	cfg.Models.Source = "embedded"
	cfg.Models.Aliases = []ModelAlias{
		{Alias: "gemini-pro", Target: "gemini-2.5-pro", Expose: true},
	}

	return cfg
}

// ExampleYAML 生成示例 YAML 文本。
func ExampleYAML() (string, error) {
	data, err := yaml.Marshal(ExampleConfig())
	if err != nil {
		return "", err
	}
	return string(data), nil
}