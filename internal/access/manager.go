package access

import (
	"context"
	"net/http"
	"sync"
)

// Manager 负责协调多个鉴权提供者，依次调用直到某个提供者认证成功。
type Manager struct {
	mu        sync.RWMutex
	providers []Provider
}

// NewManager 创建空的鉴权管理器。
func NewManager() *Manager {
	return &Manager{}
}

// SetProviders 替换当前提供者列表。
// 传入 nil 或空切片表示关闭鉴权。
func (m *Manager) SetProviders(providers []Provider) {
	if m == nil {
		return
	}
	cloned := make([]Provider, len(providers))
	copy(cloned, providers)

	m.mu.Lock()
	m.providers = cloned
	m.mu.Unlock()
}

// Providers 返回当前提供者快照。
func (m *Manager) Providers() []Provider {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Provider, len(m.providers))
	copy(out, m.providers)
	return out
}

// Authenticate 依次调用提供者，直到某个提供者认证成功。
// 处理逻辑：
// 1. 无提供者时直接放行（返回 nil, nil）
// 2. 提供者返回 NotHandled 时跳过，继续下一个
// 3. 提供者返回 NoCredentials 时记录，继续下一个
// 4. 提供者返回 InvalidCredential 时记录，继续下一个
// 5. 提供者返回其他错误时立即返回
// 6. 全部遍历后按优先级返回最合适的错误
func (m *Manager) Authenticate(ctx context.Context, r *http.Request) (*Result, *AuthError) {
	if m == nil {
		return nil, nil
	}
	providers := m.Providers()
	if len(providers) == 0 {
		return nil, nil
	}

	var (
		missing bool
		invalid bool
	)

	for _, provider := range providers {
		if provider == nil {
			continue
		}
		result, authErr := provider.Authenticate(ctx, r)
		if authErr == nil {
			return result, nil
		}
		if IsAuthErrorCode(authErr, AuthErrorCodeNotHandled) {
			continue
		}
		if IsAuthErrorCode(authErr, AuthErrorCodeNoCredentials) {
			missing = true
			continue
		}
		if IsAuthErrorCode(authErr, AuthErrorCodeInvalidCredential) {
			invalid = true
			continue
		}
		// 内部错误等其他类型，立即返回。
		return nil, authErr
	}

	// 优先返回"无效凭据"，其次"缺少凭据"。
	if invalid {
		return nil, NewInvalidCredentialError()
	}
	if missing {
		return nil, NewNoCredentialsError()
	}
	return nil, NewNoCredentialsError()
}
