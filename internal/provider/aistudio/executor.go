package aistudio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aistudio-gemini-gateway/internal/execution"
	"aistudio-gemini-gateway/internal/wsrelay"
	"aistudio-gemini-gateway/service"
)

// Logger 是执行器内部使用的最小日志接口。
type Logger interface {
	DebugContext(ctx context.Context, msg string, attrs ...any)
	InfoContext(ctx context.Context, msg string, attrs ...any)
	WarnContext(ctx context.Context, msg string, attrs ...any)
	ErrorContext(ctx context.Context, msg string, attrs ...any)
}

type noopLogger struct{}

func (noopLogger) DebugContext(ctx context.Context, msg string, attrs ...any) {}
func (noopLogger) InfoContext(ctx context.Context, msg string, attrs ...any)  {}
func (noopLogger) WarnContext(ctx context.Context, msg string, attrs ...any)  {}
func (noopLogger) ErrorContext(ctx context.Context, msg string, attrs ...any) {}

// Gemini API 默认基础地址。
const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

// Options 是执行器构造参数。
type Options struct {
	Relay      *wsrelay.Manager
	APIVersion string
	BaseURL    string
	Logger     Logger
	Now        func() time.Time

	// BootstrapTimeout 用于非流式请求的“启动首包超时”。
	// 若在该时限内未收到第一个有效上游响应包，则判定 Provider 启动失败。
	BootstrapTimeout time.Duration
}

// Executor 是 AI Studio Provider 的实际执行器。
// 职责：
// 1. 组装 WS Relay 请求
// 2. 发给指定 Provider
// 3. 返回上游原始响应
type Executor struct {
	relay            *wsrelay.Manager
	apiVersion       string
	baseURL          string
	logger           Logger
	now              func() time.Time
	bootstrapTimeout time.Duration
}

// New 创建执行器。
func New(opts Options) (*Executor, error) {
	if opts.Relay == nil {
		return nil, fmt.Errorf("aistudio executor requires non-nil wsrelay manager")
	}

	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}

	apiVersion := strings.TrimSpace(opts.APIVersion)
	if apiVersion == "" {
		apiVersion = "v1beta"
	}

	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Executor{
		relay:            opts.Relay,
		apiVersion:       apiVersion,
		baseURL:          strings.TrimRight(baseURL, "/"),
		logger:           logger,
		now:              nowFn,
		bootstrapTimeout: opts.BootstrapTimeout,
	}, nil
}

// Execute 执行非流式 generateContent。
func (e *Executor) Execute(ctx context.Context, provider *service.RuntimeProvider, req execution.PreparedRequest) (*execution.UpstreamResponse, error) {
	return e.doNonStream(ctx, provider, req)
}

// CountTokens 执行 countTokens。
func (e *Executor) CountTokens(ctx context.Context, provider *service.RuntimeProvider, req execution.PreparedRequest) (*execution.UpstreamResponse, error) {
	return e.doNonStream(ctx, provider, req)
}

// Stream 执行流式请求。
// 注意：不消费第一条事件，第一条事件由上层 pipeline 决定是否用于 bootstrap retry。
// 直接返回底层 wsrelay channel，不再启动纯转发 goroutine，
// 上层 pipeline 已通过 attemptCtx 控制取消语义。
func (e *Executor) Stream(ctx context.Context, provider *service.RuntimeProvider, req execution.PreparedRequest) (*execution.StreamResponse, error) {
	if e == nil {
		return nil, fmt.Errorf("aistudio executor is nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	wsReq, requestURL, err := e.buildWSRequest(provider, req)
	if err != nil {
		return nil, err
	}

	rawEvents, err := e.relay.Stream(ctx, provider.ID, wsReq)
	if err != nil {
		return nil, err
	}

	return &execution.StreamResponse{
		ProviderID: provider.ID,
		Action:     req.Action,
		URL:        requestURL,
		StartedAt:  e.now(),
		Events:     rawEvents,
	}, nil
}

func (e *Executor) doNonStream(ctx context.Context, provider *service.RuntimeProvider, req execution.PreparedRequest) (*execution.UpstreamResponse, error) {
	if e == nil {
		return nil, fmt.Errorf("aistudio executor is nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	wsReq, requestURL, err := e.buildWSRequest(provider, req)
	if err != nil {
		return nil, err
	}

	resp, err := e.relay.NonStreamWithBootstrap(ctx, provider.ID, wsReq, e.bootstrapTimeout)
	if err != nil {
		return nil, err
	}

	return &execution.UpstreamResponse{
		ProviderID: provider.ID,
		Action:     req.Action,
		URL:        requestURL,
		StatusCode: resp.Status,
		Headers:    resp.Headers,
		Payload:    resp.Body,
		ReceivedAt: e.now(),
	}, nil
}

// buildWSRequest 组装 WS Relay 请求。
// 注意：不再复制 req.Payload，而是直接交给 wsrelay，
// WS JSON 编码时会做最终序列化，这是唯一的不可避免复制点。
func (e *Executor) buildWSRequest(provider *service.RuntimeProvider, req execution.PreparedRequest) (*wsrelay.HTTPRequest, string, error) {
	model := normalizeModelName(firstNonEmpty(req.ResolvedModel, req.RequestedModel))
	if model == "" {
		return nil, "", fmt.Errorf("resolved model is empty")
	}
	if !req.Action.Valid() {
		return nil, "", fmt.Errorf("invalid action: %s", req.Action)
	}

	requestURL := e.buildEndpoint(model, req.Action, req.Alt)

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")

	// Provider 可在连接时声明额外的上游请求头。
	if provider != nil {
		for key, value := range provider.Metadata.RequestHeaders {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			headers.Set(key, value)
		}
	}

	return &wsrelay.HTTPRequest{
		Method:  http.MethodPost,
		URL:     requestURL,
		Headers: headers,
		Body:    req.Payload,
	}, requestURL, nil
}

func (e *Executor) buildEndpoint(model string, action service.Action, alt string) string {
	actionName := action.String()
	base := fmt.Sprintf("%s/%s/models/%s:%s", e.baseURL, e.apiVersion, model, actionName)

	switch action {
	case service.ActionStreamGenerateContent:
		trimmedAlt := strings.TrimSpace(alt)
		if trimmedAlt == "" || strings.EqualFold(trimmedAlt, "sse") {
			return base + "?alt=sse"
		}
		return base + "?$alt=" + url.QueryEscape(trimmedAlt)

	case service.ActionCountTokens:
		return base

	default:
		if strings.TrimSpace(alt) != "" {
			return base + "?$alt=" + url.QueryEscape(strings.TrimSpace(alt))
		}
		return base
	}
}

func normalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "models/") {
		model = model[len("models/"):]
	}
	if strings.HasSuffix(model, ")") {
		if idx := strings.LastIndex(model, "("); idx > 0 {
			model = model[:idx]
		}
	}
	return strings.TrimSpace(model)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}