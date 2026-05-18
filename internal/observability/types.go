// Package observability 提供系统级可观测性抽象。
// 当前版本仅保留日志能力接口，作为全局唯一的日志契约。
// 各内部模块可按需定义兼容的最小日志接口以降低包间耦合。
package observability

import "context"

// Logger 是系统统一日志接口。
// 采用与标准库 slog 接近的风格，便于对接标准库与第三方日志框架。
//
// 使用约定：
//   - attrs 参数采用 key-value 交替形式，如 "user_id", 123, "action", "login"
//   - 敏感字段由实现层自行脱敏（参见 SlogLogger）
//   - 上下文中的 request_id 由实现层自动提取并附加
type Logger interface {
	// DebugContext 记录 DEBUG 级别日志。
	DebugContext(ctx context.Context, msg string, attrs ...any)

	// InfoContext 记录 INFO 级别日志。
	InfoContext(ctx context.Context, msg string, attrs ...any)

	// WarnContext 记录 WARN 级别日志。
	WarnContext(ctx context.Context, msg string, attrs ...any)

	// ErrorContext 记录 ERROR 级别日志。
	ErrorContext(ctx context.Context, msg string, attrs ...any)

	// With 返回一个附带额外结构化字段的派生 Logger。
	With(attrs ...any) Logger
}
