package session

import (
	"strings"
	"sync"
	"time"
)

// entry 表示一个 session 绑定项。
type entry struct {
	providerID string
	expiresAt  time.Time
}

// Cache 是 Session Affinity 使用的 TTL 缓存。
// 绑定键建议采用 model + sessionID 的组合。
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewCache 创建 session 绑定缓存。
// 若 ttl <= 0，则默认使用 1 小时。
func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	c := &Cache{
		entries: make(map[string]entry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// TTL 返回缓存 TTL。
func (c *Cache) TTL() time.Duration {
	if c == nil {
		return 0
	}
	return c.ttl
}

// Get 获取绑定，但不刷新 TTL。
func (c *Cache) Get(key string) (string, bool) {
	key = normalizeKey(key)
	if c == nil || key == "" {
		return "", false
	}

	c.mu.RLock()
	item, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}

	if time.Now().After(item.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return "", false
	}
	return item.providerID, true
}

// GetAndRefresh 获取绑定，并刷新 TTL。
func (c *Cache) GetAndRefresh(key string) (string, bool) {
	key = normalizeKey(key)
	if c == nil || key == "" {
		return "", false
	}

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	item, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if now.After(item.expiresAt) {
		delete(c.entries, key)
		return "", false
	}

	item.expiresAt = now.Add(c.ttl)
	c.entries[key] = item
	return item.providerID, true
}

// Set 设置 session 绑定。
func (c *Cache) Set(key, providerID string) {
	key = normalizeKey(key)
	providerID = strings.TrimSpace(providerID)
	if c == nil || key == "" || providerID == "" {
		return
	}

	c.mu.Lock()
	c.entries[key] = entry{
		providerID: providerID,
		expiresAt:  time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// Invalidate 删除指定绑定键。
func (c *Cache) Invalidate(key string) {
	key = normalizeKey(key)
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// InvalidateProvider 删除所有绑定到指定 Provider 的 session。
func (c *Cache) InvalidateProvider(providerID string) {
	providerID = strings.TrimSpace(providerID)
	if c == nil || providerID == "" {
		return
	}

	c.mu.Lock()
	for key, item := range c.entries {
		if strings.EqualFold(item.providerID, providerID) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

// Len 返回当前缓存项数量。
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Stop 停止后台清理协程。
func (c *Cache) Stop() {
	if c == nil {
		return
	}
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *Cache) cleanupLoop() {
	interval := c.ttl / 2
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

func (c *Cache) cleanup() {
	now := time.Now()
	c.mu.Lock()
	for key, item := range c.entries {
		if now.After(item.expiresAt) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

// BuildBindingKey 构造 Session Affinity 绑定键。
// 绑定维度固定为：模型 + sessionID。
func BuildBindingKey(model, sessionID string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	if model == "" {
		model = "*"
	}
	return model + "::" + sessionID
}

func normalizeKey(key string) string {
	return strings.TrimSpace(key)
}
