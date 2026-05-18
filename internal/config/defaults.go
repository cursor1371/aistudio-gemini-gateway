package config

import "gopkg.in/yaml.v3"

// ---------------------------------------------------------------------------
// 默认常量
// ---------------------------------------------------------------------------

const (
	DefaultHost                = "127.0.0.1"
	DefaultPort                = 8080
	DefaultServerReadTimeout   = "30s"
	DefaultServerWriteTimeout  = "0s" // 默认不限制，避免 SSE 长响应被截断
	DefaultServerIdleTimeout   = "120s"
	DefaultServerShutdownTimeout = "15s"

	DefaultWebSocketPath      = "/v1/ws"
	DefaultWSHandshakeTimeout = "10s"
	DefaultWSReadTimeout      = "60s"
	DefaultWSWriteTimeout     = "10s"
	DefaultWSPingInterval     = "30s"
	DefaultWSPongTimeout      = "60s"
	DefaultWSMaxMessageSize   int64 = 64 << 20 // 64 MiB

	DefaultRoutingStrategy    = "round_robin"
	DefaultSessionAffinityTTL = "1h"
	DefaultProviderCooldown   = "5m"

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

var DefaultEmbeddedModelNames = []string{
	"gemini-2.5-pro",
	"gemini-2.5-flash",
	"gemini-2.5-flash-lite",
	"gemini-3-pro-preview",
	"gemini-3.1-pro-preview",
	"gemini-3-flash-preview",
	"gemini-3.1-flash-lite-preview",
	"gemini-pro-latest",
	"gemini-flash-latest",
	"gemini-flash-lite-latest",
	"gemini-2.5-flash-image",
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
