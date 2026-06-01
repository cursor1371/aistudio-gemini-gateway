package gateway

import (
	"io"
	"time"

	"aistudio-gemini-gateway/internal/access"
	internalconfig "aistudio-gemini-gateway/internal/config"
	modelspkg "aistudio-gemini-gateway/internal/models"
	"aistudio-gemini-gateway/internal/observability"
	registrypkg "aistudio-gemini-gateway/internal/provider/registry"
	selectorpkg "aistudio-gemini-gateway/internal/provider/selector"
	"aistudio-gemini-gateway/internal/wsrelay"
	core "aistudio-gemini-gateway/service"
)

// =========================
// 配置类型
// =========================

// Config 是网关根配置。
type Config = internalconfig.Config

// ValidationError 表示配置校验失败。
type ValidationError = internalconfig.ValidationError

// ServerConfig 是 HTTP 服务监听配置。
type ServerConfig = internalconfig.ServerConfig

// TLSConfig 是 HTTPS 配置。
type TLSConfig = internalconfig.TLSConfig

// AccessConfig 是下游 HTTP API 访问控制配置。
type AccessConfig = internalconfig.AccessConfig

// AccessPolicy 描述一种基于静态 key 的认证策略。
type AccessPolicy = internalconfig.AccessPolicy

// CORSConfig 是 CORS 配置。
type CORSConfig = internalconfig.CORSConfig

// WebSocketConfig 是 Provider WebSocket 接入配置。
type WebSocketConfig = internalconfig.WebSocketConfig

// HeartbeatConfig 是 WS 心跳配置。
type HeartbeatConfig = internalconfig.HeartbeatConfig

// OriginPolicyConfig 是 WS Origin 校验策略。
type OriginPolicyConfig = internalconfig.OriginPolicyConfig

// ProviderIdentityConfig 定义 Provider 身份元数据提取来源。
type ProviderIdentityConfig = internalconfig.ProviderIdentityConfig

// RoutingConfig 是 Provider 选择与重试配置。
type RoutingConfig = internalconfig.RoutingConfig

// ModelsConfig 是静态模型注册表配置。
type ModelsConfig = internalconfig.ModelsConfig

// ModelEntry 是单个静态模型定义。
type ModelEntry = internalconfig.ModelEntry

// ModelThinkingConfig 是模型级 thinking 能力描述。
type ModelThinkingConfig = internalconfig.ModelThinkingConfig

// ModelAlias 是静态模型别名配置。
type ModelAlias = internalconfig.ModelAlias

// GeminiConfig 是 Gemini 协议相关配置。
type GeminiConfig = internalconfig.GeminiConfig

// SafetySetting 是 Gemini safety setting 配置项。
type SafetySetting = internalconfig.SafetySetting

// GeminiThinkingConfig 是 Gemini thinking 总开关配置。
type GeminiThinkingConfig = internalconfig.GeminiThinkingConfig

// LoggingConfig 是日志配置。
type LoggingConfig = internalconfig.LoggingConfig

// =========================
// 可观测性接口
// =========================

// Logger 是系统统一日志接口。
type Logger = observability.Logger

// =========================
// 高级组件
// =========================

// AccessManager 是鉴权管理器。
type AccessManager = access.Manager

// ModelRegistry 是静态模型注册表。
type ModelRegistry = modelspkg.Registry

// Registry 是 Provider 运行时注册表。
type Registry = registrypkg.Registry

// Selector 是 Provider 选择器接口。
type Selector = selectorpkg.Selector

// RelayManager 是 WebSocket 中继管理器。
type RelayManager = wsrelay.Manager

// =========================
// 核心业务类型
// =========================

// Action 表示 Gemini 网关支持的动作类型。
type Action = core.Action

const (
	// ActionGenerateContent 对应 Gemini generateContent。
	ActionGenerateContent = core.ActionGenerateContent
	// ActionStreamGenerateContent 对应 Gemini streamGenerateContent。
	ActionStreamGenerateContent = core.ActionStreamGenerateContent
	// ActionCountTokens 对应 Gemini countTokens。
	ActionCountTokens = core.ActionCountTokens
)

// GatewayRequest 是统一的下游请求抽象。
type GatewayRequest = core.GatewayRequest

// GatewayResponse 是统一的非流式响应抽象。
type GatewayResponse = core.GatewayResponse

// StreamChunk 表示一个流式响应片段。
type StreamChunk = core.StreamChunk

// StreamResult 表示一次流式调用的返回句柄。
type StreamResult = core.StreamResult

// RuntimeProvider 表示一个在线运行时 Provider。
type RuntimeProvider = core.RuntimeProvider

// ProviderEvent 表示一个 Provider 生命周期事件。
type ProviderEvent = core.ProviderEvent

// ProviderEventType 表示 Provider 生命周期事件类型。
type ProviderEventType = core.ProviderEventType

// ProviderState 表示运行时 Provider 状态。
type ProviderState = core.ProviderState

// ProviderCapability 表示 Provider 的能力声明。
type ProviderCapability = core.ProviderCapability

// ModelInfo 表示静态模型元信息。
type ModelInfo = core.ModelInfo

// ThinkingSupport 描述模型支持的 thinking 能力。
type ThinkingSupport = core.ThinkingSupport

// GatewayError 是系统统一错误对象。
type GatewayError = core.GatewayError

// ErrorCode 是统一错误码。
type ErrorCode = core.ErrorCode

const (
	ErrorCodeRequest             = core.ErrorCodeRequest
	ErrorCodeAccess              = core.ErrorCodeAccess
	ErrorCodeProviderUnavailable = core.ErrorCodeProviderUnavailable
	ErrorCodeProviderCooldown    = core.ErrorCodeProviderCooldown
	ErrorCodeUpstreamHTTP        = core.ErrorCodeUpstreamHTTP
	ErrorCodeUpstreamProtocol    = core.ErrorCodeUpstreamProtocol
	ErrorCodeStreamBootstrap     = core.ErrorCodeStreamBootstrap
	ErrorCodeConfig              = core.ErrorCodeConfig
	ErrorCodeInternal            = core.ErrorCodeInternal
)

// =========================
// 配置与工具函数
// =========================

// DefaultConfig 返回完整默认配置。
func DefaultConfig() *Config {
	return internalconfig.DefaultConfig()
}

// ExampleConfig 返回示例配置。
func ExampleConfig() *Config {
	return internalconfig.ExampleConfig()
}

// ExampleYAML 返回示例 YAML。
func ExampleYAML() (string, error) {
	return internalconfig.ExampleYAML()
}

// LoadConfig 从文件加载配置。
func LoadConfig(path string) (*Config, error) {
	return internalconfig.Load(path)
}

// LoadConfigOptional 从文件加载配置；当 optional=true 且文件不存在时返回默认配置。
func LoadConfigOptional(path string, optional bool) (*Config, error) {
	return internalconfig.LoadOptional(path, optional)
}

// LoadConfigBootstrap 按“文件优先加载，再用环境变量覆盖”的策略加载配置。
// 规则：
// 1. 若配置文件存在，则先加载文件，再应用环境变量覆盖
// 2. 若配置文件不存在，则从默认配置出发，应用环境变量，并在命中环境变量时尝试自生成 config.yaml
// 3. 若环境变量和配置文件同时存在，则环境变量优先覆盖
func LoadConfigBootstrap(path string, optional bool) (*Config, error) {
	return internalconfig.LoadWithEnvBootstrap(path, optional)
}

// LoadConfigReader 从 reader 加载配置。
func LoadConfigReader(r io.Reader) (*Config, error) {
	return internalconfig.LoadReader(r)
}

// LoadConfigBytes 从 YAML 字节加载配置。
func LoadConfigBytes(data []byte) (*Config, error) {
	return internalconfig.LoadBytes(data)
}

// PrepareConfig 对配置执行 Clone -> ApplyDefaults -> Normalize -> Validate。
func PrepareConfig(cfg *Config) (*Config, error) {
	return internalconfig.Prepare(cfg)
}

// ParseDurationOrDefault 解析 duration，失败时返回 fallback。
// 注意：内部实现已兼容“纯数字按秒解释”，例如：30 -> 30s。
func ParseDurationOrDefault(value string, fallback time.Duration) (time.Duration, error) {
	return internalconfig.ParseDurationOrDefault(value, fallback)
}

// =========================
// 统一错误构造器
// =========================

var (
	NewError                    = core.NewError
	NewRequestError             = core.NewRequestError
	NewAccessError              = core.NewAccessError
	NewProviderUnavailableError = core.NewProviderUnavailableError
	NewProviderCooldownError    = core.NewProviderCooldownError
	NewUpstreamHTTPError        = core.NewUpstreamHTTPError
	NewUpstreamProtocolError    = core.NewUpstreamProtocolError
	NewStreamBootstrapError     = core.NewStreamBootstrapError
	NewConfigError              = core.NewConfigError
	NewInternalError            = core.NewInternalError
	WithPublicMessage           = core.WithPublicMessage
	WithStatusCode              = core.WithStatusCode
	WithRetryable               = core.WithRetryable
	WithCooldown                = core.WithCooldown
	WithRetryAfter              = core.WithRetryAfter
	WithProviderID              = core.WithProviderID
	WithModel                   = core.WithModel
	WithAction                  = core.WithAction
	WithMetadata                = core.WithMetadata
	WithRawBody                 = core.WithRawBody
)
