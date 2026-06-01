package wsrelay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/internal/common"
	"aistudio-gemini-gateway/service"
	"github.com/gorilla/websocket"
)

var (
	errSessionClosed   = errors.New("wsrelay: websocket session closed")
	errSessionReplaced = errors.New("wsrelay: session replaced by newer connection")
)

// Manager 管理所有 Provider 的 WS 连接。
// 它负责：
//  1. WS 握手升级（含 Origin 校验、鉴权、Provider 身份提取）
//  2. 为每个 Provider 维护一条 session
//  3. 连接替换（同 ID 新连接覆盖旧连接）
//  4. 提供 Send / NonStream / Stream 等请求下发入口
//
// Manager 构造完成后配置不可变。
type Manager struct {
	path string

	handshakeTimeout time.Duration
	readTimeout      time.Duration
	writeTimeout     time.Duration
	maxMessageSize   int64
	pingInterval     time.Duration
	pongTimeout      time.Duration

	upgrader websocket.Upgrader

	accessManager   *access.Manager
	originCheck     func(*http.Request) bool
	providerFactory ProviderFactory

	logger Logger

	onConnected    ConnectedHandler
	onDisconnected DisconnectedHandler
	onTouched      TouchedHandler

	now func() time.Time

	mu       sync.RWMutex
	sessions map[string]*session
}

// NewManager 创建 WS Relay Manager。
func NewManager(opts Options) *Manager {
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}

	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = "/v1/ws"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	handshakeTimeout := opts.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}
	writeTimeout := opts.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 10 * time.Second
	}
	maxMessageSize := opts.MaxMessageSize
	if maxMessageSize <= 0 {
		maxMessageSize = 64 << 20 // 64 MiB
	}
	pingInterval := opts.PingInterval
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	pongTimeout := opts.PongTimeout
	if pongTimeout <= 0 {
		pongTimeout = 60 * time.Second
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Manager{
		path:             path,
		handshakeTimeout: handshakeTimeout,
		readTimeout:      readTimeout,
		writeTimeout:     writeTimeout,
		maxMessageSize:   maxMessageSize,
		pingInterval:     pingInterval,
		pongTimeout:      pongTimeout,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: handshakeTimeout,
			ReadBufferSize:   1024,
			WriteBufferSize:  1024,
			// Origin 在升级前通过 originCheck 手动校验，这里放行以保留可控错误响应。
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		accessManager:   opts.AccessManager,
		originCheck:     opts.OriginCheck,
		providerFactory: opts.ProviderFactory,
		logger:          logger,
		onConnected:     opts.OnConnected,
		onDisconnected:  opts.OnDisconnected,
		onTouched:       opts.OnTouched,
		now:             nowFn,
		sessions:        make(map[string]*session),
	}
}

// Path 返回 WS 升级路径。
func (m *Manager) Path() string {
	if m == nil || strings.TrimSpace(m.path) == "" {
		return "/v1/ws"
	}
	return m.path
}

// Handler 返回用于路由注册的 HTTP 处理器。
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(m.handleWebsocket)
}

// Stop 关闭所有当前在线的 Session。
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	_ = ctx

	m.mu.RLock()
	snapshot := make([]*session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		snapshot = append(snapshot, sess)
	}
	m.mu.RUnlock()

	for _, sess := range snapshot {
		if sess != nil {
			sess.cleanup(errors.New("wsrelay manager stopped"))
		}
	}
	return nil
}

// Provider 返回指定 Provider 的当前快照。
func (m *Manager) Provider(providerID string) *service.RuntimeProvider {
	if m == nil {
		return nil
	}
	sess := m.session(providerID)
	if sess == nil {
		return nil
	}
	return sess.providerSnapshot()
}

// Providers 返回当前所有在线 Provider 快照。
func (m *Manager) Providers() []*service.RuntimeProvider {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	snapshot := make([]*session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		snapshot = append(snapshot, sess)
	}
	m.mu.RUnlock()

	out := make([]*service.RuntimeProvider, 0, len(snapshot))
	for _, sess := range snapshot {
		if sess == nil {
			continue
		}
		out = append(out, sess.providerSnapshot())
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == nil && out[j] == nil {
			return false
		}
		if out[i] == nil {
			return false
		}
		if out[j] == nil {
			return true
		}
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}

// SessionCount 返回在线 Session 数量。
func (m *Manager) SessionCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Send 向指定 Provider 发送消息，并返回响应通道。
func (m *Manager) Send(ctx context.Context, providerID string, msg Message) (<-chan Message, error) {
	if m == nil {
		return nil, fmt.Errorf("wsrelay manager is nil")
	}
	sess := m.session(providerID)
	if sess == nil {
		return nil, fmt.Errorf("wsrelay provider %s not connected", strings.TrimSpace(providerID))
	}
	return sess.request(ctx, msg)
}

// session 获取指定 Provider 的当前 session。
func (m *Manager) session(providerID string) *session {
	if m == nil {
		return nil
	}
	key := providerMapKey(providerID)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[key]
}

// handleWebsocket 处理 WS 握手与升级。
func (m *Manager) handleWebsocket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !strings.EqualFold(r.Method, http.MethodGet) {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 调试日志：打印当前实际收到的 WS 请求路径与 query。
	// 说明：
	// 1. 这里使用 RedactValues 对 query 做脱敏，避免 key/auth_token 明文出现在日志中
	// 2. provider_label / provider_id 这类非敏感字段仍可见，便于排查身份提取问题
	// 3. 此日志为临时排查用途，问题定位完成后可删除
	if r.URL != nil {
		m.logger.InfoContext(ctx, "wsrelay incoming request",
			"request_id", common.RequestIDFromContext(ctx),
			"method", r.Method,
			"path", r.URL.Path,
			"raw_path", r.URL.RawPath,
			"raw_query", common.RedactValues(r.URL.Query()),
			"origin", r.Header.Get("Origin"),
			"remote_addr", r.RemoteAddr,
		)
	}

	if r.URL == nil || r.URL.Path != m.Path() {
		m.logger.WarnContext(ctx, "wsrelay path mismatch",
			"request_id", common.RequestIDFromContext(ctx),
			"expected_path", m.Path(),
			"actual_path", func() string {
				if r.URL == nil {
					return ""
				}
				return r.URL.Path
			}(),
			"raw_path", func() string {
				if r.URL == nil {
					return ""
				}
				return r.URL.RawPath
			}(),
			"raw_query", func() any {
				if r.URL == nil {
					return nil
				}
				return common.RedactValues(r.URL.Query())
			}(),
			"origin", r.Header.Get("Origin"),
			"remote_addr", r.RemoteAddr,
		)
		http.NotFound(w, r)
		return
	}

	// Origin 校验。
	if m.originCheck != nil && !m.originCheck(r) {
		origin := r.Header.Get("Origin")
		m.logger.WarnContext(ctx, "wsrelay origin rejected",
			"request_id", common.RequestIDFromContext(ctx),
			"origin", origin,
			"normalized_origin", normalizeOrigin(origin),
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	// WS 鉴权。
	var authResult *access.Result
	if m.accessManager != nil {
		result, authErr := m.accessManager.Authenticate(ctx, r)
		if authErr != nil {
			statusCode := authErr.HTTPStatusCode()
			if statusCode <= 0 {
				statusCode = http.StatusUnauthorized
			}
			m.logger.WarnContext(ctx, "wsrelay 认证失败",
				"status_code", statusCode,
				"error", authErr.Error(),
				"remote_addr", r.RemoteAddr,
			)
			http.Error(w, authErr.Error(), statusCode)
			return
		}
		authResult = result
	}

	// 提取 Provider 身份。
	baseProvider := &service.RuntimeProvider{
		ID:    "",
		Label: "",
		State: service.ProviderStateActive,
		Metadata: service.ProviderMetadata{
			ProviderType: "aistudio",
			Attributes:   map[string]string{},
			Extra:        map[string]any{},
		},
	}
	if m.providerFactory != nil {
		provider, err := m.providerFactory(ctx, r, authResult)
		if err != nil {
			m.logger.WarnContext(ctx, "wsrelay Provider 身份提取失败",
				"error", err,
				"remote_addr", r.RemoteAddr,
			)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if provider != nil {
			baseProvider = provider.Clone()
		}
	}

	// 补齐默认值。
	if strings.TrimSpace(baseProvider.ID) == "" {
		baseProvider.ID = common.GenerateProviderID()
	}
	if strings.TrimSpace(baseProvider.Label) == "" {
		baseProvider.Label = baseProvider.ID
	}
	if !baseProvider.State.Valid() {
		baseProvider.State = service.ProviderStateActive
	}
	if baseProvider.Metadata.ProviderType == "" {
		baseProvider.Metadata.ProviderType = "aistudio"
	}
	if baseProvider.Metadata.Attributes == nil {
		baseProvider.Metadata.Attributes = map[string]string{}
	}
	if authResult != nil {
		if baseProvider.Metadata.Attributes["auth_provider"] == "" {
			baseProvider.Metadata.Attributes["auth_provider"] = authResult.Provider
		}
		if baseProvider.Metadata.Attributes["auth_principal"] == "" {
			baseProvider.Metadata.Attributes["auth_principal"] = authResult.Principal
		}
	}

	now := m.now()
	baseProvider.ConnectionID = common.GenerateConnectionID()
	baseProvider.ConnectedAt = now
	baseProvider.LastSeenAt = now

	// 执行 WS 升级。
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		m.logger.WarnContext(ctx, "wsrelay 升级失败", "error", err)
		return
	}

	sess := newSession(conn, m, baseProvider)

	// 注册 session，处理可能的连接替换。
	var replaced *session
	key := providerMapKey(baseProvider.ID)

	m.mu.Lock()
	if existing := m.sessions[key]; existing != nil {
		replaced = existing
	}
	m.sessions[key] = sess
	m.mu.Unlock()

	// 先替换映射，再关闭旧连接，确保旧连接 cleanup 时不会误删新连接。
	if replaced != nil {
		replaced.cleanup(errSessionReplaced)
	}

	if m.onConnected != nil {
		m.onConnected(ctx, sess.providerSnapshot())
	}

	m.logger.InfoContext(ctx, "wsrelay Provider 已连接",
		"provider_id", baseProvider.ID,
		"connection_id", baseProvider.ConnectionID,
		"label", baseProvider.Label,
		"remote_addr", r.RemoteAddr,
	)

	go sess.run()
}

// handleSessionClosed 处理 session 关闭事件。
// 仅当 session 映射仍指向该 session 时才视为真正下线。
func (m *Manager) handleSessionClosed(sess *session, cause error) {
	if m == nil || sess == nil {
		return
	}
	provider := sess.providerSnapshot()
	key := providerMapKey(provider.ID)

	var shouldNotify bool

	m.mu.Lock()
	current := m.sessions[key]
	if current == sess {
		delete(m.sessions, key)
		shouldNotify = true
	}
	m.mu.Unlock()

	if !shouldNotify {
		return
	}

	ctx := context.Background()
	if cause == nil {
		cause = errSessionClosed
	}

	m.logger.InfoContext(ctx, "wsrelay Provider 已断开",
		"provider_id", provider.ID,
		"connection_id", provider.ConnectionID,
		"cause", cause.Error(),
	)

	if m.onDisconnected != nil {
		m.onDisconnected(ctx, provider, cause)
	}
}

// handleSessionTouched 处理 session 活跃信号，同步到 Registry。
func (m *Manager) handleSessionTouched(provider *service.RuntimeProvider) {
	if m == nil || provider == nil || m.onTouched == nil {
		return
	}
	m.onTouched(context.Background(), provider.Clone())
}

// providerMapKey 把 Provider ID 归一化为 map key。
func providerMapKey(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}
