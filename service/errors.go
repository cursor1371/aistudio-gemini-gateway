package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrorCode 是统一错误码。
type ErrorCode string

const (
	// ErrorCodeRequest 表示请求参数不合法。
	ErrorCodeRequest ErrorCode = "request_error"
	// ErrorCodeAccess 表示认证失败。
	ErrorCodeAccess ErrorCode = "access_error"
	// ErrorCodeProviderUnavailable 表示当前没有可用的执行通道。
	ErrorCodeProviderUnavailable ErrorCode = "provider_unavailable"
	// ErrorCodeProviderCooldown 表示执行通道处于冷却中。
	ErrorCodeProviderCooldown ErrorCode = "provider_cooldown"
	// ErrorCodeUpstreamHTTP 表示上游服务返回 HTTP 错误。
	ErrorCodeUpstreamHTTP ErrorCode = "upstream_http_error"
	// ErrorCodeUpstreamProtocol 表示上游协议错误。
	ErrorCodeUpstreamProtocol ErrorCode = "upstream_protocol_error"
	// ErrorCodeStreamBootstrap 表示流式请求初始化失败。
	ErrorCodeStreamBootstrap ErrorCode = "stream_bootstrap_error"
	// ErrorCodeConfig 表示服务配置错误。
	ErrorCodeConfig ErrorCode = "config_error"
	// ErrorCodeInternal 表示内部服务错误。
	ErrorCodeInternal ErrorCode = "internal_error"
)

// DefaultStatus 返回错误码对应的默认 HTTP 状态码。
func (c ErrorCode) DefaultStatus() int {
	switch c {
	case ErrorCodeRequest:
		return http.StatusBadRequest
	case ErrorCodeAccess:
		return http.StatusUnauthorized
	case ErrorCodeProviderUnavailable:
		return http.StatusServiceUnavailable
	case ErrorCodeProviderCooldown:
		return http.StatusTooManyRequests
	case ErrorCodeUpstreamHTTP:
		return http.StatusBadGateway
	case ErrorCodeUpstreamProtocol:
		return http.StatusBadGateway
	case ErrorCodeStreamBootstrap:
		return http.StatusBadGateway
	case ErrorCodeConfig:
		return http.StatusInternalServerError
	case ErrorCodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// DefaultPublicMessage 返回面向外部的默认安全消息。
func (c ErrorCode) DefaultPublicMessage() string {
	switch c {
	case ErrorCodeRequest:
		return "请求参数不合法"
	case ErrorCodeAccess:
		return "认证失败"
	case ErrorCodeProviderUnavailable:
		return "当前没有可用的执行通道"
	case ErrorCodeProviderCooldown:
		return "执行通道处于冷却中"
	case ErrorCodeUpstreamHTTP:
		return "上游服务返回错误"
	case ErrorCodeUpstreamProtocol:
		return "上游协议错误"
	case ErrorCodeStreamBootstrap:
		return "流式请求初始化失败"
	case ErrorCodeConfig:
		return "服务配置错误"
	case ErrorCodeInternal:
		return "内部服务错误"
	default:
		return "内部服务错误"
	}
}

// GatewayError 是系统统一错误对象。
// HTTP 层、SDK 层、Pipeline 层都以此为核心错误模型。
type GatewayError struct {
	// Code 是错误码。
	Code ErrorCode
	// Message 是面向开发者的详细内部错误描述。
	Message string
	// Public 是面向外部客户端的安全消息。
	Public string
	// StatusCode 是最终 HTTP 状态码。
	StatusCode int
	// Retryable 标记该错误是否可重试。
	Retryable bool
	// Cooldown 标记是否应触发 Provider 冷却。
	Cooldown bool
	// RetryAfter 建议的重试等待时间。
	RetryAfter time.Duration
	// ProviderID 是出错 Provider 的标识。
	ProviderID string
	// Model 是请求涉及的模型名。
	Model string
	// Action 是请求涉及的动作。
	Action Action
	// Metadata 是附加元数据。
	Metadata map[string]any
	// RawBody 存储上游原始响应体。
	RawBody []byte
	// Cause 是底层原始错误。
	Cause error
}

// Error 实现 error 接口。
func (e *GatewayError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = e.Code.DefaultPublicMessage()
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", msg, e.Cause)
	}
	return msg
}

// Unwrap 返回底层错误。
func (e *GatewayError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HTTPStatus 返回最终 HTTP 状态码。
func (e *GatewayError) HTTPStatus() int {
	if e == nil || e.StatusCode <= 0 {
		return http.StatusInternalServerError
	}
	return e.StatusCode
}

// SafeMessage 返回可安全暴露给客户端的错误消息。
func (e *GatewayError) SafeMessage() string {
	if e == nil {
		return ErrorCodeInternal.DefaultPublicMessage()
	}
	if strings.TrimSpace(e.Public) != "" {
		return strings.TrimSpace(e.Public)
	}
	return e.Code.DefaultPublicMessage()
}

// RetryAfterDuration 返回 Retry-After 时长。
// HTTP 层通过该方法把退避信息映射到响应头。
func (e *GatewayError) RetryAfterDuration() time.Duration {
	if e == nil || e.RetryAfter <= 0 {
		return 0
	}
	return e.RetryAfter
}

// RawResponseBody 返回上游原始响应体。
// 该方法供 HTTP 输出层判断是否应透传上游完整错误。
func (e *GatewayError) RawResponseBody() []byte {
	if e == nil {
		return nil
	}
	return e.RawBody
}

// Clone 深拷贝错误对象。
func (e *GatewayError) Clone() *GatewayError {
	if e == nil {
		return nil
	}
	out := *e
	if len(e.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(e.Metadata))
		for k, v := range e.Metadata {
			out.Metadata[k] = v
		}
	}
	if len(e.RawBody) > 0 {
		out.RawBody = make([]byte, len(e.RawBody))
		copy(out.RawBody, e.RawBody)
	}
	return &out
}

// ErrorOption 用于构造错误对象时注入附加属性。
type ErrorOption func(*GatewayError)

// WithPublicMessage 设置对外安全消息。
func WithPublicMessage(msg string) ErrorOption {
	return func(e *GatewayError) {
		e.Public = strings.TrimSpace(msg)
	}
}

// WithStatusCode 设置 HTTP 状态码。
func WithStatusCode(code int) ErrorOption {
	return func(e *GatewayError) {
		if code > 0 {
			e.StatusCode = code
		}
	}
}

// WithRetryable 设置是否可重试。
func WithRetryable(retryable bool) ErrorOption {
	return func(e *GatewayError) {
		e.Retryable = retryable
	}
}

// WithCooldown 设置是否应触发 Provider 冷却。
func WithCooldown(cooldown bool) ErrorOption {
	return func(e *GatewayError) {
		e.Cooldown = cooldown
	}
}

// WithRetryAfter 设置 Retry-After 时长。
func WithRetryAfter(delay time.Duration) ErrorOption {
	return func(e *GatewayError) {
		if delay > 0 {
			e.RetryAfter = delay
		}
	}
}

// WithProviderID 设置 ProviderID。
func WithProviderID(providerID string) ErrorOption {
	return func(e *GatewayError) {
		e.ProviderID = strings.TrimSpace(providerID)
	}
}

// WithModel 设置模型。
func WithModel(model string) ErrorOption {
	return func(e *GatewayError) {
		e.Model = strings.TrimSpace(model)
	}
}

// WithAction 设置动作。
func WithAction(action Action) ErrorOption {
	return func(e *GatewayError) {
		e.Action = action
	}
}

// WithMetadata 设置附加元数据。
func WithMetadata(metadata map[string]any) ErrorOption {
	return func(e *GatewayError) {
		if len(metadata) == 0 {
			return
		}
		e.Metadata = make(map[string]any, len(metadata))
		for k, v := range metadata {
			e.Metadata[k] = v
		}
	}
}

// WithRawBody 设置上游原始响应体。
// 当上游返回非 2xx 响应时，可通过此选项携带完整的上游响应体，
// 使 HTTP 输出层能够直接透传给客户端。
func WithRawBody(body []byte) ErrorOption {
	return func(e *GatewayError) {
		if len(body) > 0 {
			e.RawBody = make([]byte, len(body))
			copy(e.RawBody, body)
		}
	}
}

// NewError 创建统一错误。
func NewError(code ErrorCode, message string, cause error, opts ...ErrorOption) *GatewayError {
	errObj := &GatewayError{
		Code:       code,
		Message:    strings.TrimSpace(message),
		Public:     code.DefaultPublicMessage(),
		StatusCode: code.DefaultStatus(),
		Cause:      cause,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(errObj)
		}
	}
	return errObj
}

// NewRequestError 创建请求参数错误。
func NewRequestError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeRequest, message, cause, opts...)
}

// NewAccessError 创建认证错误。
func NewAccessError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeAccess, message, cause, opts...)
}

// NewProviderUnavailableError 创建 Provider 不可用错误。
func NewProviderUnavailableError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeProviderUnavailable, message, cause, opts...)
}

// NewProviderCooldownError 创建 Provider 冷却错误。
func NewProviderCooldownError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeProviderCooldown, message, cause, opts...)
}

// NewUpstreamHTTPError 创建上游 HTTP 错误。
func NewUpstreamHTTPError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeUpstreamHTTP, message, cause, opts...)
}

// NewUpstreamProtocolError 创建上游协议错误。
func NewUpstreamProtocolError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeUpstreamProtocol, message, cause, opts...)
}

// NewStreamBootstrapError 创建流式启动错误。
func NewStreamBootstrapError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeStreamBootstrap, message, cause, opts...)
}

// NewConfigError 创建配置错误。
func NewConfigError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeConfig, message, cause, opts...)
}

// NewInternalError 创建内部错误。
func NewInternalError(message string, cause error, opts ...ErrorOption) *GatewayError {
	return NewError(ErrorCodeInternal, message, cause, opts...)
}
