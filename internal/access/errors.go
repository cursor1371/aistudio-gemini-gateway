package access

import (
	"fmt"
	"net/http"
	"strings"
)

// AuthErrorCode 表示鉴权失败类型。
type AuthErrorCode string

const (
	// AuthErrorCodeNoCredentials 表示请求未携带凭据。
	AuthErrorCodeNoCredentials AuthErrorCode = "no_credentials"

	// AuthErrorCodeInvalidCredential 表示凭据无效。
	AuthErrorCodeInvalidCredential AuthErrorCode = "invalid_credential"

	// AuthErrorCodeNotHandled 表示当前提供者不处理该请求。
	AuthErrorCodeNotHandled AuthErrorCode = "not_handled"

	// AuthErrorCodeInternal 表示鉴权内部错误。
	AuthErrorCodeInternal AuthErrorCode = "internal_error"
)

// AuthError 是鉴权层专用错误。
// 它与 service.GatewayError 分层存在，由 HTTP / Service 层统一映射。
type AuthError struct {
	Code       AuthErrorCode
	Message    string
	StatusCode int
	Cause      error
}

// Error 实现 error 接口。
func (e *AuthError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "authentication error"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", msg, e.Cause)
	}
	return msg
}

// Unwrap 返回底层错误。
func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HTTPStatusCode 返回该错误对应的 HTTP 状态码。
func (e *AuthError) HTTPStatusCode() int {
	if e == nil || e.StatusCode <= 0 {
		return http.StatusInternalServerError
	}
	return e.StatusCode
}

func newAuthError(code AuthErrorCode, message string, statusCode int, cause error) *AuthError {
	return &AuthError{
		Code:       code,
		Message:    strings.TrimSpace(message),
		StatusCode: statusCode,
		Cause:      cause,
	}
}

// NewNoCredentialsError 创建"请求未携带凭据"错误。
func NewNoCredentialsError() *AuthError {
	return newAuthError(AuthErrorCodeNoCredentials, "missing API key", http.StatusUnauthorized, nil)
}

// NewInvalidCredentialError 创建"凭据无效"错误。
func NewInvalidCredentialError() *AuthError {
	return newAuthError(AuthErrorCodeInvalidCredential, "invalid API key", http.StatusUnauthorized, nil)
}

// NewNotHandledError 创建"提供者不处理该请求"错误。
func NewNotHandledError() *AuthError {
	return newAuthError(AuthErrorCodeNotHandled, "authentication provider did not handle request", 0, nil)
}

// NewInternalAuthError 创建"鉴权内部错误"。
func NewInternalAuthError(message string, cause error) *AuthError {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "authentication service error"
	}
	return newAuthError(AuthErrorCodeInternal, msg, http.StatusInternalServerError, cause)
}

// IsAuthErrorCode 判断错误码是否匹配。
func IsAuthErrorCode(err *AuthError, code AuthErrorCode) bool {
	if err == nil {
		return false
	}
	return err.Code == code
}
