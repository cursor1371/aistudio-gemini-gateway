package service

import (
	"net/http"
	"net/url"
	"time"
)

// =========================
// 动作
// =========================

// Action 表示 Gemini 网关支持的动作类型。
type Action string

const (
	// ActionGenerateContent 对应 Gemini generateContent。
	ActionGenerateContent Action = "generateContent"
	// ActionStreamGenerateContent 对应 Gemini streamGenerateContent。
	ActionStreamGenerateContent Action = "streamGenerateContent"
	// ActionCountTokens 对应 Gemini countTokens。
	ActionCountTokens Action = "countTokens"
)

// Valid 判断动作是否合法。
func (a Action) Valid() bool {
	switch a {
	case ActionGenerateContent, ActionStreamGenerateContent, ActionCountTokens:
		return true
	default:
		return false
	}
}

// IsStreaming 判断该动作是否为流式动作。
func (a Action) IsStreaming() bool {
	return a == ActionStreamGenerateContent
}

// String 返回动作字符串。
func (a Action) String() string {
	return string(a)
}

// =========================
// 请求与响应
// =========================

// GatewayRequest 是统一的下游请求抽象。
type GatewayRequest struct {
	// RequestID 是请求唯一标识。
	RequestID string
	// Model 是请求的模型名，可能包含 models/ 前缀或 thinking suffix。
	Model string
	// Action 是请求动作。
	Action Action
	// Headers 是下游请求头副本。
	Headers http.Header
	// Query 是下游请求查询参数副本。
	Query url.Values
	// Payload 是请求体原始字节。
	Payload []byte
	// Metadata 是请求附加元数据，用于承载鉴权信息等上下文。
	Metadata map[string]any
	// SessionID 是会话标识，用于 session affinity。
	SessionID string
	// Alt 是 Gemini alt 参数，控制流式输出格式。
	Alt string
	// RequestedAt 是请求到达时间。
	RequestedAt time.Time
}

// Clone 深拷贝请求对象。
func (r *GatewayRequest) Clone() *GatewayRequest {
	if r == nil {
		return nil
	}
	out := *r
	out.Headers = cloneHeader(r.Headers)
	out.Query = cloneValues(r.Query)
	out.Payload = cloneBytes(r.Payload)
	out.Metadata = cloneAnyMap(r.Metadata)
	return &out
}

// IsStreaming 判断请求是否为流式请求。
func (r *GatewayRequest) IsStreaming() bool {
	if r == nil {
		return false
	}
	return r.Action.IsStreaming()
}

// GatewayResponse 是统一的非流式响应抽象。
type GatewayResponse struct {
	// RequestID 是对应请求的唯一标识。
	RequestID string
	// StatusCode 是 HTTP 状态码。
	StatusCode int
	// Headers 是响应头。
	Headers http.Header
	// Payload 是响应体原始字节。
	Payload []byte
	// Metadata 是响应附加元数据。
	Metadata map[string]any
	// ReceivedAt 是响应接收时间。
	ReceivedAt time.Time
}

// Clone 深拷贝响应对象。
func (r *GatewayResponse) Clone() *GatewayResponse {
	if r == nil {
		return nil
	}
	out := *r
	out.Headers = cloneHeader(r.Headers)
	out.Payload = cloneBytes(r.Payload)
	out.Metadata = cloneAnyMap(r.Metadata)
	return &out
}

// StreamChunk 表示一个流式响应片段。
// 若 Err 非 nil，则表示流式过程中的终止错误。
type StreamChunk struct {
	// Payload 是流式数据块。
	Payload []byte
	// Metadata 是该 chunk 的附加元数据。
	Metadata map[string]any
	// Err 是该 chunk 携带的错误（非 nil 时表示流终止）。
	Err error
}

// Clone 深拷贝流式片段。
func (c StreamChunk) Clone() StreamChunk {
	return StreamChunk{
		Payload:  cloneBytes(c.Payload),
		Metadata: cloneAnyMap(c.Metadata),
		Err:      c.Err,
	}
}

// StreamResult 表示一次流式调用的返回句柄。
type StreamResult struct {
	// RequestID 是对应请求的唯一标识。
	RequestID string
	// StatusCode 是首事件确定的 HTTP 状态码。
	StatusCode int
	// Headers 是首事件确定的响应头。
	Headers http.Header
	// Chunks 是流式 chunk 通道。
	Chunks <-chan StreamChunk
}

// =========================
// Provider 运行时
// =========================

// ProviderState 表示运行时 Provider 状态。
type ProviderState string

const (
	// ProviderStateActive 表示 Provider 正常可用。
	ProviderStateActive ProviderState = "active"
	// ProviderStateCooling 表示 Provider 处于冷却中。
	ProviderStateCooling ProviderState = "cooling"
	// ProviderStateDisabled 表示 Provider 被手动禁用。
	ProviderStateDisabled ProviderState = "disabled"
	// ProviderStateDisconnected 表示 Provider 已断开。
	ProviderStateDisconnected ProviderState = "disconnected"
)

// Valid 判断 Provider 状态是否合法。
func (s ProviderState) Valid() bool {
	switch s {
	case ProviderStateActive, ProviderStateCooling, ProviderStateDisabled, ProviderStateDisconnected:
		return true
	default:
		return false
	}
}

// ProviderCapability 表示 Provider 的能力声明。
type ProviderCapability string

const (
	// ProviderCapabilityGenerateContent 表示支持非流式内容生成。
	ProviderCapabilityGenerateContent ProviderCapability = "generate_content"
	// ProviderCapabilityStreamGenerateContent 表示支持流式内容生成。
	ProviderCapabilityStreamGenerateContent ProviderCapability = "stream_generate_content"
	// ProviderCapabilityCountTokens 表示支持 Token 计数。
	ProviderCapabilityCountTokens ProviderCapability = "count_tokens"
)

// Valid 判断能力声明是否合法。
func (c ProviderCapability) Valid() bool {
	switch c {
	case ProviderCapabilityGenerateContent, ProviderCapabilityStreamGenerateContent, ProviderCapabilityCountTokens:
		return true
	default:
		return false
	}
}

// ProviderMetadata 是 Provider 的附加元数据。
type ProviderMetadata struct {
	// ProviderType 标识 Provider 类型，如 "aistudio"。
	ProviderType string
	// RequestHeaders 是 Provider 连接时声明的额外上游请求头。
	RequestHeaders map[string]string
	// Attributes 是连接期提取的身份属性。
	Attributes map[string]string
	// Extra 是任意扩展元数据。
	Extra map[string]any
}

// Clone 深拷贝元数据。
func (m *ProviderMetadata) Clone() *ProviderMetadata {
	if m == nil {
		return nil
	}
	out := *m
	out.RequestHeaders = cloneStringMap(m.RequestHeaders)
	out.Attributes = cloneStringMap(m.Attributes)
	out.Extra = cloneAnyMap(m.Extra)
	return &out
}

// RuntimeProvider 表示一个在线运行时 Provider。
// Provider 仅表达：是否在线、是否冷却、执行优先级、身份标签等。
// 模型支持范围统一由静态模型注册表决定，Provider 不参与模型声明。
type RuntimeProvider struct {
	// ID 是 Provider 唯一标识。
	ID string
	// ConnectionID 是当前连接的唯一标识。
	ConnectionID string
	// Label 是 Provider 的可读标签。
	Label string
	// State 是当前运行状态。
	State ProviderState
	// Priority 是选路优先级，数值越大优先级越高。
	Priority int
	// ConnectedAt 是连接建立时间。
	ConnectedAt time.Time
	// LastSeenAt 是最近活跃时间。
	LastSeenAt time.Time
	// CooldownUntil 是冷却截止时间。
	CooldownUntil time.Time
	// Capabilities 是 Provider 声明的能力列表。
	Capabilities []ProviderCapability
	// Tags 是 Provider 的标签列表。
	Tags []string
	// Metadata 是 Provider 的附加元数据。
	Metadata ProviderMetadata
}

// Clone 深拷贝 Provider。
func (p *RuntimeProvider) Clone() *RuntimeProvider {
	if p == nil {
		return nil
	}
	out := *p
	out.Capabilities = cloneCapabilities(p.Capabilities)
	out.Tags = cloneStringSlice(p.Tags)
	out.Metadata = ProviderMetadata{}
	if cloned := p.Metadata.Clone(); cloned != nil {
		out.Metadata = *cloned
	}
	return &out
}

// =========================
// Provider 生命周期事件
// =========================

// ProviderEventType 表示 Provider 生命周期事件类型。
type ProviderEventType string

const (
	// ProviderEventConnected 表示 Provider 连接成功。
	ProviderEventConnected ProviderEventType = "connected"
	// ProviderEventDisconnected 表示 Provider 已断开。
	ProviderEventDisconnected ProviderEventType = "disconnected"
	// ProviderEventReplaced 表示 Provider 连接被新连接替换。
	ProviderEventReplaced ProviderEventType = "replaced"
	// ProviderEventStateChanged 表示 Provider 状态发生变更。
	ProviderEventStateChanged ProviderEventType = "state_changed"
)

// ProviderEvent 表示一个 Provider 生命周期事件。
type ProviderEvent struct {
	// Type 是事件类型。
	Type ProviderEventType
	// Provider 是事件关联的 Provider 快照。
	Provider *RuntimeProvider
	// At 是事件发生时间。
	At time.Time
	// Message 是事件描述信息。
	Message string
	// Metadata 是事件附加元数据。
	Metadata map[string]any
}

// Clone 深拷贝 Provider 事件。
func (e *ProviderEvent) Clone() *ProviderEvent {
	if e == nil {
		return nil
	}
	out := *e
	out.Provider = e.Provider.Clone()
	out.Metadata = cloneAnyMap(e.Metadata)
	return &out
}

// =========================
// 模型信息
// =========================

// ThinkingSupport 描述模型支持的 thinking 能力。
type ThinkingSupport struct {
	// Min 是允许的最小 thinking budget。
	Min int64 `json:"min,omitempty"`
	// Max 是允许的最大 thinking budget。
	Max int64 `json:"max,omitempty"`
	// ZeroAllowed 标记是否允许 budget 为 0（即关闭思考但保留结构）。
	ZeroAllowed bool `json:"zero_allowed,omitempty"`
	// DynamicAllowed 标记是否允许 auto/dynamic 模式。
	DynamicAllowed bool `json:"dynamic_allowed,omitempty"`
	// Levels 是模型支持的离散 thinking level 列表。
	Levels []string `json:"levels,omitempty"`
}

// Clone 深拷贝 thinking 支持信息。
func (s *ThinkingSupport) Clone() *ThinkingSupport {
	if s == nil {
		return nil
	}
	out := *s
	out.Levels = cloneStringSlice(s.Levels)
	return &out
}

// ModelInfo 表示静态模型元信息。
// 该结构只描述模型定义，不承载运行时可用性状态。
type ModelInfo struct {
	// Name 是面向 Gemini 风格接口返回的名字，通常形如 models/gemini-2.5-pro。
	Name string
	// BaseName 是内部使用的基础模型名，不带 models/ 前缀。
	BaseName string

	// DisplayName 是模型的展示名。
	DisplayName string
	// Description 是模型描述。
	Description string
	// InputTokenLimit 是输入 Token 上限。
	InputTokenLimit int64
	// OutputTokenLimit 是输出 Token 上限。
	OutputTokenLimit int64

	// SupportedActions 是对内部执行器友好的归一化动作列表。
	SupportedActions []Action
	// SupportsThinking 标记该模型是否支持 thinking。
	SupportsThinking bool

	// Object 是 Gemini 模型对象类型，通常为 "model"。
	Object string
	// Created 是模型创建时间戳。
	Created int64
	// OwnedBy 是模型归属方。
	OwnedBy string
	// Type 是模型协议类型，通常为 "gemini"。
	Type string
	// Version 是模型版本号。
	Version string
	// ContextLength 是上下文窗口长度。
	ContextLength int64
	// MaxCompletionTokens 是最大生成 Token 数。
	MaxCompletionTokens int64
	// SupportedGenerationMethods 是 Gemini API 兼容的生成方法列表。
	SupportedGenerationMethods []string
	// SupportedParameters 是模型支持的参数列表。
	SupportedParameters []string
	// SupportedInputModalities 是支持的输入模态列表。
	SupportedInputModalities []string
	// SupportedOutputModalities 是支持的输出模态列表。
	SupportedOutputModalities []string
	// Thinking 是模型的 thinking 能力描述。
	Thinking *ThinkingSupport
	// UserDefined 标记该模型是否由用户自定义。
	UserDefined bool

	// Metadata 是任意扩展元数据。
	Metadata map[string]any
}

// Clone 深拷贝模型信息。
func (m *ModelInfo) Clone() *ModelInfo {
	if m == nil {
		return nil
	}
	out := *m
	out.SupportedActions = cloneActions(m.SupportedActions)
	out.SupportedGenerationMethods = cloneStringSlice(m.SupportedGenerationMethods)
	out.SupportedParameters = cloneStringSlice(m.SupportedParameters)
	out.SupportedInputModalities = cloneStringSlice(m.SupportedInputModalities)
	out.SupportedOutputModalities = cloneStringSlice(m.SupportedOutputModalities)
	out.Metadata = cloneAnyMap(m.Metadata)
	out.Thinking = cloneThinkingSupport(m.Thinking)
	return &out
}

// =========================
// 内部工具函数
// =========================

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func cloneHeader(in http.Header) http.Header {
	if len(in) == 0 {
		return nil
	}
	out := make(http.Header, len(in))
	for k, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[k] = copied
	}
	return out
}

func cloneValues(in url.Values) url.Values {
	if len(in) == 0 {
		return nil
	}
	out := make(url.Values, len(in))
	for k, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[k] = copied
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneCapabilities(in []ProviderCapability) []ProviderCapability {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProviderCapability, len(in))
	copy(out, in)
	return out
}

func cloneActions(in []Action) []Action {
	if len(in) == 0 {
		return nil
	}
	out := make([]Action, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCloneAny(v)
	}
	return out
}

func cloneThinkingSupport(in *ThinkingSupport) *ThinkingSupport {
	if in == nil {
		return nil
	}
	return in.Clone()
}

func deepCloneAny(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case []byte:
		return cloneBytes(typed)
	case []string:
		return cloneStringSlice(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = deepCloneAny(typed[i])
		}
		return out
	case map[string]any:
		return cloneAnyMap(typed)
	case map[string]string:
		return cloneStringMap(typed)
	case http.Header:
		return cloneHeader(typed)
	case url.Values:
		return cloneValues(typed)
	default:
		return typed
	}
}
