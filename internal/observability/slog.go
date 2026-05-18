package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"aistudio-gemini-gateway/internal/common"
)

// SlogOptions 是 SlogLogger 的构造参数。
type SlogOptions struct {
	// Writer 是日志输出目标。为 nil 时默认使用 os.Stdout。
	Writer io.Writer

	// Level 是日志级别，支持：debug / info / warn / error。
	// 为空时默认为 info。
	Level string

	// Format 是日志输出格式，支持：text / json。
	// 为空时默认为 text。
	Format string

	// AddSource 是否在日志中附加源码调用位置。
	AddSource bool
}

// SlogLogger 是基于标准库 log/slog 的 Logger 实现。
// 它会自动完成以下处理：
//   - 从 context 中提取 request_id 并附加到日志
//   - 对敏感字段名（如 authorization / token / secret）自动脱敏
//   - 对包含 url 的字段名自动脱敏 userinfo 与敏感 query 参数
type SlogLogger struct {
	base *slog.Logger
}

// NewSlogLogger 创建 SlogLogger。
func NewSlogLogger(opts SlogOptions) (Logger, error) {
	level, err := parseSlogLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "text"
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     level,
		AddSource: opts.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 敏感字段自动脱敏。
			if common.IsSensitiveName(a.Key) {
				return slog.Any(a.Key, common.RedactValue(a.Key, a.Value.Any()))
			}
			// URL 类字段自动脱敏 userinfo 与敏感 query。
			if strings.Contains(strings.ToLower(a.Key), "url") {
				return slog.Any(a.Key, common.RedactValue(a.Key, a.Value.Any()))
			}
			// 复杂值类型统一走脱敏路径。
			switch value := a.Value.Any().(type) {
			case map[string]string, map[string]any, []string, []byte:
				return slog.Any(a.Key, common.RedactValue(a.Key, value))
			case io.Reader:
				// 避免把 reader 内容打进日志。
				return slog.String(a.Key, "<reader>")
			default:
				return slog.Any(a.Key, common.RedactValue(a.Key, value))
			}
		},
	}

	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(opts.Writer, handlerOpts)
	case "json":
		handler = slog.NewJSONHandler(opts.Writer, handlerOpts)
	default:
		return nil, fmt.Errorf("unsupported slog format: %s", format)
	}

	return &SlogLogger{base: slog.New(handler)}, nil
}

// With 返回一个附带额外结构化字段的派生 Logger。
func (l *SlogLogger) With(attrs ...any) Logger {
	if l == nil || l.base == nil {
		return NoopLogger{}
	}
	return &SlogLogger{base: l.base.With(attrs...)}
}

func (l *SlogLogger) DebugContext(ctx context.Context, msg string, attrs ...any) {
	l.log(ctx, slog.LevelDebug, msg, attrs...)
}

func (l *SlogLogger) InfoContext(ctx context.Context, msg string, attrs ...any) {
	l.log(ctx, slog.LevelInfo, msg, attrs...)
}

func (l *SlogLogger) WarnContext(ctx context.Context, msg string, attrs ...any) {
	l.log(ctx, slog.LevelWarn, msg, attrs...)
}

func (l *SlogLogger) ErrorContext(ctx context.Context, msg string, attrs ...any) {
	l.log(ctx, slog.LevelError, msg, attrs...)
}

// log 是内部统一写日志入口。
// 它会自动从 context 中提取 request_id 并附加，避免调用方重复传入。
func (l *SlogLogger) log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	if l == nil || l.base == nil {
		return
	}
	if ctx != nil {
		if requestID := common.RequestIDFromContext(ctx); requestID != "" && !attrsContainKey(attrs, "request_id") {
			attrs = append(attrs, "request_id", requestID)
		}
	}
	l.base.Log(ctx, level, msg, attrs...)
}

// attrsContainKey 判断 attrs 中是否已包含指定 key。
// 支持两种风格：
//   - slog.Attr{Key: "xxx", ...}
//   - "key", value 交替
func attrsContainKey(attrs []any, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for i := 0; i < len(attrs); i++ {
		switch typed := attrs[i].(type) {
		case slog.Attr:
			if strings.EqualFold(typed.Key, key) {
				return true
			}
		case string:
			if i+1 < len(attrs) && strings.EqualFold(strings.TrimSpace(typed), key) {
				return true
			}
		}
	}
	return false
}

// parseSlogLevel 将配置字符串解析为 slog 日志级别。
func parseSlogLevel(level string) (slog.Leveler, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return nil, fmt.Errorf("unsupported slog level: %s", level)
	}
}
