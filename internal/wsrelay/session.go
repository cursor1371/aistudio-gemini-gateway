package wsrelay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"aistudio-gemini-gateway/service"
	"github.com/gorilla/websocket"
)

// pendingRequest 表示一个等待响应的请求槽位。
type pendingRequest struct {
	ch        chan Message
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

// trySend 尝试非阻塞发送消息。
func (p *pendingRequest) trySend(msg Message) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}

	select {
	case p.ch <- msg:
	default:
	}
	return true
}

// close 关闭响应通道。
func (p *pendingRequest) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.ch)
		p.mu.Unlock()
	})
}

// session 表示一个 Provider 的单条 WS 连接。
type session struct {
	conn    *websocket.Conn
	manager *Manager

	metaMu   sync.RWMutex
	provider *service.RuntimeProvider

	closed    chan struct{}
	closeOnce sync.Once

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]*pendingRequest
}

// newSession 创建 session 并初始化 WS 连接参数与心跳。
func newSession(conn *websocket.Conn, mgr *Manager, provider *service.RuntimeProvider) *session {
	s := &session{
		conn:     conn,
		manager:  mgr,
		provider: provider.Clone(),
		closed:   make(chan struct{}),
		pending:  make(map[string]*pendingRequest),
	}

	conn.SetReadLimit(mgr.maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(mgr.readTimeout))
	conn.SetPongHandler(func(appData string) error {
		s.touch(time.Now())
		return conn.SetReadDeadline(time.Now().Add(mgr.pongTimeout))
	})

	s.startHeartbeat()
	return s
}

// run 持续读取 Provider 返回的消息并分发。
func (s *session) run() {
	defer s.cleanup(errSessionClosed)

	for {
		var msg Message
		if err := s.conn.ReadJSON(&msg); err != nil {
			s.cleanup(err)
			return
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(s.manager.readTimeout))

		s.touch(time.Now())
		s.dispatch(msg)
	}
}

// dispatch 根据消息类型分发到对应的 pending request 或直接回复。
func (s *session) dispatch(msg Message) {
	// 应用层 ping/pong 直接处理。
	switch msg.Type {
	case MessageTypePing:
		_ = s.send(context.Background(), Message{
			ID:   msg.ID,
			Type: MessageTypePong,
		})
		return
	case MessageTypePong:
		return
	}

	var (
		req      *pendingRequest
		terminal bool
	)

	s.pendingMu.Lock()
	req = s.pending[msg.ID]
	terminal = msg.Type.Terminal()
	if req != nil && terminal {
		delete(s.pending, msg.ID)
	}
	s.pendingMu.Unlock()

	if req != nil {
		req.trySend(msg)
		if terminal {
			req.close()
		}
		return
	}

	// 终止消息但无对应请求，记录调试日志。
	if msg.Type.Terminal() {
		provider := s.providerSnapshot()
		s.manager.logger.DebugContext(context.Background(), "wsrelay 收到未知消息 ID 的终止消息",
			"provider_id", provider.ID,
			"message_id", msg.ID,
			"type", string(msg.Type),
		)
	}
}

// startHeartbeat 启动心跳发送协程。
func (s *session) startHeartbeat() {
	if s == nil || s.conn == nil || s.manager == nil {
		return
	}
	ticker := time.NewTicker(s.manager.pingInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-s.closed:
				return
			case <-ticker.C:
				s.writeMu.Lock()
				err := s.conn.WriteControl(
					websocket.PingMessage,
					[]byte("ping"),
					time.Now().Add(s.manager.writeTimeout),
				)
				s.writeMu.Unlock()
				if err != nil {
					s.cleanup(err)
					return
				}
			}
		}
	}()
}

// send 向 Provider 发送单条 WS 消息。
func (s *session) send(ctx context.Context, msg Message) error {
	if s == nil {
		return fmt.Errorf("wsrelay session is nil")
	}
	if msg.ID == "" {
		return fmt.Errorf("wsrelay message id is required")
	}

	select {
	case <-s.closed:
		return errSessionClosed
	default:
	}

	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.conn.SetWriteDeadline(time.Now().Add(s.manager.writeTimeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if err := s.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// request 发送请求消息并返回响应通道。
// 调用方通过读取返回的 channel 获取响应消息序列。
func (s *session) request(ctx context.Context, msg Message) (<-chan Message, error) {
	if s == nil {
		return nil, fmt.Errorf("wsrelay session is nil")
	}
	if msg.ID == "" {
		return nil, fmt.Errorf("wsrelay message id is required")
	}

	req := &pendingRequest{ch: make(chan Message, 16)}

	s.pendingMu.Lock()
	if _, exists := s.pending[msg.ID]; exists {
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("wsrelay duplicate message id: %s", msg.ID)
	}
	s.pending[msg.ID] = req
	s.pendingMu.Unlock()

	if err := s.send(ctx, msg); err != nil {
		s.pendingMu.Lock()
		if actual, exists := s.pending[msg.ID]; exists && actual == req {
			delete(s.pending, msg.ID)
		}
		s.pendingMu.Unlock()

		req.close()
		return nil, err
	}

	// 监听 ctx 取消，及时释放 pending 槽位。
	go func() {
		select {
		case <-s.closed:
			return
		case <-ctx.Done():
			shouldClose := false

			s.pendingMu.Lock()
			if actual, exists := s.pending[msg.ID]; exists && actual == req {
				delete(s.pending, msg.ID)
				shouldClose = true
			}
			s.pendingMu.Unlock()

			if shouldClose {
				req.close()
			}
		}
	}()
	return req.ch, nil
}

// cleanup 执行 session 清理。
// 该方法幂等：关闭通道、通知所有 pending 请求、关闭底层连接、通知 Manager。
func (s *session) cleanup(cause error) {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.closed)

		if cause == nil {
			cause = errSessionClosed
		}

		// 收割所有 pending 请求并发送错误通知。
		s.pendingMu.Lock()
		pending := make(map[string]*pendingRequest, len(s.pending))
		for key, req := range s.pending {
			pending[key] = req
		}
		s.pending = make(map[string]*pendingRequest)
		s.pendingMu.Unlock()

		for msgID, req := range pending {
			if req != nil {
				req.trySend(Message{
					ID:   msgID,
					Type: MessageTypeError,
					Payload: map[string]any{
						"error": cause.Error(),
					},
				})
				req.close()
			}
		}

		_ = s.conn.Close()

		if s.manager != nil {
			s.manager.handleSessionClosed(s, cause)
		}
	})
}

// touch 更新 Provider 的最后活跃时间，并通过 Manager 回调同步到 Registry。
func (s *session) touch(at time.Time) {
	if s == nil {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	var snapshot *service.RuntimeProvider

	s.metaMu.Lock()
	if s.provider != nil {
		s.provider.LastSeenAt = at
		snapshot = s.provider.Clone()
	}
	s.metaMu.Unlock()

	if snapshot != nil && s.manager != nil {
		s.manager.handleSessionTouched(snapshot)
	}
}

// providerSnapshot 返回当前 Provider 快照。
func (s *session) providerSnapshot() *service.RuntimeProvider {
	if s == nil {
		return nil
	}
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	if s.provider == nil {
		return nil
	}
	return s.provider.Clone()
}

// PendingCount 返回当前 session 中未完成的请求数量。
// 该信息仅用于状态总览接口，帮助诊断"请求卡住"问题。
func (s *session) PendingCount() int {
	if s == nil {
		return 0
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return len(s.pending)
}