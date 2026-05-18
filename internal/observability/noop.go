package observability

import "context"

// NoopLogger 是空日志器实现。
// 所有方法均为空操作，适用于不需要日志输出的场景（如单元测试）。
type NoopLogger struct{}

func (NoopLogger) DebugContext(ctx context.Context, msg string, attrs ...any) {}
func (NoopLogger) InfoContext(ctx context.Context, msg string, attrs ...any)  {}
func (NoopLogger) WarnContext(ctx context.Context, msg string, attrs ...any)  {}
func (NoopLogger) ErrorContext(ctx context.Context, msg string, attrs ...any) {}
func (NoopLogger) With(attrs ...any) Logger                                   { return NoopLogger{} }
