package common

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// requestIDKey 是 request id 的上下文 key 类型。
type requestIDKey struct{}

// GenerateRequestID 生成一个 8 位十六进制短请求 ID。
func GenerateRequestID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf)
}

// WithRequestID 将 request id 放入 context。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFromContext 从 context 中取回 request id。
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// EnsureRequestID 确保 context 中一定存在 request id。
// 若当前 context 已包含 request id，则直接返回；否则自动生成。
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if id := RequestIDFromContext(ctx); id != "" {
		return ctx, id
	}
	id := GenerateRequestID()
	return WithRequestID(ctx, id), id
}
