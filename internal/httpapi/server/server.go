package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/internal/httpapi/gemini"
	"aistudio-gemini-gateway/internal/httpapi/middleware"
	"aistudio-gemini-gateway/internal/observability"
)

// StatusSource 是可选的状态总览数据源接口。
// 若 backend 实现了此接口，则：
// 1. GET / 返回生产状态总览 JSON（需要 HTTP API 鉴权）
// 2. GET /dashboard 返回内置可视化面板页面（无需鉴权，页面内自行携带 key 拉取 /）
type StatusSource interface {
	Status(ctx context.Context) (map[string]any, error)
}

// Options 是 HTTP Server 的构造选项。
type Options struct {
	// Config 是网关配置。
	Config *config.Config

	// Backend 是 Gemini HTTP 处理层依赖的抽象后端。
	Backend gemini.Backend

	// HTTPAccessManager 是 HTTP API 鉴权管理器。
	// 传入 nil 时，所有 HTTP 请求将跳过鉴权。
	HTTPAccessManager *access.Manager

	// WSHandler 是 WebSocket Provider 接入处理器，由 wsrelay.Manager 提供。
	// 传入 nil 时，不注册 WebSocket 路由。
	WSHandler http.Handler

	// Logger 是日志记录器。
	Logger observability.Logger
}

// Server 是 HTTP API 服务宿主。
// 服务构造完成后配置与路由即固定，不支持运行时变更。
type Server struct {
	cfg               *config.Config
	backend           gemini.Backend
	httpAccessManager *access.Manager
	wsHandler         http.Handler
	logger            observability.Logger

	handler    http.Handler
	httpServer *http.Server
}

// New 创建 HTTP Server。
func New(opts Options) (*Server, error) {
	cfg, err := config.Prepare(opts.Config)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if opts.Backend == nil {
		return nil, fmt.Errorf("http server requires non-nil backend")
	}

	logger := opts.Logger
	if logger == nil {
		logger = observability.NoopLogger{}
	}

	s := &Server{
		cfg:               cfg,
		backend:           opts.Backend,
		httpAccessManager: opts.HTTPAccessManager,
		wsHandler:         opts.WSHandler,
		logger:            logger,
	}

	rootHandler := s.buildHandler()
	s.handler = rootHandler

	readTimeout, _ := config.ParseDurationOrDefault(cfg.Server.ReadTimeout, 30*time.Second)
	writeTimeout, _ := config.ParseDurationOrDefault(cfg.Server.WriteTimeout, 0)
	idleTimeout, _ := config.ParseDurationOrDefault(cfg.Server.IdleTimeout, 120*time.Second)

	// 负值回落到 0，避免传入异常值给 http.Server。
	if writeTimeout < 0 {
		writeTimeout = 0
	}

	s.httpServer = &http.Server{
		Addr:         s.listenAddr(),
		Handler:      rootHandler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	return s, nil
}

// Handler 返回可嵌入宿主项目的 HTTP Handler。
// 宿主项目可将其注册到自己的 HTTP Server 或路由中。
func (s *Server) Handler() http.Handler {
	if s == nil {
		return nil
	}
	return s.handler
}

// Start 启动 HTTP 服务。
// 该方法为阻塞调用，直到服务被关闭或出错才返回。
func (s *Server) Start() error {
	if s == nil || s.httpServer == nil {
		return fmt.Errorf("http server not initialized")
	}

	cfg := s.cfg
	useTLS := cfg != nil && cfg.Server.TLS.Enabled
	if useTLS {
		cert := strings.TrimSpace(cfg.Server.TLS.CertFile)
		key := strings.TrimSpace(cfg.Server.TLS.KeyFile)
		if cert == "" || key == "" {
			return fmt.Errorf("tls enabled but cert-file or key-file is empty")
		}
		if err := s.httpServer.ListenAndServeTLS(cert, key); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 优雅停止服务。
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.httpServer.Shutdown(ctx)
}

// buildHandler 构建完整的 HTTP 路由与中间件链。
//
// 路由结构：
//
//	/healthz                                 -> 健康检查（无鉴权）
//	/                                        -> 生产状态总览 JSON（HTTP 鉴权，仅 backend 支持时注册）
//	/dashboard                               -> 内置可视化状态面板（无鉴权，仅 backend 支持时注册）
//	/v1beta/models                           -> Gemini API（HTTP 鉴权）
//	/v1beta/models/                          -> Gemini API（HTTP 鉴权）
//	<websocket.path>                         -> WebSocket Provider 接入（WS 自有鉴权）
func (s *Server) buildHandler() http.Handler {
	geminiHandler := gemini.NewHandler(s.backend, s.logger, 32<<20)
	rootMux := http.NewServeMux()

	// withCommon 返回所有路由共享的基础中间件：
	// RequestID + Recover + 可选 AccessLog。
	withCommon := func(extra ...middleware.Middleware) []middleware.Middleware {
		out := make([]middleware.Middleware, 0, 6)
		out = append(out, middleware.RequestID(), middleware.Recover(s.logger))
		if s.cfg != nil && s.cfg.Logging.AccessLog {
			out = append(out, middleware.AccessLog(s.logger))
		}
		out = append(out, extra...)
		return out
	}

	// 健康检查：不需要鉴权和 BodyLimit。
	rootMux.Handle("/healthz", middleware.Chain(
		http.HandlerFunc(s.handleHealth),
		withCommon(middleware.CORS(s.cfg.Access.CORS))...,
	))

	// 如果 backend 提供状态总览能力，则：
	// 1. GET / 返回状态 JSON（需要 HTTP 鉴权）
	// 2. GET /dashboard 返回内置监控页面（无需鉴权，仅静态页）
	if statusSource, ok := s.backend.(StatusSource); ok {
		// 根路径状态总览接口：需要 HTTP API 鉴权。
		rootMux.Handle("/", middleware.Chain(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.handleStatus(w, r, statusSource)
			}),
			withCommon(
				middleware.CORS(s.cfg.Access.CORS),
				middleware.Auth(s.httpAccessManager),
			)...,
		))

		// 内置 Dashboard：匿名可访问。
		// 页面本身不包含敏感数据，只是静态资源；
		// 页面内部会要求用户输入 HTTP key，并通过同源请求去拉取 GET / 的状态 JSON。
		dashboardHandler := middleware.Chain(
			http.HandlerFunc(s.handleDashboard),
			withCommon()...,
		)
		rootMux.Handle("/dashboard", dashboardHandler)
		rootMux.Handle("/dashboard/", dashboardHandler)
	}

	// Gemini HTTP API：需要 CORS + BodyLimit + Auth。
	modelsHandler := middleware.Chain(
		http.Handler(geminiHandler),
		withCommon(
			middleware.CORS(s.cfg.Access.CORS),
			middleware.BodyLimit(32<<20),
			middleware.Auth(s.httpAccessManager),
		)...,
	)
	rootMux.Handle("/v1beta/models", modelsHandler)
	rootMux.Handle("/v1beta/models/", modelsHandler)

	// WebSocket Provider 接入：鉴权由 wsrelay 自己处理，不套 HTTP API auth。
	if s.wsHandler != nil {
		wsPath := "/v1/ws"
		if strings.TrimSpace(s.cfg.WebSocket.Path) != "" {
			wsPath = strings.TrimSpace(s.cfg.WebSocket.Path)
		}
		rootMux.Handle(wsPath, middleware.Chain(
			s.wsHandler,
			withCommon()...,
		))
	}

	return rootMux
}

// handleHealth 处理健康检查请求。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	payload := map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeJSON(w, http.StatusOK, payload)
}

// handleStatus 返回生产状态总览 JSON。
// 仅精确响应 GET /。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, source StatusSource) {
	// 只响应精确的 GET /，其他路径返回 404。
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !strings.EqualFold(r.Method, http.MethodGet) {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": map[string]any{"code": 405, "message": "method not allowed"},
		})
		return
	}

	data, err := source.Status(r.Context())
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleDashboard 返回内置的监控页面。
// 页面本身不包含敏感数据，因此允许匿名访问；
// 页面内部会通过用户输入的 HTTP 密钥，向同源 GET / 发起鉴权请求。
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dashboard" && r.URL.Path != "/dashboard/" {
		http.NotFound(w, r)
		return
	}
	if !strings.EqualFold(r.Method, http.MethodGet) {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.TrimSpace(dashboardHTML) == "" {
		http.Error(w, "dashboard asset is empty", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Frame-Options", "SAMEORIGIN")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dashboardHTML))
}

// listenAddr 返回 HTTP 监听地址。
func (s *Server) listenAddr() string {
	host := config.DefaultHost
	port := config.DefaultPort

	if s.cfg != nil {
		if strings.TrimSpace(s.cfg.Server.Host) != "" {
			host = strings.TrimSpace(s.cfg.Server.Host)
		}
		if s.cfg.Server.Port > 0 {
			port = s.cfg.Server.Port
		}
	}

	return net.JoinHostPort(host, strconv.Itoa(port))
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}