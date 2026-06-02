package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"aistudio-gemini-gateway/service"
)

// Logger 是 registry 内部使用的最小日志接口。
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

// Options 是 Registry 的构造参数。
type Options struct {
	Logger Logger
	Now    func() time.Time
}

const maxRecentEvents = 64

// ProviderDiagnostics 是每个 Provider 的运行时诊断信息。
// 该信息不影响选路逻辑，仅用于状态总览接口的展示。
type ProviderDiagnostics struct {
	LastError            string
	LastErrorAt          time.Time
	LastDisconnectReason string
	LastDisconnectAt     time.Time
	LastStateChange      string // 例如 "active -> cooling"
	LastStateReason      string
	LastStateAt          time.Time
}

// eventRingBuffer 是固定容量的 Provider 事件环形缓冲区。
// 用于在状态总览接口中展示最近发生的 Provider 生命周期事件。
type eventRingBuffer struct {
	mu     sync.Mutex
	events []service.ProviderEvent
}

func newEventRingBuffer() *eventRingBuffer {
	return &eventRingBuffer{
		events: make([]service.ProviderEvent, 0, maxRecentEvents),
	}
}

// Add 记录一条事件。超过容量时自动淘汰最旧事件。
func (b *eventRingBuffer) Add(event service.ProviderEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 浅拷贝事件，深拷贝 Provider 和 Metadata 避免后续被外部修改。
	stored := event
	if event.Provider != nil {
		stored.Provider = event.Provider.Clone()
	}
	if len(event.Metadata) > 0 {
		meta := make(map[string]any, len(event.Metadata))
		for k, v := range event.Metadata {
			meta[k] = v
		}
		stored.Metadata = meta
	}

	b.events = append(b.events, stored)
	if len(b.events) > maxRecentEvents {
		copy(b.events, b.events[len(b.events)-maxRecentEvents:])
		b.events = b.events[:maxRecentEvents]
	}
}

// Recent 返回最近 n 条事件（最新在前）。
func (b *eventRingBuffer) Recent(n int) []service.ProviderEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := len(b.events)
	if n <= 0 || n > total {
		n = total
	}

	out := make([]service.ProviderEvent, n)
	for i := 0; i < n; i++ {
		src := b.events[total-1-i]
		out[i] = src
		if src.Provider != nil {
			out[i].Provider = src.Provider.Clone()
		}
		if len(src.Metadata) > 0 {
			meta := make(map[string]any, len(src.Metadata))
			for k, v := range src.Metadata {
				meta[k] = v
			}
			out[i].Metadata = meta
		}
	}
	return out
}

// Registry 管理运行时在线 Provider。
// 核心规则：
//  1. Provider 不声明模型支持范围，模型真值源来自静态模型注册表
//  2. 冷却恢复由定时器驱动，读路径不做隐式写恢复
type Registry struct {
	mu             sync.RWMutex
	providers      map[string]*service.RuntimeProvider
	cooldownTimers map[string]*time.Timer
	diagnostics    map[string]*ProviderDiagnostics

	subscribers map[uint64]chan service.ProviderEvent
	nextSubID   uint64

	recentEvents *eventRingBuffer

	logger Logger
	now    func() time.Time
}

// New 创建新的 Registry。
func New(opts Options) *Registry {
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Registry{
		providers:      make(map[string]*service.RuntimeProvider),
		cooldownTimers: make(map[string]*time.Timer),
		diagnostics:    make(map[string]*ProviderDiagnostics),
		subscribers:    make(map[uint64]chan service.ProviderEvent),
		recentEvents:   newEventRingBuffer(),
		logger:         logger,
		now:            nowFn,
	}
}

// HandleProviderConnected 适配 wsrelay.OnConnected 回调。
func (r *Registry) HandleProviderConnected(ctx context.Context, provider *service.RuntimeProvider) {
	_, _, err := r.Register(provider)
	if err != nil {
		r.logger.ErrorContext(ensureContext(ctx), "registry failed to register provider",
			"provider_id", providerIDOf(provider),
			"error", err,
		)
	}
}

// HandleProviderDisconnected 适配 wsrelay.OnDisconnected 回调。
func (r *Registry) HandleProviderDisconnected(ctx context.Context, provider *service.RuntimeProvider, cause error) {
	if provider == nil {
		return
	}
	_, _ = r.Disconnect(provider.ID, provider.ConnectionID, cause)
}

// HandleProviderTouched 适配 wsrelay.OnTouched 回调。
// 只更新 last_seen，不做冷却恢复。
func (r *Registry) HandleProviderTouched(ctx context.Context, provider *service.RuntimeProvider) {
	_ = ctx
	if provider == nil {
		return
	}
	_, _ = r.Touch(provider.ID, provider.ConnectionID, provider.LastSeenAt)
}

// Register 注册或更新 Provider。
// 若同一 Provider ID 已存在但 ConnectionID 不同，则视为连接替换。
func (r *Registry) Register(provider *service.RuntimeProvider) (*service.RuntimeProvider, *service.RuntimeProvider, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("registry is nil")
	}
	now := r.now()

	normalized, err := normalizeProvider(provider, now)
	if err != nil {
		return nil, nil, err
	}

	var (
		event    *service.ProviderEvent
		replaced *service.RuntimeProvider
		current  *service.RuntimeProvider
	)

	key := providerMapKey(normalized.ID)

	r.mu.Lock()
	existing := r.providers[key]

	switch {
	case existing == nil:
		// 新 Provider 注册。
		r.providers[key] = normalized
		r.syncCooldownTimerLocked(key, normalized.ID, normalized.State, normalized.CooldownUntil)
		current = normalized.Clone()

		event = &service.ProviderEvent{
			Type:     service.ProviderEventConnected,
			Provider: current.Clone(),
			At:       now,
			Message:  "provider connected",
		}

	case existing.ConnectionID != normalized.ConnectionID:
		// 连接替换：新连接成为当前有效 Provider。
		replaced = existing.Clone()
		r.stopCooldownTimerLocked(key)
		r.providers[key] = normalized
		r.syncCooldownTimerLocked(key, normalized.ID, normalized.State, normalized.CooldownUntil)
		current = normalized.Clone()

		event = &service.ProviderEvent{
			Type:     service.ProviderEventReplaced,
			Provider: current.Clone(),
			At:       now,
			Message:  "provider connection replaced",
			Metadata: map[string]any{
				"old_connection_id": replaced.ConnectionID,
				"new_connection_id": current.ConnectionID,
			},
		}

	default:
		// 同一连接的重复注册，视为幂等更新。
		normalized.ConnectedAt = existing.ConnectedAt
		if normalized.LastSeenAt.Before(existing.LastSeenAt) {
			normalized.LastSeenAt = existing.LastSeenAt
		}
		r.providers[key] = normalized
		r.syncCooldownTimerLocked(key, normalized.ID, normalized.State, normalized.CooldownUntil)
		current = normalized.Clone()
	}
	r.mu.Unlock()

	if event != nil {
		r.publish(context.Background(), *event)
	}
	return current, replaced, nil
}

// Disconnect 按 providerID + connectionID 断开 Provider。
// 若 connectionID 为空，则只按 providerID 匹配。
func (r *Registry) Disconnect(providerID, connectionID string, cause error) (*service.RuntimeProvider, bool) {
	if r == nil {
		return nil, false
	}
	providerID = strings.TrimSpace(providerID)
	connectionID = strings.TrimSpace(connectionID)
	if providerID == "" {
		return nil, false
	}

	var removed *service.RuntimeProvider
	now := r.now()
	key := providerMapKey(providerID)

	r.mu.Lock()
	current := r.providers[key]
	if current != nil {
		if connectionID == "" || strings.EqualFold(current.ConnectionID, connectionID) {
			removed = current.Clone()
			delete(r.providers, key)
			r.stopCooldownTimerLocked(key)

			// 记录断连诊断。
			diag := r.ensureDiagnosticsLocked(key)
			if cause != nil {
				diag.LastDisconnectReason = cause.Error()
			} else {
				diag.LastDisconnectReason = "unknown"
			}
			diag.LastDisconnectAt = now
		}
	}
	r.mu.Unlock()

	if removed == nil {
		return nil, false
	}

	metadata := map[string]any{}
	if cause != nil {
		metadata["cause"] = cause.Error()
	}

	r.publish(context.Background(), service.ProviderEvent{
		Type:     service.ProviderEventDisconnected,
		Provider: removed.Clone(),
		At:       now,
		Message:  "provider disconnected",
		Metadata: metadata,
	})
	return removed, true
}

// Touch 更新 Provider 的最后活跃时间。
func (r *Registry) Touch(providerID, connectionID string, at time.Time) (*service.RuntimeProvider, bool) {
	if r == nil {
		return nil, false
	}
	providerID = strings.TrimSpace(providerID)
	connectionID = strings.TrimSpace(connectionID)
	if providerID == "" {
		return nil, false
	}
	if at.IsZero() {
		at = r.now()
	}

	var updated *service.RuntimeProvider
	key := providerMapKey(providerID)

	r.mu.Lock()
	if current := r.providers[key]; current != nil {
		if connectionID == "" || strings.EqualFold(current.ConnectionID, connectionID) {
			current.LastSeenAt = at
			updated = current.Clone()
		}
	}
	r.mu.Unlock()

	return updated, updated != nil
}

// SetState 设置 Provider 状态。
// cooling 状态应通过 SetCooldown 进入；disconnected 状态应使用 Disconnect。
func (r *Registry) SetState(providerID string, state service.ProviderState, message string) (*service.RuntimeProvider, bool, error) {
	if r == nil {
		return nil, false, fmt.Errorf("registry is nil")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, false, fmt.Errorf("provider id cannot be empty")
	}
	if !state.Valid() {
		return nil, false, fmt.Errorf("invalid provider state: %s", state)
	}
	if state == service.ProviderStateCooling {
		return nil, false, fmt.Errorf("use SetCooldown to enter cooling state")
	}
	if state == service.ProviderStateDisconnected {
		state = service.ProviderStateDisabled
	}

	now := r.now()
	key := providerMapKey(providerID)

	var (
		out   *service.RuntimeProvider
		event *service.ProviderEvent
	)

	r.mu.Lock()
	current := r.providers[key]
	if current == nil {
		r.mu.Unlock()
		return nil, false, nil
	}

	oldState := current.State
	oldCooldownUntil := current.CooldownUntil

	current.State = state
	current.CooldownUntil = time.Time{}
	current.LastSeenAt = now
	r.stopCooldownTimerLocked(key)

	out = current.Clone()
	if oldState != current.State || !sameTime(oldCooldownUntil, current.CooldownUntil) {
		event = &service.ProviderEvent{
			Type:     service.ProviderEventStateChanged,
			Provider: out.Clone(),
			At:       now,
			Message:  firstNonEmpty(message, "provider state changed"),
			Metadata: map[string]any{
				"from":               string(oldState),
				"to":                 string(current.State),
				"old_cooldown_until": oldCooldownUntil,
				"cooldown_until":     current.CooldownUntil,
			},
		}
	}

	// 记录状态变更诊断。
	diag := r.ensureDiagnosticsLocked(key)
	diag.LastStateChange = string(oldState) + " -> " + string(current.State)
	diag.LastStateReason = firstNonEmpty(message, "state changed")
	diag.LastStateAt = now
	r.mu.Unlock()

	if event != nil {
		r.publish(context.Background(), *event)
	}
	return out, true, nil
}

// SetCooldown 设置 Provider 冷却截止时间。
// 冷却恢复由定时器驱动，到期后自动切换回 active。
func (r *Registry) SetCooldown(providerID string, until time.Time, message string) (*service.RuntimeProvider, bool) {
	if r == nil {
		return nil, false
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, false
	}

	now := r.now()
	key := providerMapKey(providerID)

	var (
		out   *service.RuntimeProvider
		event *service.ProviderEvent
	)

	r.mu.Lock()
	current := r.providers[key]
	if current == nil {
		r.mu.Unlock()
		return nil, false
	}

	oldState := current.State
	oldCooldownUntil := current.CooldownUntil

	if until.IsZero() || !until.After(now) {
		current.State = service.ProviderStateActive
		current.CooldownUntil = time.Time{}
		r.stopCooldownTimerLocked(key)
	} else {
		current.State = service.ProviderStateCooling
		current.CooldownUntil = until
		r.replaceCooldownTimerLocked(key, providerID, until)
	}
	current.LastSeenAt = now
	out = current.Clone()

	if oldState != current.State || !sameTime(oldCooldownUntil, current.CooldownUntil) {
		event = &service.ProviderEvent{
			Type:     service.ProviderEventStateChanged,
			Provider: out.Clone(),
			At:       now,
			Message:  firstNonEmpty(message, "provider cooldown updated"),
			Metadata: map[string]any{
				"from":               string(oldState),
				"to":                 string(current.State),
				"old_cooldown_until": oldCooldownUntil,
				"cooldown_until":     current.CooldownUntil,
			},
		}
	}

	// 记录状态变更诊断。
	diag := r.ensureDiagnosticsLocked(key)
	diag.LastStateChange = string(oldState) + " -> " + string(current.State)
	diag.LastStateReason = firstNonEmpty(message, "cooldown updated")
	diag.LastStateAt = now
	r.mu.Unlock()

	if event != nil {
		r.publish(context.Background(), *event)
	}
	return out, true
}

// ClearCooldown 清除 Provider 冷却。
func (r *Registry) ClearCooldown(providerID string, message string) (*service.RuntimeProvider, bool) {
	return r.SetCooldown(providerID, time.Time{}, firstNonEmpty(message, "provider cooldown cleared"))
}

// Get 获取指定 Provider 快照。
func (r *Registry) Get(providerID string) (*service.RuntimeProvider, bool) {
	if r == nil {
		return nil, false
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, false
	}

	key := providerMapKey(providerID)

	r.mu.RLock()
	current := r.providers[key]
	r.mu.RUnlock()

	if current == nil {
		return nil, false
	}
	return current.Clone(), true
}

// List 返回当前所有 Provider 快照。
func (r *Registry) List() []*service.RuntimeProvider {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	out := make([]*service.RuntimeProvider, 0, len(r.providers))
	for _, item := range r.providers {
		out = append(out, item.Clone())
	}
	r.mu.RUnlock()

	sortProviders(out)
	return out
}

// ListSelectable 返回当前可供 selector 评估的 Provider 快照。
// 不按模型筛选；selector 自行处理 cooling / tried / priority。
func (r *Registry) ListSelectable() []*service.RuntimeProvider {
	return r.List()
}

// Count 返回当前 Provider 数量。
func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}

// Subscribe 订阅 Provider 生命周期事件。
// 返回事件 channel 与取消函数。
func (r *Registry) Subscribe(buffer int) (<-chan service.ProviderEvent, func()) {
	if r == nil {
		ch := make(chan service.ProviderEvent)
		close(ch)
		return ch, func() {}
	}
	if buffer <= 0 {
		buffer = 16
	}

	ch := make(chan service.ProviderEvent, buffer)

	r.mu.Lock()
	r.nextSubID++
	id := r.nextSubID
	r.subscribers[id] = ch
	r.mu.Unlock()

	cancel := func() {
		r.mu.Lock()
		existing, ok := r.subscribers[id]
		if ok {
			delete(r.subscribers, id)
			close(existing)
		}
		r.mu.Unlock()
	}
	return ch, cancel
}

// RecordProviderError 记录 Provider 最近一次错误，仅用于状态总览展示。
func (r *Registry) RecordProviderError(providerID string, message string) {
	if r == nil {
		return
	}
	key := providerMapKey(providerID)
	now := r.now()

	r.mu.Lock()
	diag := r.ensureDiagnosticsLocked(key)
	diag.LastError = message
	diag.LastErrorAt = now
	r.mu.Unlock()
}

// AllDiagnostics 返回所有 Provider 的诊断信息副本。
func (r *Registry) AllDiagnostics() map[string]*ProviderDiagnostics {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make(map[string]*ProviderDiagnostics, len(r.diagnostics))
	for k, v := range r.diagnostics {
		cloned := *v
		out[k] = &cloned
	}
	return out
}

// RecentEvents 返回最近 limit 条 Provider 生命周期事件（最新在前）。
func (r *Registry) RecentEvents(limit int) []service.ProviderEvent {
	if r == nil || r.recentEvents == nil {
		return nil
	}
	return r.recentEvents.Recent(limit)
}

func (r *Registry) ensureDiagnosticsLocked(key string) *ProviderDiagnostics {
	if diag, ok := r.diagnostics[key]; ok {
		return diag
	}
	diag := &ProviderDiagnostics{}
	r.diagnostics[key] = diag
	return diag
}

// publish 通知所有事件订阅者。
// 持有 RLock 期间完成非阻塞发送；由于 cancel() 需要 WLock，
// 在 RLock 持有期间 cancel() 无法执行 close(ch)，避免 send on closed channel。
func (r *Registry) publish(ctx context.Context, event service.ProviderEvent) {
	if r == nil {
		return
	}
	ctx = ensureContext(ctx)
	// 记录到最近事件缓冲区，供状态总览接口展示。
	if r.recentEvents != nil {
		r.recentEvents.Add(event)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, ch := range r.subscribers {
		ev := *event.Clone()
		select {
		case ch <- ev:
		default:
			r.logger.WarnContext(ctx, "provider event subscriber channel is full, dropping event",
				"provider_id", providerIDFromEvent(event),
				"event_type", string(event.Type),
			)
		}
	}
}

// handleCooldownTimer 是冷却定时器的回调。
// 当定时器到期时，自动将 Provider 恢复为 active 状态。
func (r *Registry) handleCooldownTimer(providerID string, expectedUntil time.Time) {
	if r == nil {
		return
	}

	now := r.now()
	key := providerMapKey(providerID)

	var event *service.ProviderEvent

	r.mu.Lock()
	current := r.providers[key]
	if current == nil {
		r.mu.Unlock()
		return
	}

	// 若期间 cooldown 已被更新或清除，则该 timer 已失效，直接忽略。
	if current.State != service.ProviderStateCooling || !sameTime(current.CooldownUntil, expectedUntil) {
		r.mu.Unlock()
		return
	}

	// 若还未到期（时钟边界情况），也不执行恢复。
	if current.CooldownUntil.After(now) {
		r.mu.Unlock()
		return
	}

	oldState := current.State
	current.State = service.ProviderStateActive
	current.CooldownUntil = time.Time{}
	current.LastSeenAt = now
	delete(r.cooldownTimers, key)

	event = &service.ProviderEvent{
		Type:     service.ProviderEventStateChanged,
		Provider: current.Clone(),
		At:       now,
		Message:  "provider cooldown expired",
		Metadata: map[string]any{
			"from": string(oldState),
			"to":   string(service.ProviderStateActive),
		},
	}
	r.mu.Unlock()

	r.publish(context.Background(), *event)
}

// syncCooldownTimerLocked 根据当前 Provider 状态同步冷却定时器。
// 调用方必须持有 r.mu 写锁。
func (r *Registry) syncCooldownTimerLocked(key, providerID string, state service.ProviderState, until time.Time) {
	switch state {
	case service.ProviderStateCooling:
		if until.IsZero() {
			// 不允许保留"无截止时间的 cooling"，退化为 active。
			if current := r.providers[key]; current != nil {
				current.State = service.ProviderStateActive
				current.CooldownUntil = time.Time{}
			}
			r.stopCooldownTimerLocked(key)
			return
		}
		r.replaceCooldownTimerLocked(key, providerID, until)
	default:
		r.stopCooldownTimerLocked(key)
	}
}

// replaceCooldownTimerLocked 重置冷却定时器。
// 调用方必须持有 r.mu 写锁。
func (r *Registry) replaceCooldownTimerLocked(key, providerID string, until time.Time) {
	if timer := r.cooldownTimers[key]; timer != nil {
		timer.Stop()
		delete(r.cooldownTimers, key)
	}
	if until.IsZero() {
		return
	}

	delay := time.Until(until)
	if delay < 0 {
		delay = 0
	}

	r.cooldownTimers[key] = time.AfterFunc(delay, func() {
		r.handleCooldownTimer(providerID, until)
	})
}

// stopCooldownTimerLocked 停止冷却定时器。
// 调用方必须持有 r.mu 写锁。
func (r *Registry) stopCooldownTimerLocked(key string) {
	if timer := r.cooldownTimers[key]; timer != nil {
		timer.Stop()
		delete(r.cooldownTimers, key)
	}
}

func normalizeProvider(provider *service.RuntimeProvider, now time.Time) (*service.RuntimeProvider, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	out := provider.Clone()
	if out == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	out.ID = strings.TrimSpace(out.ID)
	out.ConnectionID = strings.TrimSpace(out.ConnectionID)
	out.Label = strings.TrimSpace(out.Label)

	if err := ValidateProviderID(out.ID); err != nil {
		return nil, err
	}
	if out.ConnectionID == "" {
		return nil, fmt.Errorf("provider connection id cannot be empty")
	}
	if out.Label == "" {
		out.Label = out.ID
	}
	if len(out.Label) > 256 {
		return nil, fmt.Errorf("provider label too long")
	}
	if !out.State.Valid() {
		out.State = service.ProviderStateActive
	}
	if out.ConnectedAt.IsZero() {
		out.ConnectedAt = now
	}
	if out.LastSeenAt.IsZero() {
		out.LastSeenAt = now
	}
	if out.State == service.ProviderStateCooling {
		if out.CooldownUntil.IsZero() || !out.CooldownUntil.After(now) {
			out.State = service.ProviderStateActive
			out.CooldownUntil = time.Time{}
		}
	}
	if out.Metadata.ProviderType == "" {
		out.Metadata.ProviderType = "aistudio"
	}
	if out.Metadata.Attributes == nil {
		out.Metadata.Attributes = map[string]string{}
	}
	return out, nil
}

func providerMapKey(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func sortProviders(items []*service.RuntimeProvider) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left == nil && right == nil {
			return false
		}
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return strings.ToLower(left.ID) < strings.ToLower(right.ID)
	})
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

func sameTime(a, b time.Time) bool {
	return a.Equal(b)
}

func providerIDFromEvent(event service.ProviderEvent) string {
	if event.Provider == nil {
		return ""
	}
	return event.Provider.ID
}

func providerIDOf(provider *service.RuntimeProvider) string {
	if provider == nil {
		return ""
	}
	return provider.ID
}

func ensureContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}