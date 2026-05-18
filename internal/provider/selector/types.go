package selector

import (
	"context"
	"strings"
	"sync"
	"time"

	sessionpkg "aistudio-gemini-gateway/internal/provider/session"
	"aistudio-gemini-gateway/service"
)

// Logger 是 selector 内部使用的最小日志接口。
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

// Request 是 selector 使用的轻量选择请求。
// selector 不依赖完整 GatewayRequest，也不自行提取 session。
type Request struct {
	RequestID string
	Model     string
	SessionID string
}

// Selector 是 Provider 选择器接口。
type Selector interface {
	Name() string
	Select(ctx context.Context, req Request, providers []*service.RuntimeProvider, tried map[string]struct{}) (*service.RuntimeProvider, error)
}

// Options 是构造 Selector 的选项。
type Options struct {
	Logger        Logger
	CursorLimit   int
	AffinityTTL   time.Duration
	AffinityCache *sessionpkg.Cache
}

// New 根据策略名创建内置选择器。
// 支持：round_robin / fill_first / session_affinity。
func New(strategy string, opts Options) Selector {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	switch strategy {
	case "", "round_robin":
		return NewRoundRobinSelector(opts)
	case "fill_first":
		return NewFillFirstSelector(opts)
	case "session_affinity":
		return NewSessionAffinitySelector(opts)
	default:
		return NewRoundRobinSelector(opts)
	}
}

// RoundRobinSelector 按模型维度做轮询选择。
type RoundRobinSelector struct {
	logger  Logger
	maxKeys int
	mu      sync.Mutex
	cursors map[string]int
}

// NewRoundRobinSelector 创建轮询选择器。
func NewRoundRobinSelector(opts Options) *RoundRobinSelector {
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	limit := opts.CursorLimit
	if limit <= 0 {
		limit = 4096
	}
	return &RoundRobinSelector{
		logger:  logger,
		maxKeys: limit,
		cursors: make(map[string]int),
	}
}

// Name 返回策略名。
func (s *RoundRobinSelector) Name() string {
	return "round_robin"
}

// Select 执行轮询选择。
func (s *RoundRobinSelector) Select(ctx context.Context, req Request, providers []*service.RuntimeProvider, tried map[string]struct{}) (*service.RuntimeProvider, error) {
	now := time.Now()
	ready, summary := collectReadyProviders(req.Model, providers, tried, now)
	if len(ready) == 0 {
		return nil, selectionErrorFromSummary(req.Model, summary, now)
	}

	key := cursorKeyForModel(req.Model)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cursors == nil {
		s.cursors = make(map[string]int)
	}
	if _, exists := s.cursors[key]; !exists && len(s.cursors) >= s.maxKeys {
		s.cursors = make(map[string]int)
	}

	index := s.cursors[key]
	if index >= 2_147_483_640 {
		index = 0
	}
	s.cursors[key] = index + 1

	selected := ready[index%len(ready)].Clone()
	s.logger.DebugContext(ctx, "selector picked provider",
		"strategy", s.Name(),
		"model", req.Model,
		"provider_id", selected.ID,
		"candidate_count", len(ready),
	)
	return selected, nil
}

// FillFirstSelector 总是优先选择稳定排序后的第一个可用 Provider。
type FillFirstSelector struct {
	logger Logger
}

// NewFillFirstSelector 创建 FillFirst 选择器。
func NewFillFirstSelector(opts Options) *FillFirstSelector {
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	return &FillFirstSelector{logger: logger}
}

// Name 返回策略名。
func (s *FillFirstSelector) Name() string {
	return "fill_first"
}

// Select 执行选择。
func (s *FillFirstSelector) Select(ctx context.Context, req Request, providers []*service.RuntimeProvider, tried map[string]struct{}) (*service.RuntimeProvider, error) {
	now := time.Now()
	ready, summary := collectReadyProviders(req.Model, providers, tried, now)
	if len(ready) == 0 {
		return nil, selectionErrorFromSummary(req.Model, summary, now)
	}

	selected := ready[0].Clone()
	s.logger.DebugContext(ctx, "selector picked provider",
		"strategy", s.Name(),
		"model", req.Model,
		"provider_id", selected.ID,
		"candidate_count", len(ready),
	)
	return selected, nil
}

// SessionAffinitySelector 实现会话亲和选择。
// 规则：
// 1. 命中绑定时优先复用原 Provider
// 2. 未命中时回退到 round_robin
// 3. 绑定键固定为 model + sessionID
type SessionAffinitySelector struct {
	logger   Logger
	cache    *sessionpkg.Cache
	fallback Selector
}

// NewSessionAffinitySelector 创建会话亲和选择器。
func NewSessionAffinitySelector(opts Options) *SessionAffinitySelector {
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	cache := opts.AffinityCache
	if cache == nil {
		cache = sessionpkg.NewCache(opts.AffinityTTL)
	}

	fallback := NewRoundRobinSelector(opts)

	return &SessionAffinitySelector{
		logger:   logger,
		cache:    cache,
		fallback: fallback,
	}
}

// Name 返回策略名。
func (s *SessionAffinitySelector) Name() string {
	return "session_affinity"
}

// Select 执行会话亲和选择。
func (s *SessionAffinitySelector) Select(ctx context.Context, req Request, providers []*service.RuntimeProvider, tried map[string]struct{}) (*service.RuntimeProvider, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return s.fallback.Select(ctx, req, providers, tried)
	}

	now := time.Now()
	ready, summary := collectReadyProviders(req.Model, providers, tried, now)
	if len(ready) == 0 {
		return nil, selectionErrorFromSummary(req.Model, summary, now)
	}

	bindingKey := sessionpkg.BuildBindingKey(req.Model, sessionID)
	if bindingKey != "" && s.cache != nil {
		if providerID, ok := s.cache.GetAndRefresh(bindingKey); ok {
			for _, item := range ready {
				if item != nil && strings.EqualFold(item.ID, providerID) {
					selected := item.Clone()
					s.logger.DebugContext(ctx, "session affinity hit",
						"strategy", s.Name(),
						"model", req.Model,
						"session_id", sessionID,
						"provider_id", selected.ID,
					)
					return selected, nil
				}
			}
			// 已绑定 Provider 不再可用，失效旧绑定。
			s.cache.Invalidate(bindingKey)
		}
	}

	selected, err := s.fallback.Select(ctx, req, ready, nil)
	if err != nil {
		return nil, err
	}
	if selected != nil && bindingKey != "" && s.cache != nil {
		s.cache.Set(bindingKey, selected.ID)
	}

	s.logger.DebugContext(ctx, "session affinity bind provider",
		"strategy", s.Name(),
		"model", req.Model,
		"session_id", sessionID,
		"provider_id", selected.ID,
	)
	return selected, nil
}

// HandleProviderEvent 处理 Provider 生命周期事件。
// 当 Provider 被替换、断开、禁用或冷却时，主动失效其亲和绑定。
func (s *SessionAffinitySelector) HandleProviderEvent(ctx context.Context, event service.ProviderEvent) {
	if s == nil || s.cache == nil || event.Provider == nil {
		return
	}

	needInvalidate := false

	switch event.Type {
	case service.ProviderEventDisconnected, service.ProviderEventReplaced:
		needInvalidate = true
	case service.ProviderEventStateChanged:
		switch event.Provider.State {
		case service.ProviderStateCooling, service.ProviderStateDisabled, service.ProviderStateDisconnected:
			needInvalidate = true
		}
	}

	if !needInvalidate {
		return
	}

	s.cache.InvalidateProvider(event.Provider.ID)
	s.logger.DebugContext(ctx, "session affinity invalidated provider bindings",
		"provider_id", event.Provider.ID,
		"event_type", string(event.Type),
		"state", string(event.Provider.State),
	)
}

// Stop 停止内部缓存后台协程。
func (s *SessionAffinitySelector) Stop() {
	if s == nil || s.cache == nil {
		return
	}
	s.cache.Stop()
}
