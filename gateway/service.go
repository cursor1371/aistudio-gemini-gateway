package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/internal/config"
	httpgemini "aistudio-gemini-gateway/internal/httpapi/gemini"
	httpserver "aistudio-gemini-gateway/internal/httpapi/server"
	modelspkg "aistudio-gemini-gateway/internal/models"
	"aistudio-gemini-gateway/internal/observability"
	"aistudio-gemini-gateway/internal/pipeline"
	registrypkg "aistudio-gemini-gateway/internal/provider/registry"
	"aistudio-gemini-gateway/internal/wsrelay"
	core "aistudio-gemini-gateway/service"
)

// RequestOptions 是 SDK 调用时的可选参数。
type RequestOptions struct {
	Headers   http.Header
	Query     url.Values
	Metadata  map[string]any
	SessionID string
	Alt       string
}

// Options 是 Service 的构造参数。
type Options struct {
	// Config 是网关配置。若为 nil，则使用默认配置。
	Config *Config

	// Logger 是日志实现。若为 nil，则基于配置自动创建 slog logger。
	Logger Logger

	// ModelRegistry 是静态模型注册表。若为 nil，则基于配置自动创建。
	ModelRegistry *ModelRegistry

	// ProviderRegistry 是 Provider 运行时注册表。若为 nil，则自动创建。
	// 当提供自定义 Relay 时，必须同时提供自定义 ProviderRegistry，
	// 并保证二者生命周期回调已在外部绑定。
	ProviderRegistry *Registry

	// Selector 是 Provider 选择器。若为 nil，则按 routing.strategy 自动创建。
	Selector Selector

	// Relay 是 WebSocket 中继管理器。若为 nil，则基于配置自动创建。
	Relay *RelayManager

	// HTTPAccessManager 是 HTTP API 鉴权管理器。若为 nil，则自动创建。
	HTTPAccessManager *AccessManager

	// WSAccessManager 是 WS Provider 握手鉴权管理器。若为 nil，则自动创建。
	WSAccessManager *AccessManager
}

// Service 是轻量 Gemini 网关的对外门面。
// 它既可作为独立服务运行（Start），也可被宿主项目嵌入（Handler）。
type Service struct {
	cfg *config.Config

	logger observability.Logger

	modelRegistry    *modelspkg.Registry
	providerRegistry *registrypkg.Registry

	httpAccessManager *access.Manager
	wsAccessManager   *access.Manager

	relay    *wsrelay.Manager
	pipeline *pipeline.Pipeline
	httpAPI  *httpserver.Server

	stateMu  sync.Mutex
	started  bool
	shutdown bool
}

// NewService 创建并初始化 Service。
// 该方法会按照固定契约装配：静态模型注册表、Provider 运行时、Pipeline 与 HTTP Server。
func NewService(opts Options) (*Service, error) {
	// ---- 配置 ----
	cfg, err := config.Prepare(opts.Config)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	// ---- 日志 ----
	logger := opts.Logger
	if logger == nil {
		logger, err = observability.NewSlogLogger(observability.SlogOptions{
			Writer:    os.Stdout,
			Level:     cfg.Logging.Level,
			Format:    cfg.Logging.Format,
			AddSource: cfg.Logging.AddSource,
		})
		if err != nil {
			return nil, err
		}
	}

	// ---- 鉴权 ----
	httpAccessManager := opts.HTTPAccessManager
	if httpAccessManager == nil {
		httpAccessManager = access.NewManager()
	}
	if err := applyAccessPolicy(httpAccessManager, cfg.Access.HTTP, "http-static-key"); err != nil {
		return nil, err
	}

	wsAccessManager := opts.WSAccessManager
	if wsAccessManager == nil {
		wsAccessManager = access.NewManager()
	}
	if err := applyAccessPolicy(wsAccessManager, cfg.WebSocket.Auth, "ws-static-key"); err != nil {
		return nil, err
	}

	// ---- Relay / Registry 联动校验 ----
	// 若注入自定义 Relay，则要求同时注入自定义 ProviderRegistry，
	// 因为 Relay 构造后不可变，Service 无法再补齐回调绑定。
	if opts.Relay != nil && opts.ProviderRegistry == nil {
		return nil, fmt.Errorf("custom relay requires custom provider registry configured with matching callbacks")
	}

	// ---- Provider Registry ----
	providerRegistry := opts.ProviderRegistry
	if providerRegistry == nil {
		providerRegistry = registrypkg.New(registrypkg.Options{
			Logger: logger,
		})
	}

	// ---- 静态模型注册表 ----
	modelRegistry := opts.ModelRegistry
	if modelRegistry == nil {
		modelRegistry, err = modelspkg.New(cfg.Models)
		if err != nil {
			return nil, err
		}
	}

	// ---- WS Relay ----
	relay := opts.Relay
	if relay == nil {
		identityExtractor := registrypkg.NewIdentityExtractor(cfg.WebSocket.ProviderIdentity)

		handshakeTimeout, _ := config.ParseDurationOrDefault(cfg.WebSocket.HandshakeTimeout, 10*time.Second)
		readTimeout, _ := config.ParseDurationOrDefault(cfg.WebSocket.ReadTimeout, 60*time.Second)
		writeTimeout, _ := config.ParseDurationOrDefault(cfg.WebSocket.WriteTimeout, 10*time.Second)
		pingInterval, _ := config.ParseDurationOrDefault(cfg.WebSocket.Heartbeat.PingInterval, 30*time.Second)
		pongTimeout, _ := config.ParseDurationOrDefault(cfg.WebSocket.Heartbeat.PongTimeout, 60*time.Second)

		relay = wsrelay.NewManager(wsrelay.Options{
			Path:             cfg.WebSocket.Path,
			HandshakeTimeout: handshakeTimeout,
			ReadTimeout:      readTimeout,
			WriteTimeout:     writeTimeout,
			MaxMessageSize:   cfg.WebSocket.MaxMessageSize,
			PingInterval:     pingInterval,
			PongTimeout:      pongTimeout,
			AccessManager:    wsAccessManager,
			OriginCheck:      wsrelay.NewOriginValidator(cfg.WebSocket.Origin.Mode, cfg.WebSocket.Origin.AllowedOrigins),
			ProviderFactory: func(ctx context.Context, r *http.Request, authResult *access.Result) (*core.RuntimeProvider, error) {
				return identityExtractor.Extract(r, authResult)
			},
			OnConnected:    providerRegistry.HandleProviderConnected,
			OnDisconnected: providerRegistry.HandleProviderDisconnected,
			OnTouched:      providerRegistry.HandleProviderTouched,
			Logger:         logger,
		})
	} else {
		// 自定义 Relay 已在外部完成回调绑定。
		// 这里把已在线 Provider 同步到 registry，保证初始化快照一致。
		for _, provider := range relay.Providers() {
			if provider != nil {
				providerRegistry.HandleProviderConnected(context.Background(), provider)
			}
		}
	}

	// ---- Pipeline ----
	// 这里使用执行热路径专用的只读模型解析接口，避免每请求 clone ModelInfo。
	resolveModel := func(model string) (string, *core.ModelInfo, bool) {
		if modelRegistry == nil {
			return "", nil, false
		}
		return modelspkg.ResolveForExecution(modelRegistry, model)
	}

	pipelineRef, err := pipeline.New(pipeline.Options{
		Config:       cfg,
		ResolveModel: resolveModel,
		Registry:     providerRegistry,
		Selector:     opts.Selector,
		Relay:        relay,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}

	// ---- 组装 Service ----
	s := &Service{
		cfg:               cfg,
		logger:            logger,
		modelRegistry:     modelRegistry,
		providerRegistry:  providerRegistry,
		httpAccessManager: httpAccessManager,
		wsAccessManager:   wsAccessManager,
		relay:             relay,
		pipeline:          pipelineRef,
	}

	// ---- HTTP Server ----
	httpAPI, err := httpserver.New(httpserver.Options{
		Config:            cfg,
		Backend:           &httpBackendAdapter{svc: s},
		HTTPAccessManager: httpAccessManager,
		WSHandler:         relay.Handler(),
		Logger:            logger,
	})
	if err != nil {
		_ = pipelineRef.Close()
		return nil, err
	}
	s.httpAPI = httpAPI

	return s, nil
}

// Start 启动独立 HTTP 服务。
// 该方法为阻塞调用：先在后台启动 HTTP 服务，然后阻塞直到 ctx 取消或 HTTP 服务退出。
// 对于嵌入式场景，可不调用 Start，而直接使用 Handler()。
func (s *Service) Start(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.httpAPI == nil {
		return fmt.Errorf("http server is not initialized")
	}

	s.stateMu.Lock()
	if s.shutdown {
		s.stateMu.Unlock()
		return fmt.Errorf("service already shutdown")
	}
	if s.started {
		s.stateMu.Unlock()
		return fmt.Errorf("service already started")
	}
	s.started = true
	s.stateMu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpAPI.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			close(errCh)
			return
		}
		close(errCh)
	}()

	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			s.stateMu.Lock()
			if !s.shutdown {
				s.started = false
			}
			s.stateMu.Unlock()
			return err
		}
		return nil

	case <-ctx.Done():
		shutdownTimeout := 15 * time.Second
		if s.cfg != nil {
			if parsed, pErr := config.ParseDurationOrDefault(s.cfg.Server.ShutdownTimeout, 15*time.Second); pErr == nil {
				shutdownTimeout = parsed
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)

		if err, ok := <-errCh; ok {
			return err
		}
		return nil
	}
}

// Shutdown 优雅关闭 Service。该方法幂等。
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.stateMu.Lock()
	if s.shutdown {
		s.stateMu.Unlock()
		return nil
	}
	s.shutdown = true
	httpAPI := s.httpAPI
	relay := s.relay
	pipe := s.pipeline
	s.stateMu.Unlock()

	var firstErr error
	if httpAPI != nil {
		if err := httpAPI.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
			firstErr = err
		}
	}
	if relay != nil {
		if err := relay.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pipe != nil {
		if err := pipe.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Handler 返回可嵌入宿主项目的 HTTP Handler。
func (s *Service) Handler() http.Handler {
	if s == nil || s.httpAPI == nil {
		return nil
	}
	return s.httpAPI.Handler()
}

// =========================
// SDK 请求入口
// =========================

// Generate 使用完整 GatewayRequest 执行非流式生成。
func (s *Service) Generate(ctx context.Context, req *core.GatewayRequest) (*core.GatewayResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("service is nil")
	}
	if s.pipeline == nil {
		return nil, core.NewInternalError("pipeline is not initialized", nil)
	}
	return s.pipeline.Execute(ctx, req)
}

// ExecuteGemini 是便捷入口：直接用模型名和 payload 调用 generateContent。
func (s *Service) ExecuteGemini(ctx context.Context, model string, payload []byte, opts RequestOptions) (*core.GatewayResponse, error) {
	return s.Generate(ctx, buildGatewayRequest(model, core.ActionGenerateContent, payload, opts))
}

// Stream 使用完整 GatewayRequest 执行流式生成。
func (s *Service) Stream(ctx context.Context, req *core.GatewayRequest) (*core.StreamResult, error) {
	if s == nil {
		return nil, fmt.Errorf("service is nil")
	}
	if s.pipeline == nil {
		return nil, core.NewInternalError("pipeline is not initialized", nil)
	}
	return s.pipeline.Stream(ctx, req)
}

// StreamGemini 是便捷入口：直接用模型名和 payload 调用 streamGenerateContent。
func (s *Service) StreamGemini(ctx context.Context, model string, payload []byte, opts RequestOptions) (*core.StreamResult, error) {
	return s.Stream(ctx, buildGatewayRequest(model, core.ActionStreamGenerateContent, payload, opts))
}

// CountTokens 是便捷入口：直接用模型名和 payload 调用 countTokens。
func (s *Service) CountTokens(ctx context.Context, model string, payload []byte, opts RequestOptions) (*core.GatewayResponse, error) {
	return s.countTokensRequest(ctx, buildGatewayRequest(model, core.ActionCountTokens, payload, opts))
}

// countTokensRequest 是 countTokens 的内部统一执行入口。
func (s *Service) countTokensRequest(ctx context.Context, req *core.GatewayRequest) (*core.GatewayResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("service is nil")
	}
	if s.pipeline == nil {
		return nil, core.NewInternalError("pipeline is not initialized", nil)
	}
	return s.pipeline.CountTokens(ctx, req)
}

// =========================
// Provider 管理
// =========================

// ListProviders 返回当前所有在线 Provider 快照。
func (s *Service) ListProviders() []*core.RuntimeProvider {
	if s == nil || s.providerRegistry == nil {
		return nil
	}
	return s.providerRegistry.List()
}

// GetProvider 获取指定 Provider 快照。
func (s *Service) GetProvider(providerID string) (*core.RuntimeProvider, bool) {
	if s == nil || s.providerRegistry == nil {
		return nil, false
	}
	return s.providerRegistry.Get(providerID)
}

// SubscribeProviderEvents 订阅 Provider 生命周期事件。
func (s *Service) SubscribeProviderEvents(buffer int) (<-chan core.ProviderEvent, func()) {
	if s == nil || s.providerRegistry == nil {
		ch := make(chan core.ProviderEvent)
		close(ch)
		return ch, func() {}
	}
	return s.providerRegistry.Subscribe(buffer)
}

// =========================
// 模型查询
// =========================

// ListModels 返回静态模型注册表中的全部可见模型。
func (s *Service) ListModels() []*core.ModelInfo {
	if s == nil || s.modelRegistry == nil {
		return nil
	}
	return s.modelRegistry.List()
}

// GetModel 获取单个静态模型元信息。
func (s *Service) GetModel(model string) (*core.ModelInfo, bool) {
	if s == nil || s.modelRegistry == nil {
		return nil, false
	}
	return s.modelRegistry.Get(model)
}

// =========================
// HTTP Backend Adapter
// =========================

// httpBackendAdapter 将 gemini.Backend 接口适配到 Service 内部方法。
type httpBackendAdapter struct {
	svc *Service
}

func (a *httpBackendAdapter) ListModels(ctx context.Context) ([]map[string]any, error) {
	_ = ctx
	if a == nil || a.svc == nil {
		return nil, core.NewInternalError("service backend not initialized", nil)
	}
	items := a.svc.ListModels()
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, modelInfoToHTTPMap(item))
	}
	return out, nil
}

func (a *httpBackendAdapter) GetModel(ctx context.Context, model string) (map[string]any, error) {
	_ = ctx
	if a == nil || a.svc == nil {
		return nil, core.NewInternalError("service backend not initialized", nil)
	}
	item, ok := a.svc.GetModel(model)
	if !ok || item == nil {
		return nil, core.NewRequestError(
			"model not found",
			nil,
			core.WithStatusCode(http.StatusNotFound),
			core.WithPublicMessage("模型不存在"),
			core.WithModel(model),
		)
	}
	return modelInfoToHTTPMap(item), nil
}

func (a *httpBackendAdapter) Generate(ctx context.Context, req *httpgemini.Request) (*httpgemini.Response, error) {
	if a == nil || a.svc == nil {
		return nil, core.NewInternalError("service backend not initialized", nil)
	}
	gwReq := convertHTTPGeminiRequest(req, core.ActionGenerateContent)
	resp, err := a.svc.Generate(ctx, gwReq)
	if err != nil {
		return nil, err
	}
	return convertToHTTPGeminiResponse(resp), nil
}

func (a *httpBackendAdapter) Stream(ctx context.Context, req *httpgemini.Request) (*httpgemini.StreamResult, error) {
	if a == nil || a.svc == nil {
		return nil, core.NewInternalError("service backend not initialized", nil)
	}
	gwReq := convertHTTPGeminiRequest(req, core.ActionStreamGenerateContent)
	result, err := a.svc.Stream(ctx, gwReq)
	if err != nil {
		return nil, err
	}
	return convertToHTTPGeminiStreamResult(ctx, result), nil
}

func (a *httpBackendAdapter) CountTokens(ctx context.Context, req *httpgemini.Request) (*httpgemini.Response, error) {
	if a == nil || a.svc == nil {
		return nil, core.NewInternalError("service backend not initialized", nil)
	}
	gwReq := convertHTTPGeminiRequest(req, core.ActionCountTokens)
	resp, err := a.svc.countTokensRequest(ctx, gwReq)
	if err != nil {
		return nil, err
	}
	return convertToHTTPGeminiResponse(resp), nil
}

// =========================
// 内部转换函数
// =========================

// convertHTTPGeminiRequest 把 HTTP 层的 gemini.Request 转换为统一 GatewayRequest。
// 注意：
// 1. Payload 直接传递所有权，不做额外复制
// 2. Headers / Query 在 HTTP 层请求处理周期内视为只读，这里直接传递引用
// 3. Metadata 由 handler 层按需分配，不存在共享修改问题
func convertHTTPGeminiRequest(req *httpgemini.Request, action core.Action) *core.GatewayRequest {
	if req == nil {
		return &core.GatewayRequest{
			Action:      action,
			RequestedAt: time.Now(),
		}
	}
	return &core.GatewayRequest{
		RequestID:   strings.TrimSpace(req.RequestID),
		Model:       strings.TrimSpace(req.Model),
		Action:      action,
		Headers:     req.Headers,
		Query:       req.Query,
		Payload:     req.Payload,
		Metadata:    req.Metadata,
		SessionID:   strings.TrimSpace(req.SessionID),
		Alt:         strings.TrimSpace(req.Alt),
		RequestedAt: time.Now(),
	}
}

// convertToHTTPGeminiResponse 把统一 GatewayResponse 转换为 HTTP 层的 gemini.Response。
// 这里直接传递引用，避免 Service -> HTTP 层之间的重复复制。
// 调用链保证该响应对象在返回后只读。
func convertToHTTPGeminiResponse(resp *core.GatewayResponse) *httpgemini.Response {
	if resp == nil {
		return nil
	}
	return &httpgemini.Response{
		RequestID:  resp.RequestID,
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		Payload:    resp.Payload,
		Metadata:   resp.Metadata,
	}
}

// convertToHTTPGeminiStreamResult 把统一 StreamResult 转换为 HTTP 层的 gemini.StreamResult。
// 这里不再对 headers 和每个 chunk 做重复 clone，减少流式路径分配。
func convertToHTTPGeminiStreamResult(ctx context.Context, result *core.StreamResult) *httpgemini.StreamResult {
	if result == nil {
		return nil
	}

	out := make(chan httpgemini.StreamChunk, 16)
	go func() {
		defer close(out)
		for chunk := range result.Chunks {
			select {
			case <-ctx.Done():
				return
			case out <- httpgemini.StreamChunk{
				Payload:  chunk.Payload,
				Metadata: chunk.Metadata,
				Err:      chunk.Err,
			}:
			}
		}
	}()

	return &httpgemini.StreamResult{
		RequestID:  result.RequestID,
		StatusCode: result.StatusCode,
		Headers:    result.Headers,
		Chunks:     out,
	}
}

// buildGatewayRequest 为 SDK 便捷方法构造统一请求。
// 这里做 payload 复制是因为调用方可能继续持有原始切片。
func buildGatewayRequest(model string, action core.Action, payload []byte, opts RequestOptions) *core.GatewayRequest {
	alt := strings.TrimSpace(opts.Alt)
	if alt == "" && len(opts.Query) > 0 {
		for _, key := range []string{"alt", "$alt"} {
			if value := strings.TrimSpace(opts.Query.Get(key)); value != "" {
				alt = value
				break
			}
		}
	}

	return &core.GatewayRequest{
		Model:       strings.TrimSpace(model),
		Action:      action,
		Headers:     cloneHeader(opts.Headers),
		Query:       cloneValues(opts.Query),
		Payload:     cloneBytes(payload),
		Metadata:    cloneAnyMap(opts.Metadata),
		SessionID:   strings.TrimSpace(opts.SessionID),
		Alt:         alt,
		RequestedAt: time.Now(),
	}
}

// =========================
// 模型信息 HTTP 表示
// =========================

// modelInfoToHTTPMap 将 ModelInfo 转换为适合 /v1beta/models 输出的 JSON 表示。
func modelInfoToHTTPMap(info *core.ModelInfo) map[string]any {
	if info == nil {
		return nil
	}

	out := map[string]any{
		"object":                     firstNonEmpty(info.Object, "model"),
		"name":                       firstNonEmpty(info.Name, "models/"+info.BaseName),
		"version":                    info.Version,
		"displayName":                info.DisplayName,
		"description":                info.Description,
		"inputTokenLimit":            info.InputTokenLimit,
		"outputTokenLimit":           info.OutputTokenLimit,
		"supportedGenerationMethods": cloneStringSlice(info.SupportedGenerationMethods),
	}

	if info.ContextLength > 0 {
		out["contextLength"] = info.ContextLength
	}
	if info.MaxCompletionTokens > 0 {
		out["maxCompletionTokens"] = info.MaxCompletionTokens
	}
	if len(info.SupportedInputModalities) > 0 {
		out["supportedInputModalities"] = cloneStringSlice(info.SupportedInputModalities)
	}
	if len(info.SupportedOutputModalities) > 0 {
		out["supportedOutputModalities"] = cloneStringSlice(info.SupportedOutputModalities)
	}
	if info.Thinking != nil {
		out["thinking"] = map[string]any{
			"min":              info.Thinking.Min,
			"max":              info.Thinking.Max,
			"zeroAllowed":      info.Thinking.ZeroAllowed,
			"dynamicAllowed":   info.Thinking.DynamicAllowed,
			"levels":           cloneStringSlice(info.Thinking.Levels),
			"supportsThinking": info.SupportsThinking,
		}
	}
	return out
}

// =========================
// 鉴权策略应用
// =========================

func applyAccessPolicy(manager *access.Manager, policy config.AccessPolicy, providerName string) error {
	if manager == nil {
		return nil
	}
	if !policy.Enabled {
		manager.SetProviders(nil)
		return nil
	}

	provider, err := access.NewStaticKeyProvider(access.StaticKeyProviderConfig{
		Name: providerName,
		Keys: cloneStringSlice(policy.Keys),
		Options: access.ExtractionOptions{
			AllowBearer: policy.AllowBearer,
			HeaderNames: cloneStringSlice(policy.HeaderNames),
			QueryNames:  cloneStringSlice(policy.QueryNames),
		},
	})
	if err != nil {
		return err
	}
	manager.SetProviders([]access.Provider{provider})
	return nil
}

// =========================
// 通用工具函数
// =========================

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
	default:
		return typed
	}
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
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
