package pipeline

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"aistudio-gemini-gateway/internal/common"
	"aistudio-gemini-gateway/internal/compat/imagepreview"
	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/internal/execution"
	gemininorm "aistudio-gemini-gateway/internal/normalize/gemini"
	aistudiopkg "aistudio-gemini-gateway/internal/provider/aistudio"
	registrypkg "aistudio-gemini-gateway/internal/provider/registry"
	selectorpkg "aistudio-gemini-gateway/internal/provider/selector"
	sessionpkg "aistudio-gemini-gateway/internal/provider/session"
	"aistudio-gemini-gateway/internal/thinking"
	"aistudio-gemini-gateway/internal/wsrelay"
	"aistudio-gemini-gateway/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Logger 是 pipeline 内部使用的最小日志接口。
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

// ResolveModelFunc 是 pipeline 依赖的模型解析函数签名。
// 由上层装配时把静态 ModelRegistry 注入。
type ResolveModelFunc func(model string) (resolved string, info *service.ModelInfo, found bool)

// Options 是 Pipeline 的构造参数。
type Options struct {
	Config *config.Config

	// ResolveModel 是静态模型真值源的解析入口。
	ResolveModel ResolveModelFunc

	Registry         *registrypkg.Registry
	Selector         selectorpkg.Selector
	SessionExtractor sessionpkg.Extractor

	Relay    *wsrelay.Manager
	Executor *aistudiopkg.Executor

	Logger Logger
}

// Pipeline 是 Gemini 请求执行管线。
//
// 职责：
//  1. 模型名解析（alias、suffix 剥离）
//  2. 请求标准化（normalize / thinking / countTokens 字段清理 / image preview 兼容）
//  3. Provider 选择与 bootstrap retry
//  4. 执行与错误分类
//  5. 流式协议适配
type Pipeline struct {
	cfg          *config.Config
	resolveModel ResolveModelFunc
	registry     *registrypkg.Registry
	selector     selectorpkg.Selector
	executor     *aistudiopkg.Executor

	logger           Logger
	sessionExtractor sessionpkg.Extractor

	bootstrapRetries int
	providerCooldown time.Duration

	ownedSelector bool

	closeOnce     sync.Once
	subscribeStop func()
}

// New 创建 Pipeline。
func New(opts Options) (*Pipeline, error) {
	cfg, err := config.Prepare(opts.Config)
	if err != nil {
		return nil, service.NewConfigError("pipeline config invalid", err)
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if opts.ResolveModel == nil {
		return nil, service.NewConfigError("pipeline requires non-nil model resolver", nil)
	}
	if opts.Registry == nil {
		return nil, service.NewConfigError("pipeline requires non-nil provider registry", nil)
	}

	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}

	sessionExtractor := opts.SessionExtractor
	if sessionExtractor == nil {
		sessionExtractor = sessionpkg.NewDefaultExtractor()
	}

	sessionTTL, _ := config.ParseDurationOrDefault(cfg.Routing.SessionAffinityTTL, time.Hour)

	selectorRef := opts.Selector
	ownedSelector := false
	if selectorRef == nil {
		selectorRef = selectorpkg.New(cfg.Routing.Strategy, selectorpkg.Options{
			Logger:      logger,
			AffinityTTL: sessionTTL,
		})
		ownedSelector = true
	}

	executorRef := opts.Executor
	if executorRef == nil {
		if opts.Relay == nil {
			return nil, service.NewConfigError("pipeline requires relay or prebuilt executor", nil)
		}
		executorRef, err = aistudiopkg.New(aistudiopkg.Options{
			Relay:      opts.Relay,
			APIVersion: cfg.Gemini.APIVersion,
			Logger:     logger,
		})
		if err != nil {
			return nil, err
		}
	}

	providerCooldown, _ := config.ParseDurationOrDefault(cfg.Routing.ProviderCooldown, 5*time.Minute)

	p := &Pipeline{
		cfg:              cfg,
		resolveModel:     opts.ResolveModel,
		registry:         opts.Registry,
		selector:         selectorRef,
		executor:         executorRef,
		logger:           logger,
		sessionExtractor: sessionExtractor,
		bootstrapRetries: maxInt(0, cfg.Routing.BootstrapRetries),
		providerCooldown: providerCooldown,
		ownedSelector:    ownedSelector,
	}

	p.attachProviderEventSink()
	return p, nil
}

// Close 释放 Pipeline 持有的后台资源。
func (p *Pipeline) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.subscribeStop != nil {
			p.subscribeStop()
		}
		if p.ownedSelector {
			if stopper, ok := p.selector.(interface{ Stop() }); ok {
				stopper.Stop()
			}
		}
	})
	return nil
}

// Execute 处理 Gemini 非流式 generateContent。
func (p *Pipeline) Execute(ctx context.Context, req *service.GatewayRequest) (*service.GatewayResponse, error) {
	ctx, requestCtx, err := p.prepareGenerateRequest(ctx, req, service.ActionGenerateContent)
	if err != nil {
		return nil, err
	}

	resp, _, err := p.executeNonStreamWithRetry(ctx, requestCtx)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Stream 处理 Gemini 流式 streamGenerateContent。
func (p *Pipeline) Stream(ctx context.Context, req *service.GatewayRequest) (*service.StreamResult, error) {
	ctx, requestCtx, err := p.prepareGenerateRequest(ctx, req, service.ActionStreamGenerateContent)
	if err != nil {
		return nil, err
	}

	candidates := p.registry.ListSelectable()
	if len(candidates) == 0 {
		return nil, service.NewProviderUnavailableError(
			"no provider connected",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithPublicMessage("当前没有可用的 Provider"),
		)
	}

	maxAttempts := p.maxAttempts(len(candidates))
	tried := make(map[string]struct{}, len(candidates))

	var (
		lastErr      error
		lastProvider *service.RuntimeProvider
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		requestCtx.Attempt = attempt + 1

		provider, selectErr := p.selectProvider(ctx, requestCtx, tried)
		if selectErr != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, selectErr
		}

		lastProvider = provider.Clone()
		requestCtx.Provider = provider.Clone()
		tried[normalizeID(provider.ID)] = struct{}{}

		// 每次 bootstrap 尝试使用独立的 attempt context。
		attemptCtx, attemptCancel := context.WithCancel(ctx)

		streamResp, execErr := p.executor.Stream(attemptCtx, provider, requestCtx.PreparedRequest())
		if execErr != nil {
			attemptCancel()
			gwErr := p.classifyTransportError(execErr, requestCtx, provider)
			p.applyProviderCooldown(provider, gwErr)
			lastErr = gwErr

			if p.canRetry(ctx, gwErr, attempt, maxAttempts) {
				continue
			}
			return nil, gwErr
		}

		firstEvent, ok := <-streamResp.Events
		if !ok {
			attemptCancel()
			gwErr := service.NewStreamBootstrapError(
				"provider stream closed before first event",
				nil,
				service.WithRetryable(true),
				service.WithCooldown(true),
				service.WithModel(requestCtx.ResolvedModel),
				service.WithAction(requestCtx.Action),
				service.WithProviderID(provider.ID),
				service.WithPublicMessage("上游流式连接初始化失败"),
			)
			p.applyProviderCooldown(provider, gwErr)
			lastErr = gwErr

			if p.canRetry(ctx, gwErr, attempt, maxAttempts) {
				continue
			}
			return nil, gwErr
		}

		if bootstrapErr := p.classifyBootstrapEvent(firstEvent, requestCtx, provider); bootstrapErr != nil {
			attemptCancel()
			p.applyProviderCooldown(provider, bootstrapErr)
			lastErr = bootstrapErr

			if p.canRetry(ctx, bootstrapErr, attempt, maxAttempts) {
				continue
			}
			return nil, bootstrapErr
		}

		// Bootstrap 成功后，不再切换 Provider。
		return p.buildStreamResult(ctx, attemptCtx, attemptCancel, requestCtx, provider, firstEvent, streamResp.Events), nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	_ = lastProvider

	return nil, service.NewProviderUnavailableError(
		"no provider available after retries",
		nil,
		service.WithModel(requestCtx.ResolvedModel),
		service.WithAction(requestCtx.Action),
		service.WithPublicMessage("当前没有可用的 Provider"),
	)
}

// CountTokens 处理 Gemini countTokens。
// 处理规则：
//  1. 执行 NormalizeRequest
//  2. 不做 thinking 处理
//  3. 不做 image preview 兼容注入
//  4. 清理 countTokens 不支持的字段
func (p *Pipeline) CountTokens(ctx context.Context, req *service.GatewayRequest) (*service.GatewayResponse, error) {
	ctx, requestCtx, err := p.prepareCountTokensRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, _, err := p.executeNonStreamWithRetry(ctx, requestCtx)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// =========================
// 请求准备链
// =========================

// prepareGenerateRequest 是 generateContent / streamGenerateContent 的标准化链。
// 处理顺序：
//
//	原始请求体 -> NormalizeRequest -> thinking.Apply -> 剥离网关内部字段 -> imagepreview.Apply
func (p *Pipeline) prepareGenerateRequest(ctx context.Context, req *service.GatewayRequest, action service.Action) (context.Context, *RequestContext, error) {
	ctx, requestCtx, err := p.prepareBaseRequest(ctx, req, action)
	if err != nil {
		return nil, nil, err
	}

	// 单工作缓冲区：work 在每个阶段逐步变换，不保留原始 payload 副本。
	work, err := gemininorm.NormalizeRequest(req.Payload, gemininorm.Options{
		SafetyMode:     p.cfg.Gemini.SafetyDefaultsMode,
		SafetySettings: p.cfg.Gemini.DefaultSafetySettings,
	})
	if err != nil {
		return nil, nil, err
	}

	work, err = thinking.Apply(work, requestCtx.RequestedModel, requestCtx.ModelInfo, p.cfg.Gemini.Thinking)
	if err != nil {
		return nil, nil, err
	}

	work = stripGatewayOnlyJSONFields(work)

	work, err = imagepreview.Apply(requestCtx.ResolvedModel, work, p.cfg.Gemini.ImagePreviewCompatibility)
	if err != nil {
		return nil, nil, err
	}

	requestCtx.PreparedPayload = work
	return ctx, requestCtx, nil
}

// prepareCountTokensRequest 是 countTokens 的标准化链。
// 处理顺序：
//
//	原始请求体 -> NormalizeRequest -> 剥离 countTokens 不支持的字段
func (p *Pipeline) prepareCountTokensRequest(ctx context.Context, req *service.GatewayRequest) (context.Context, *RequestContext, error) {
	ctx, requestCtx, err := p.prepareBaseRequest(ctx, req, service.ActionCountTokens)
	if err != nil {
		return nil, nil, err
	}

	work, err := gemininorm.NormalizeRequest(req.Payload, gemininorm.Options{
		SafetyMode:     p.cfg.Gemini.SafetyDefaultsMode,
		SafetySettings: p.cfg.Gemini.DefaultSafetySettings,
	})
	if err != nil {
		return nil, nil, err
	}

	work = stripCountTokensUnsupportedFields(work)

	requestCtx.PreparedPayload = work
	return ctx, requestCtx, nil
}

// prepareBaseRequest 是所有请求的公共预处理。
// 职责：参数校验、模型解析、session 提取、构造 RequestContext。
func (p *Pipeline) prepareBaseRequest(ctx context.Context, req *service.GatewayRequest, action service.Action) (context.Context, *RequestContext, error) {
	if p == nil {
		return nil, nil, service.NewInternalError("pipeline is nil", nil)
	}
	if req == nil {
		return nil, nil, service.NewRequestError(
			"请求不能为空",
			nil,
			service.WithPublicMessage("请求不能为空"),
			service.WithAction(action),
		)
	}

	if req.Action != "" && req.Action != action {
		return nil, nil, service.NewRequestError(
			"请求 action 与调用方法不匹配",
			nil,
			service.WithPublicMessage("请求 action 与调用方法不匹配"),
			service.WithAction(action),
		)
	}

	ctx, requestID := common.EnsureRequestID(ctx)
	if strings.TrimSpace(req.RequestID) != "" {
		requestID = strings.TrimSpace(req.RequestID)
		ctx = common.WithRequestID(ctx, requestID)
	}

	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}

	requestedModel := strings.TrimSpace(req.Model)
	if requestedModel == "" {
		return nil, nil, service.NewRequestError(
			"模型名称不能为空",
			nil,
			service.WithPublicMessage("模型名称不能为空"),
			service.WithAction(action),
		)
	}

	normalizedRequested := normalizeModelName(requestedModel)
	if normalizedRequested == "" {
		return nil, nil, service.NewRequestError(
			"模型名称非法",
			nil,
			service.WithPublicMessage("模型名称非法"),
			service.WithModel(requestedModel),
			service.WithAction(action),
		)
	}

	if p.resolveModel == nil {
		return nil, nil, service.NewInternalError(
			"model resolver is not configured",
			nil,
			service.WithModel(requestedModel),
			service.WithAction(action),
		)
	}

	resolvedModel, modelInfo, found := p.resolveStaticModel(requestedModel)
	if !found || strings.TrimSpace(resolvedModel) == "" || modelInfo == nil {
		return nil, nil, service.NewRequestError(
			"model not found",
			nil,
			service.WithStatusCode(http.StatusNotFound),
			service.WithPublicMessage("请求的模型不存在"),
			service.WithModel(requestedModel),
			service.WithAction(action),
		)
	}

	if !modelSupportsAction(modelInfo, action) {
		return nil, nil, service.NewRequestError(
			"模型不支持当前操作",
			nil,
			service.WithStatusCode(http.StatusBadRequest),
			service.WithPublicMessage("模型不支持当前操作"),
			service.WithModel(resolvedModel),
			service.WithAction(action),
		)
	}

	// session 提取必须在请求体字段剥离之前完成，
	// 否则 body 中的 session_id / conversation_id 会被删掉导致 session affinity 失效。
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" && p.sessionExtractor != nil {
		sessionID = strings.TrimSpace(p.sessionExtractor.Extract(req))
	}

	requestCtx := &RequestContext{
		RequestID:                requestID,
		RequestedModel:           requestedModel,
		NormalizedRequestedModel: normalizedRequested,
		ResolvedModel:            resolvedModel,
		Action:                   action,
		Alt:                      strings.TrimSpace(req.Alt),
		Streaming:                action.IsStreaming(),
		SessionID:                sessionID,
		RequestedAt:              requestedAt,

		// 性能优化：
		// 模型注册表在启动后是只读的，这里直接持有只读引用，避免每请求 Clone。
		ModelInfo: modelInfo,
	}
	return ctx, requestCtx, nil
}

func (p *Pipeline) resolveStaticModel(model string) (string, *service.ModelInfo, bool) {
	if p == nil || p.resolveModel == nil {
		return "", nil, false
	}

	resolved, info, found := p.resolveModel(model)
	if !found {
		normalized := normalizeModelName(model)
		if normalized != "" && normalized != strings.TrimSpace(model) {
			resolved, info, found = p.resolveModel(normalized)
		}
	}
	if !found || info == nil {
		return "", nil, false
	}

	canonical := normalizeModelName(firstNonEmpty(
		resolved,
		info.BaseName,
		info.Name,
		model,
	))
	if canonical == "" {
		return "", nil, false
	}

	// 性能优化：
	// resolveModel 由静态模型注册表提供只读引用，这里不再 Clone。
	return canonical, info, true
}

// =========================
// 执行逻辑
// =========================

func (p *Pipeline) executeNonStreamWithRetry(ctx context.Context, requestCtx *RequestContext) (*service.GatewayResponse, *service.RuntimeProvider, error) {
	candidates := p.registry.ListSelectable()
	if len(candidates) == 0 {
		return nil, nil, service.NewProviderUnavailableError(
			"no provider connected",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithPublicMessage("当前没有可用的 Provider"),
		)
	}

	maxAttempts := p.maxAttempts(len(candidates))
	tried := make(map[string]struct{}, len(candidates))

	var (
		lastErr      error
		lastProvider *service.RuntimeProvider
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		requestCtx.Attempt = attempt + 1

		provider, selectErr := p.selectProvider(ctx, requestCtx, tried)
		if selectErr != nil {
			if lastErr != nil {
				return nil, lastProvider, lastErr
			}
			return nil, lastProvider, selectErr
		}

		lastProvider = provider.Clone()
		requestCtx.Provider = provider.Clone()
		tried[normalizeID(provider.ID)] = struct{}{}

		resp, execErr := p.executeOnce(ctx, requestCtx, provider)
		if execErr != nil {
			p.applyProviderCooldown(provider, execErr)
			lastErr = execErr

			if p.canRetry(ctx, execErr, attempt, maxAttempts) {
				p.logger.WarnContext(ctx, "pipeline retrying non-stream request",
					"request_id", requestCtx.RequestID,
					"provider_id", provider.ID,
					"attempt", requestCtx.Attempt,
					"max_attempts", maxAttempts,
					"error", execErr.Error(),
				)
				continue
			}
			return nil, provider, execErr
		}

		return resp, provider, nil
	}

	if lastErr != nil {
		return nil, lastProvider, lastErr
	}

	return nil, lastProvider, service.NewProviderUnavailableError(
		"no provider available after retries",
		nil,
		service.WithModel(requestCtx.ResolvedModel),
		service.WithAction(requestCtx.Action),
		service.WithPublicMessage("当前没有可用的 Provider"),
	)
}

func (p *Pipeline) executeOnce(ctx context.Context, requestCtx *RequestContext, provider *service.RuntimeProvider) (*service.GatewayResponse, error) {
	if requestCtx == nil || provider == nil {
		return nil, service.NewInternalError("invalid execute state", nil)
	}

	prepared := requestCtx.PreparedRequest()

	var (
		upstream *execution.UpstreamResponse
		err      error
	)

	switch requestCtx.Action {
	case service.ActionGenerateContent:
		upstream, err = p.executor.Execute(ctx, provider, prepared)
	case service.ActionCountTokens:
		upstream, err = p.executor.CountTokens(ctx, provider, prepared)
	default:
		return nil, service.NewRequestError(
			"unsupported non-stream action",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
		)
	}

	if err != nil {
		return nil, p.classifyTransportError(err, requestCtx, provider)
	}
	if upstream == nil {
		return nil, service.NewUpstreamProtocolError(
			"executor returned nil upstream response",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithProviderID(provider.ID),
			service.WithRetryable(true),
			service.WithCooldown(true),
		)
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return nil, p.classifyHTTPStatus(upstream.StatusCode, upstream.Headers, upstream.Payload, requestCtx, provider)
	}

	response := &service.GatewayResponse{
		RequestID:  requestCtx.RequestID,
		StatusCode: upstream.StatusCode,

		// 性能优化：
		// upstream 在本次执行结束后不会再被写入，直接把 Header/Payload 所有权移交给响应对象，
		// 避免非流式响应重复复制。
		Headers:    upstream.Headers,
		Payload:    upstream.Payload,
		Metadata:   requestCtx.ResponseMetadata(),
		ReceivedAt: upstream.ReceivedAt,
	}
	if response.Metadata == nil {
		response.Metadata = make(map[string]any)
	}
	response.Metadata["upstream_url"] = upstream.URL
	return response, nil
}

// =========================
// 流式结果构建
// =========================

func (p *Pipeline) buildStreamResult(
	ctx context.Context,
	attemptCtx context.Context,
	attemptCancel context.CancelFunc,
	requestCtx *RequestContext,
	provider *service.RuntimeProvider,
	firstEvent wsrelay.StreamEvent,
	events <-chan wsrelay.StreamEvent,
) *service.StreamResult {
	// 边界保护：
	// 正常路径下 requestCtx / provider 都不应为 nil。
	// 这里做兜底，避免极端情况下后续错误处理或日志路径发生空指针。
	if requestCtx == nil {
		requestCtx = &RequestContext{}
	}
	if provider == nil {
		provider = &service.RuntimeProvider{}
	}

	statusCode := firstEvent.Status
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}

	// 性能优化：
	// 首包 Header 在当前请求生命周期内只读，直接引用，避免复制。
	headers := firstEvent.Headers

	useSSE := strings.TrimSpace(requestCtx.Alt) == "" || strings.EqualFold(strings.TrimSpace(requestCtx.Alt), "sse")
	adapter := newGeminiStreamAdapter(useSSE)

	out := make(chan service.StreamChunk, 16)

	go func() {
		defer close(out)
		defer attemptCancel()

		sendChunk := func(chunk service.StreamChunk) bool {
			select {
			case <-attemptCtx.Done():
				return false
			case out <- chunk:
				return true
			}
		}

		logLogicalTerminal := func(reason string, source string) {
			if strings.TrimSpace(reason) == "" {
				reason = "UNKNOWN"
			}
			p.logger.InfoContext(ctx, "pipeline stream reached logical terminal payload",
				"request_id", requestCtx.RequestID,
				"provider_id", provider.ID,
				"model", requestCtx.ResolvedModel,
				"action", requestCtx.Action.String(),
				"attempt", requestCtx.Attempt,
				"source", source,
				"finish_reason", reason,
			)
		}

		// forwardAdapted 负责把 adapter 已完成协议适配的 payload 下发给客户端。
		//
		// 关键增强：
		// 每发送一个 payload 后，立即检查该 payload 是否已经包含 Gemini 协议层的结束信号：
		// 1. candidates[0].finishReason
		// 2. response.candidates[0].finishReason
		// 3. promptFeedback.blockReason
		// 4. response.promptFeedback.blockReason
		//
		// 一旦命中，说明这轮请求在 Gemini 语义上已经完成，
		// 即便 Provider 没有显式发送 stream_end，也应该主动结束流，
		// 避免多轮工具调用中的前一轮连接一直占着不释放。
		forwardAdapted := func(items []adaptedStreamChunk, source string) (ok bool, terminal bool, terminalReason string) {
			for _, item := range items {
				if len(item.DownstreamPayload) == 0 {
					continue
				}

				if !sendChunk(service.StreamChunk{
					// 性能优化：
					// stream adapter 已为当前 chunk 产出独立 payload，这里不再二次 clone。
					Payload: item.DownstreamPayload,
					Metadata: map[string]any{
						"provider_id": provider.ID,
					},
				}) {
					return false, false, ""
				}

				// 逻辑终止检测：
				// 只要当前已发送给客户端的 payload 自身已是终态，就直接收尾。
				if done, reason := detectGeminiTerminalPayload(item.DownstreamPayload); done {
					logLogicalTerminal(reason, source)
					return true, true, reason
				}
			}
			return true, false, ""
		}

		forwardPayload := func(raw []byte, source string) (ok bool, terminal bool, terminalReason string) {
			return forwardAdapted(adapter.Adapt(raw), source)
		}

		flushPayload := func(source string) (ok bool, terminal bool, terminalReason string) {
			return forwardAdapted(adapter.Flush(), source)
		}

		process := func(event wsrelay.StreamEvent) bool {
			if event.Err != nil {
				// 发生 transport 错误时，先尽量冲刷 adapter 缓冲中的尾部数据，
				// 然后将错误下发给客户端。
				_, _, _ = flushPayload("event_err_flush")

				_ = sendChunk(service.StreamChunk{
					Err: p.classifyTransportError(event.Err, requestCtx, provider),
					Metadata: map[string]any{
						"provider_id": provider.ID,
					},
				})
				return false
			}

			switch event.Type {
			case wsrelay.MessageTypeStreamStart:
				// bootstrap 阶段已消费过 status/header，这里不再额外下发。
				return true

			case wsrelay.MessageTypeStreamChunk:
				if len(event.Payload) == 0 {
					return true
				}

				ok, terminal, _ := forwardPayload(event.Payload, "stream_chunk")
				if !ok {
					return false
				}
				if terminal {
					// 当前 chunk 已经是 Gemini 逻辑终态，不再继续等待 stream_end。
					return false
				}
				return true

			case wsrelay.MessageTypeHTTPResp:
				// 某些 Provider 可能不会走 stream_end，而是直接补一个整体 http_response。
				if len(event.Payload) > 0 {
					ok, terminal, _ := forwardPayload(event.Payload, "http_response")
					if !ok {
						return false
					}
					if terminal {
						return false
					}
				}

				// 即使没有显式逻辑终止，也将 adapter 剩余缓冲冲刷后结束。
				_, _, _ = flushPayload("http_response_flush")
				return false

			case wsrelay.MessageTypeStreamEnd:
				// 正常 transport 结束：冲刷缓存后退出。
				_, _, _ = flushPayload("stream_end_flush")
				return false

			case wsrelay.MessageTypeError:
				// 显式 error 包：冲刷缓存后下发上游协议错误。
				_, _, _ = flushPayload("message_error_flush")

				_ = sendChunk(service.StreamChunk{
					Err: service.NewUpstreamProtocolError(
						"upstream stream error",
						event.Err,
						service.WithModel(requestCtx.ResolvedModel),
						service.WithAction(requestCtx.Action),
						service.WithProviderID(provider.ID),
					),
					Metadata: map[string]any{
						"provider_id": provider.ID,
					},
				})
				return false
			}

			return true
		}

		// 先处理 bootstrap 后的首个事件。
		if !process(firstEvent) {
			return
		}

		// 再持续消费后续事件。
		for {
			select {
			case <-attemptCtx.Done():
				// 调用方取消或上层主动结束时，尽量冲刷残留缓冲。
				_, _, _ = flushPayload("attempt_ctx_done_flush")
				return

			case event, ok := <-events:
				if !ok {
					// Provider 事件流关闭：冲刷残留缓冲后结束。
					_, _, _ = flushPayload("events_closed_flush")
					return
				}
				if !process(event) {
					return
				}
			}
		}
	}()

	return &service.StreamResult{
		RequestID:  requestCtx.RequestID,
		StatusCode: statusCode,
		Headers:    headers,
		Chunks:     out,
	}
}

// =========================
// Provider 选择
// =========================

func (p *Pipeline) selectProvider(ctx context.Context, requestCtx *RequestContext, tried map[string]struct{}) (*service.RuntimeProvider, error) {
	candidates := p.registry.ListSelectable()
	if len(candidates) == 0 {
		return nil, service.NewProviderUnavailableError(
			"no provider connected",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithPublicMessage("当前没有可用的 Provider"),
		)
	}

	selectionReq := requestCtx.SelectorRequest()
	return p.selector.Select(ctx, selectionReq, candidates, tried)
}

// =========================
// 错误分类
// =========================

func (p *Pipeline) classifyBootstrapEvent(event wsrelay.StreamEvent, requestCtx *RequestContext, provider *service.RuntimeProvider) *service.GatewayError {
	switch event.Type {
	case wsrelay.MessageTypeError:
		return p.classifyTransportError(event.Err, requestCtx, provider)

	case wsrelay.MessageTypeHTTPResp:
		if event.Status < 200 || event.Status >= 300 {
			return p.classifyHTTPStatus(event.Status, event.Headers, event.Payload, requestCtx, provider)
		}
		return nil

	case wsrelay.MessageTypeStreamStart:
		if event.Status > 0 && (event.Status < 200 || event.Status >= 300) {
			return p.classifyHTTPStatus(event.Status, event.Headers, event.Payload, requestCtx, provider)
		}
		return nil

	case wsrelay.MessageTypeStreamEnd:
		// 启动阶段直接结束，视为 bootstrap 失败。
		return service.NewStreamBootstrapError(
			"upstream stream ended before data",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithProviderID(providerID(provider)),
			service.WithRetryable(true),
			service.WithCooldown(true),
			service.WithPublicMessage("上游流式连接初始化失败"),
		)

	default:
		// 只要首个事件是数据 chunk，即视为 bootstrap 成功。
		return nil
	}
}

func (p *Pipeline) classifyTransportError(err error, requestCtx *RequestContext, provider *service.RuntimeProvider) *service.GatewayError {
	if err == nil {
		return service.NewInternalError(
			"unknown transport error",
			nil,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithProviderID(providerID(provider)),
		)
	}

	// 已经是 GatewayError 时直接使用。
	var gatewayErr *service.GatewayError
	if errors.As(err, &gatewayErr) && gatewayErr != nil {
		return gatewayErr
	}

	// 调用方主动取消。
	if errors.Is(err, context.Canceled) {
		return service.NewInternalError(
			"request canceled",
			err,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithProviderID(providerID(provider)),
			service.WithPublicMessage("请求已取消"),
		)
	}

	// 超时。
	if errors.Is(err, context.DeadlineExceeded) {
		return service.NewUpstreamProtocolError(
			"request deadline exceeded",
			err,
			service.WithModel(requestCtx.ResolvedModel),
			service.WithAction(requestCtx.Action),
			service.WithProviderID(providerID(provider)),
			service.WithRetryable(true),
			service.WithCooldown(true),
			service.WithPublicMessage("上游请求超时"),
		)
	}

	// wsrelay 层错误。
	var relayErr *wsrelay.RelayError
	if errors.As(err, &relayErr) && relayErr != nil && relayErr.Status > 0 {
		return p.classifyHTTPStatus(relayErr.Status, nil, []byte(relayErr.Message), requestCtx, provider)
	}

	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "upstream transport error"
	}

	return service.NewUpstreamProtocolError(
		message,
		err,
		service.WithModel(requestCtx.ResolvedModel),
		service.WithAction(requestCtx.Action),
		service.WithProviderID(providerID(provider)),
		service.WithRetryable(true),
		service.WithCooldown(true),
		service.WithPublicMessage("上游连接失败"),
	)
}

// classifyHTTPStatus 将上游 HTTP 状态码分类为统一的 GatewayError。
//
// 核心策略：
// 1. 所有上游 HTTP 错误均标记为 retryable，允许切换 Provider 重试
// 2. 冷却策略按状态码区分：
//    - 401/403：Provider auth 失效 → 冷却
//    - 429：Provider 限流 → 冷却（带 Retry-After）
//    - 5xx：Provider 上游故障 → 冷却
//    - 400/404/其他 4xx：可能是请求本身问题 → 不冷却 Provider
// 3. publicMessage 统一透传上游原始错误信息
//
// 设计原则：
// 上游错误是 Provider 级别的，不同 Provider 可能有不同的 auth 状态、配额和可用性。
// 只要还有未尝试的 Provider，就应该切换重试，避免中断客户端的连续任务。
func (p *Pipeline) classifyHTTPStatus(status int, headers http.Header, body []byte, requestCtx *RequestContext, provider *service.RuntimeProvider) *service.GatewayError {
	// 从上游响应体中提取错误信息。
	message := extractErrorMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "upstream http error"
	}

	// 解析 Retry-After 响应头。
	retryAfter := parseRetryAfterHeader(headers)

	// -------------------------------------------------------------------------
	// 所有上游 HTTP 错误均标记为 retryable。
	// 上游错误是 Provider 级别的——不同 Provider 的 session、quota、auth 状态各不相同，
	// 在一个 Provider 上失败的请求，在另一个 Provider 上可能完全正常。
	// 只要还有未尝试的 Provider，Pipeline 就会自动切换重试。
	// -------------------------------------------------------------------------
	retryable := true

	// -------------------------------------------------------------------------
	// 冷却策略：
	// - 只对明确属于"Provider 自身问题"的状态码触发冷却
	// - 400/404 等可能是请求本身的问题，不应惩罚 Provider
	// -------------------------------------------------------------------------
	cooldown := false

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden: // 401, 403
		// Provider session/auth 失效，短期内不会自愈 → 冷却
		cooldown = true
	case http.StatusTooManyRequests: // 429
		// Provider 配额耗尽 → 冷却
		cooldown = true
	case http.StatusInternalServerError: // 500
		cooldown = true
	case http.StatusBadGateway: // 502
		cooldown = true
	case http.StatusServiceUnavailable: // 503
		cooldown = true
	case http.StatusGatewayTimeout: // 504
		cooldown = true
	}

	// 响应体中包含限流/配额关键词时，也触发冷却。
	if containsTemporaryHint(strings.ToLower(message)) {
		cooldown = true
	}

	// -------------------------------------------------------------------------
	// publicMessage 统一透传上游原始错误信息，对特定状态码补充中文前缀。
	// -------------------------------------------------------------------------
	publicMessage := message

	switch status {
	case http.StatusBadRequest: // 400
		publicMessage = "上游请求参数错误: " + message
	case http.StatusUnauthorized: // 401
		publicMessage = "上游认证失败: " + message
	case http.StatusForbidden: // 403
		publicMessage = "上游权限不足: " + message
	case http.StatusNotFound: // 404
		publicMessage = "上游模型不存在: " + message
	case http.StatusTooManyRequests: // 429
		publicMessage = "上游限流或配额不足: " + message
	case http.StatusInternalServerError: // 500
		publicMessage = "上游内部错误: " + message
	case http.StatusBadGateway: // 502
		publicMessage = "上游网关错误: " + message
	case http.StatusServiceUnavailable: // 503
		publicMessage = "上游服务暂时不可用: " + message
	case http.StatusGatewayTimeout: // 504
		publicMessage = "上游网关超时: " + message
	}

	return service.NewUpstreamHTTPError(
		message,
		nil,
		service.WithStatusCode(status),
		service.WithRetryable(retryable),
		service.WithCooldown(cooldown),
		service.WithRetryAfter(retryAfter),
		service.WithModel(requestCtx.ResolvedModel),
		service.WithAction(requestCtx.Action),
		service.WithProviderID(providerID(provider)),
		service.WithPublicMessage(publicMessage),
		service.WithRawBody(body),
		service.WithMetadata(map[string]any{
			"status_code": status,
			"retry_after": retryAfter.String(),
		}),
	)
}

// =========================
// 重试与冷却
// =========================

func (p *Pipeline) canRetry(ctx context.Context, err error, attempt, maxAttempts int) bool {
	if attempt >= maxAttempts-1 {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	var gatewayErr *service.GatewayError
	if errors.As(err, &gatewayErr) && gatewayErr != nil {
		return gatewayErr.Retryable
	}
	return false
}

func (p *Pipeline) applyProviderCooldown(provider *service.RuntimeProvider, err error) {
	if p == nil || p.registry == nil || provider == nil || err == nil {
		return
	}

	// 不论是否需要冷却，都记录本次错误到诊断存储。
	// 这样状态总览接口可以展示每个 Provider 最近一次错误的详情。
	errMessage := err.Error()
	var gatewayErr *service.GatewayError
	if errors.As(err, &gatewayErr) && gatewayErr != nil {
		// 优先使用 publicMessage，它通常包含上游原始错误信息。
		if pub := strings.TrimSpace(gatewayErr.Public); pub != "" {
			errMessage = pub
		}
	}
	p.registry.RecordProviderError(provider.ID, errMessage)

	// 仅当错误标记了 Cooldown 时，才触发 Provider 冷却。
	if !errors.As(err, &gatewayErr) || gatewayErr == nil || !gatewayErr.Cooldown {
		return
	}

	cooldown := p.providerCooldown
	if gatewayErr.RetryAfter > cooldown {
		cooldown = gatewayErr.RetryAfter
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	until := time.Now().Add(cooldown)
	_, _ = p.registry.SetCooldown(provider.ID, until, "provider cooled down: "+errMessage)
}

// maxAttempts 返回本次请求最大尝试次数。
//
// 策略：尝试所有可用 Provider。
// 只要还有未尝试的 Provider，就不应该把上游错误直接抛给客户端。
// 这样可以最大限度避免因单个 Provider 的临时问题中断客户端的连续任务。
//
// 注意：
// 1. 每个 Provider 最多尝试一次（通过 tried map 保证）
// 2. 已冷却的 Provider 不会被选中（selector 负责过滤）
// 3. 若所有 Provider 均失败，最终返回最后一个上游错误
func (p *Pipeline) maxAttempts(providerCount int) int {
	if providerCount <= 0 {
		return 1
	}
	return providerCount
}

// =========================
// Provider 事件订阅
// =========================

// attachProviderEventSink 将 selector（若实现了事件处理接口）
// 接入 registry 的 Provider 生命周期事件流。
// 用于 session affinity selector 在 Provider 失效时主动清绑定。
func (p *Pipeline) attachProviderEventSink() {
	if p == nil || p.registry == nil || p.selector == nil {
		return
	}

	type providerEventHandler interface {
		HandleProviderEvent(ctx context.Context, event service.ProviderEvent)
	}

	sink, ok := p.selector.(providerEventHandler)
	if !ok || sink == nil {
		return
	}

	ch, cancel := p.registry.Subscribe(64)
	p.subscribeStop = cancel

	go func() {
		for event := range ch {
			sink.HandleProviderEvent(context.Background(), event)
		}
	}()
}

// =========================
// 辅助函数
// =========================

// modelSupportsAction 判断模型是否支持指定操作。
// 当 SupportedActions 为空时，默认支持所有操作。
func modelSupportsAction(info *service.ModelInfo, action service.Action) bool {
	if info == nil {
		return false
	}
	if len(info.SupportedActions) == 0 {
		return true
	}
	for _, item := range info.SupportedActions {
		if item == action {
			return true
		}
	}
	return false
}

// stripGatewayOnlyJSONFields 从请求体中剥离网关内部使用的字段，
// 这些字段不应被透传到上游 Gemini API。
func stripGatewayOnlyJSONFields(payload []byte) []byte {
	return deleteJSONPaths(payload,
		"session_id",
		"conversation_id",
	)
}

// stripCountTokensUnsupportedFields 从 countTokens 请求体中剥离上游不支持的字段。
// 与 Gemini countTokens 语义对齐，只保留 contents / systemInstruction 等核心字段。
func stripCountTokensUnsupportedFields(payload []byte) []byte {
	return deleteJSONPaths(payload,
		"generationConfig",
		"tools",
		"safetySettings",
		"session_id",
		"conversation_id",
	)
}

func deleteJSONPaths(payload []byte, paths ...string) []byte {
	out := payload
	for _, path := range paths {
		out = deleteJSONPath(out, path)
	}
	return out
}

func deleteJSONPath(payload []byte, path string) []byte {
	if len(payload) == 0 || strings.TrimSpace(path) == "" || !gjson.ValidBytes(payload) {
		return payload
	}
	out, err := sjson.DeleteBytes(payload, path)
	if err != nil {
		return payload
	}
	return out
}

// extractErrorMessage 从上游错误响应体中提取错误消息。
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	if gjson.ValidBytes(body) {
		for _, path := range []string{
			"error.message",
			"message",
			"error",
		} {
			value := strings.TrimSpace(gjson.GetBytes(body, path).String())
			if value != "" {
				return value
			}
		}
	}

	text := strings.TrimSpace(string(body))
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}
// detectGeminiTerminalPayload 检测一个 Gemini JSON payload 是否已经表示“逻辑结束”。
//
// transport 层的 stream_end 并不总是可靠出现。
// 某些 Provider / 浏览器扩展 / relay 只会持续吐出 JSON chunk，
// 其中最后一个 chunk 已经包含 finishReason，
// 但连接本身还保持着，导致网关一直傻等 stream_end，最终出现“卡住不结束”。
//
// 判定规则：
// 1. candidates[0].finishReason 非空
// 2. response.candidates[0].finishReason 非空（兼容某些包裹格式）
// 3. candidates[0].finish_reason 非空（兼容 snake_case）
// 4. response.candidates[0].finish_reason 非空
// 5. promptFeedback.blockReason 非空
// 6. response.promptFeedback.blockReason 非空
// 7. prompt_feedback.block_reason 非空
// 8. response.prompt_feedback.block_reason 非空
//
// 返回值：
// - done：是否已检测到 Gemini 协议层逻辑结束
// - reason：结束原因，便于日志定位，如 STOP / MAX_TOKENS / SAFETY / BLOCKLIST
func detectGeminiTerminalPayload(payload []byte) (done bool, reason string) {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return false, ""
	}

	// 1. 标准 candidate finishReason 路径
	for _, path := range []string{
		"candidates.0.finishReason",
		"response.candidates.0.finishReason",
		"candidates.0.finish_reason",
		"response.candidates.0.finish_reason",
	} {
		value := strings.TrimSpace(gjson.GetBytes(payload, path).String())
		if value != "" {
			return true, value
		}
	}

	// 2. prompt 被阻断时，也视为请求已完整结束
	for _, path := range []string{
		"promptFeedback.blockReason",
		"response.promptFeedback.blockReason",
		"prompt_feedback.block_reason",
		"response.prompt_feedback.block_reason",
	} {
		value := strings.TrimSpace(gjson.GetBytes(payload, path).String())
		if value != "" {
			return true, value
		}
	}

	return false, ""
}

// containsTemporaryHint 判断错误消息中是否包含临时性错误提示关键词。
func containsTemporaryHint(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	hints := []string{
		"rate limit",
		"quota",
		"resource exhausted",
		"temporarily unavailable",
		"temporary unavailable",
		"too many requests",
		"try again later",
	}
	for _, hint := range hints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

// parseRetryAfterHeader 从响应头中解析 Retry-After 时长。
func parseRetryAfterHeader(headers http.Header) time.Duration {
	if len(headers) == 0 {
		return 0
	}

	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	if when, err := http.ParseTime(raw); err == nil {
		if dur := time.Until(when); dur > 0 {
			return dur
		}
	}

	return 0
}

func providerID(provider *service.RuntimeProvider) string {
	if provider == nil {
		return ""
	}
	return strings.TrimSpace(provider.ID)
}

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// normalizeModelName 归一化模型名称：去除 models/ 前缀和 thinking suffix。
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

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
