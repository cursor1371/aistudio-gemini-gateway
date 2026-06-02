package config

// Config 是网关根配置。
// 包含 7 个顶层区块：server、access、websocket、routing、models、gemini、logging。
type Config struct {
	Server    ServerConfig    `yaml:"server" json:"server"`
	Access    AccessConfig    `yaml:"access" json:"access"`
	WebSocket WebSocketConfig `yaml:"websocket" json:"websocket"`
	Routing   RoutingConfig   `yaml:"routing" json:"routing"`
	Models    ModelsConfig    `yaml:"models" json:"models"`
	Gemini    GeminiConfig    `yaml:"gemini" json:"gemini"`
	Logging   LoggingConfig   `yaml:"logging" json:"logging"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// ServerConfig 是 HTTP 服务监听配置。
type ServerConfig struct {
	// Host 监听地址，默认 127.0.0.1。
	Host string `yaml:"host" json:"host"`
	// Port 监听端口，默认 8080。
	Port int `yaml:"port" json:"port"`
	// ReadTimeout 读取请求头/请求体超时，默认 30s。
	ReadTimeout string `yaml:"read-timeout" json:"read-timeout"`
	// WriteTimeout 写响应超时，默认 0s（不限制），避免 SSE 长流被截断。
	WriteTimeout string `yaml:"write-timeout" json:"write-timeout"`
	// IdleTimeout Keep-Alive 空闲超时，默认 120s。
	IdleTimeout string `yaml:"idle-timeout" json:"idle-timeout"`
	// ShutdownTimeout 优雅关闭超时，默认 15s。
	ShutdownTimeout string `yaml:"shutdown-timeout" json:"shutdown-timeout"`
	// TLS HTTPS 配置。
	TLS TLSConfig `yaml:"tls" json:"tls"`
}

// TLSConfig 是 HTTPS 证书配置。
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"cert-file" json:"cert-file"`
	KeyFile  string `yaml:"key-file" json:"key-file"`
}

// ---------------------------------------------------------------------------
// Access
// ---------------------------------------------------------------------------

// AccessConfig 是下游 HTTP API 访问控制配置。
type AccessConfig struct {
	// HTTP 静态 Key 鉴权策略。
	HTTP AccessPolicy `yaml:"http" json:"http"`
	// CORS 跨域配置。
	CORS CORSConfig `yaml:"cors" json:"cors"`
}

// AccessPolicy 描述基于静态密钥的认证策略。
// 支持 Bearer / Header / Query 三种提取方式。
type AccessPolicy struct {
	// Enabled 是否启用鉴权。
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Keys 允许的密钥列表。
	Keys []string `yaml:"keys,omitempty" json:"keys,omitempty"`
	// AllowBearer 是否允许 Authorization: Bearer <key>。
	AllowBearer bool `yaml:"allow-bearer" json:"allow-bearer"`
	// HeaderNames 从请求头中取密钥的字段名列表。
	HeaderNames []string `yaml:"header-names,omitempty" json:"header-names,omitempty"`
	// QueryNames 从 URL 查询参数中取密钥的字段名列表。
	QueryNames []string `yaml:"query-names,omitempty" json:"query-names,omitempty"`
}

// CORSConfig 是 CORS 跨域配置。
type CORSConfig struct {
	Enabled          bool     `yaml:"enabled" json:"enabled"`
	AllowedOrigins   []string `yaml:"allowed-origins,omitempty" json:"allowed-origins,omitempty"`
	AllowedMethods   []string `yaml:"allowed-methods,omitempty" json:"allowed-methods,omitempty"`
	AllowedHeaders   []string `yaml:"allowed-headers,omitempty" json:"allowed-headers,omitempty"`
	ExposeHeaders    []string `yaml:"expose-headers,omitempty" json:"expose-headers,omitempty"`
	AllowCredentials bool     `yaml:"allow-credentials" json:"allow-credentials"`
	MaxAgeSeconds    int      `yaml:"max-age-seconds" json:"max-age-seconds"`
}

// ---------------------------------------------------------------------------
// WebSocket
// ---------------------------------------------------------------------------

// WebSocketConfig 是 Provider WebSocket 接入配置。
type WebSocketConfig struct {
	// Path WS 升级路径，默认 /v1/ws。
	Path string `yaml:"path" json:"path"`
	// HandshakeTimeout 握手超时，默认 10s。
	HandshakeTimeout string `yaml:"handshake-timeout" json:"handshake-timeout"`
	// ReadTimeout WS 消息读取超时，默认 300s。
	ReadTimeout string `yaml:"read-timeout" json:"read-timeout"`
	// WriteTimeout WS 消息写入超时，默认 10s。
	WriteTimeout string `yaml:"write-timeout" json:"write-timeout"`
	// MaxMessageSize 单条 WS 消息最大字节数，默认 64 MiB。
	MaxMessageSize int64 `yaml:"max-message-size" json:"max-message-size"`
	// Heartbeat 心跳配置。
	Heartbeat HeartbeatConfig `yaml:"heartbeat" json:"heartbeat"`
	// Origin Origin 校验策略。
	Origin OriginPolicyConfig `yaml:"origin" json:"origin"`
	// Auth WS 握手鉴权策略。
	Auth AccessPolicy `yaml:"auth" json:"auth"`
	// ProviderIdentity Provider 身份元数据提取来源。
	ProviderIdentity ProviderIdentityConfig `yaml:"provider-identity" json:"provider-identity"`
}

// HeartbeatConfig 是 WS 心跳配置。
type HeartbeatConfig struct {
	// PingInterval 服务端主动 Ping 间隔，默认 30s。
	PingInterval string `yaml:"ping-interval" json:"ping-interval"`
	// PongTimeout 等待 Pong 的超时时间，默认 120s。
	PongTimeout string `yaml:"pong-timeout" json:"pong-timeout"`
}

// OriginPolicyConfig 是 WS Origin 校验策略。
// Mode 支持：any / allow-missing / strict。
type OriginPolicyConfig struct {
	Mode           string   `yaml:"mode" json:"mode"`
	AllowedOrigins []string `yaml:"allowed-origins,omitempty" json:"allowed-origins,omitempty"`
}

// ProviderIdentityConfig 定义 Provider 身份元数据的提取来源。
type ProviderIdentityConfig struct {
	IDHeaderNames       []string `yaml:"id-header-names,omitempty" json:"id-header-names,omitempty"`
	IDQueryNames        []string `yaml:"id-query-names,omitempty" json:"id-query-names,omitempty"`
	LabelHeaderNames    []string `yaml:"label-header-names,omitempty" json:"label-header-names,omitempty"`
	LabelQueryNames     []string `yaml:"label-query-names,omitempty" json:"label-query-names,omitempty"`
	TagsHeaderNames     []string `yaml:"tags-header-names,omitempty" json:"tags-header-names,omitempty"`
	TagsQueryNames      []string `yaml:"tags-query-names,omitempty" json:"tags-query-names,omitempty"`
	PriorityHeaderNames []string `yaml:"priority-header-names,omitempty" json:"priority-header-names,omitempty"`
	PriorityQueryNames  []string `yaml:"priority-query-names,omitempty" json:"priority-query-names,omitempty"`
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

// RoutingConfig 是 Provider 选择与重试配置。
type RoutingConfig struct {
	// Strategy 选路策略：round_robin / fill_first / session_affinity。
	Strategy string `yaml:"strategy" json:"strategy"`
	// SessionAffinityTTL 会话绑定保留时间，默认 1h。
	SessionAffinityTTL string `yaml:"session-affinity-ttl" json:"session-affinity-ttl"`
	// BootstrapRetries 指首字节重试策略参考次数（实际重试次数由可用 Provider 数量决定）。
	BootstrapRetries int `yaml:"bootstrap-retries" json:"bootstrap-retries"`
	// ProviderCooldown Provider 冷却时间，默认 5m。
	ProviderCooldown string `yaml:"provider-cooldown" json:"provider-cooldown"`

	// BootstrapTimeout 启动首包超时，默认 60s。
	// 从网关开始向 Provider 发送请求，到收到第一个有效 Gemini 数据包的最长等待时间。
	BootstrapTimeout string `yaml:"bootstrap-timeout" json:"bootstrap-timeout"`

	// StreamIdleTimeout 流式空闲中断超时，默认 90s。
	// 流式输出正在传输过程中，相邻两个数据块（chunk/event）之间的最长空闲间隔。
	StreamIdleTimeout string `yaml:"stream-idle-timeout" json:"stream-idle-timeout"`

	// NonStreamTimeout 非流式总体执行超时，默认 600s。
	// 实用于 generateContent（非流式）和 countTokens 的单次请求整体耗时上限。
	NonStreamTimeout string `yaml:"non-stream-timeout" json:"non-stream-timeout"`
}

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// ModelsConfig 是静态模型注册表配置。
type ModelsConfig struct {
	// Source 模型来源：embedded（内置） / custom（自定义）。
	Source string `yaml:"source" json:"source"`
	// Entries 自定义模型列表，仅在 Source=custom 时使用。
	Entries []ModelEntry `yaml:"entries,omitempty" json:"entries,omitempty"`
	// Aliases 模型别名列表。
	Aliases []ModelAlias `yaml:"aliases,omitempty" json:"aliases,omitempty"`
}

// ModelEntry 是单个静态模型定义。
type ModelEntry struct {
	// Name 模型基础名称（不含 models/ 前缀），如 gemini-2.5-pro。
	Name string `yaml:"name" json:"name"`
	// DisplayName 模型显示名称。
	DisplayName string `yaml:"display-name,omitempty" json:"display-name,omitempty"`
	// Description 模型描述。
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Version 模型版本号。
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// InputTokenLimit 输入 Token 上限。
	InputTokenLimit int64 `yaml:"input-token-limit,omitempty" json:"input-token-limit,omitempty"`
	// OutputTokenLimit 输出 Token 上限。
	OutputTokenLimit int64 `yaml:"output-token-limit,omitempty" json:"output-token-limit,omitempty"`
	// SupportedActions 支持的动作列表，如 generateContent / streamGenerateContent / countTokens。
	SupportedActions []string `yaml:"supported-actions,omitempty" json:"supported-actions,omitempty"`
	// SupportedGenerationMethods 支持的 Gemini generation methods。
	SupportedGenerationMethods []string `yaml:"supported-generation-methods,omitempty" json:"supported-generation-methods,omitempty"`
	// SupportedInputModalities 支持的输入模态，如 TEXT / IMAGE。
	SupportedInputModalities []string `yaml:"supported-input-modalities,omitempty" json:"supported-input-modalities,omitempty"`
	// SupportedOutputModalities 支持的输出模态，如 TEXT。
	SupportedOutputModalities []string `yaml:"supported-output-modalities,omitempty" json:"supported-output-modalities,omitempty"`
	// Thinking 模型 thinking 能力描述，为 nil 表示不支持。
	Thinking *ModelThinkingConfig `yaml:"thinking,omitempty" json:"thinking,omitempty"`
}

// ModelThinkingConfig 是模型级 thinking 能力描述。
type ModelThinkingConfig struct {
	// Min thinking budget 最小值。
	Min int64 `yaml:"min,omitempty" json:"min,omitempty"`
	// Max thinking budget 最大值。
	Max int64 `yaml:"max,omitempty" json:"max,omitempty"`
	// ZeroAllowed 是否允许 budget=0（关闭 thinking 但保持兼容）。
	ZeroAllowed bool `yaml:"zero-allowed,omitempty" json:"zero-allowed,omitempty"`
	// DynamicAllowed 是否允许 auto / -1 等动态模式。
	DynamicAllowed bool `yaml:"dynamic-allowed,omitempty" json:"dynamic-allowed,omitempty"`
	// Levels 支持的离散 thinking level 列表，如 minimal / low / medium / high。
	Levels []string `yaml:"levels,omitempty" json:"levels,omitempty"`
}

// ModelAlias 是静态模型别名配置。
type ModelAlias struct {
	// Alias 对外别名。
	Alias string `yaml:"alias" json:"alias"`
	// Target 目标物理模型名。
	Target string `yaml:"target" json:"target"`
	// Expose 是否在 /v1beta/models 列表中显示该别名。
	Expose bool `yaml:"expose,omitempty" json:"expose,omitempty"`
}

// ---------------------------------------------------------------------------
// Gemini
// ---------------------------------------------------------------------------

// GeminiConfig 是 Gemini 协议相关配置。
type GeminiConfig struct {
	// APIVersion Gemini API 版本前缀，默认 v1beta。
	APIVersion string `yaml:"api-version" json:"api-version"`
	// SafetyDefaultsMode 默认 safetySettings 注入模式：auto / off。
	SafetyDefaultsMode string `yaml:"safety-defaults-mode" json:"safety-defaults-mode"`
	// DefaultSafetySettings 默认 safety setting 列表。
	DefaultSafetySettings []SafetySetting `yaml:"default-safety-settings,omitempty" json:"default-safety-settings,omitempty"`
	// ImagePreviewCompatibility 是否启用 Gemini image preview 兼容修复。
	ImagePreviewCompatibility bool `yaml:"image-preview-compatibility" json:"image-preview-compatibility"`
	// Thinking thinking 总开关配置。
	Thinking GeminiThinkingConfig `yaml:"thinking" json:"thinking"`
}

// SafetySetting 是 Gemini safety setting 配置项。
type SafetySetting struct {
	Category  string `yaml:"category" json:"category"`
	Threshold string `yaml:"threshold" json:"threshold"`
}

// GeminiThinkingConfig 是 Gemini thinking 处理开关。
type GeminiThinkingConfig struct {
	// Mode 处理模式：auto（启用）/ off（关闭）。
	Mode string `yaml:"mode" json:"mode"`
	// StrictValidation 是否启用严格校验。
	StrictValidation bool `yaml:"strict-validation" json:"strict-validation"`
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

// LoggingConfig 是日志配置。
type LoggingConfig struct {
	// Level 日志级别：debug / info / warn / error。
	Level string `yaml:"level" json:"level"`
	// Format 日志格式：text / json。
	Format string `yaml:"format" json:"format"`
	// AddSource 是否在日志中附带源码位置信息。
	AddSource bool `yaml:"add-source" json:"add-source"`
	// AccessLog 是否启用 HTTP 访问日志，默认关闭。
	AccessLog bool `yaml:"access-log" json:"access-log"`
}