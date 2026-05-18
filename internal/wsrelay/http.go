package wsrelay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"aistudio-gemini-gateway/internal/common"
)

// RelayError 表示 Provider 返回的中继错误。
// Pipeline 层会通过 errors.As 匹配该类型，并根据 Status 做 HTTP 状态码分类。
type RelayError struct {
	Message string
	Status  int
}

func (e *RelayError) Error() string {
	if e == nil {
		return ""
	}
	if e.Status > 0 {
		return fmt.Sprintf("%s (status=%d)", e.Message, e.Status)
	}
	return e.Message
}

// NonStream 通过指定 Provider 执行一次非流式 HTTP 风格请求。
// 即便 Provider 返回的是 stream_start/chunk/end 序列，本方法也会自动拼接成最终 body。
func (m *Manager) NonStream(ctx context.Context, providerID string, req *HTTPRequest) (*HTTPResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("wsrelay manager is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("wsrelay request is nil")
	}

	msg := Message{
		ID:      common.GenerateMessageID(),
		Type:    MessageTypeHTTPReq,
		Payload: encodeRequest(req),
	}

	respCh, err := m.Send(ctx, providerID, msg)
	if err != nil {
		return nil, err
	}

	var (
		streamMode bool
		streamResp *HTTPResponse
		bodyBuf    bytes.Buffer
	)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case msg, ok := <-respCh:
			if !ok {
				// 通道异常关闭（未收到终止消息），视为错误。
				return nil, errors.New("wsrelay response channel closed unexpectedly")
			}

			switch msg.Type {
			case MessageTypeHTTPResp:
				resp := decodeResponse(msg.Payload)
				if streamMode && bodyBuf.Len() > 0 && len(resp.Body) == 0 {
					resp.Body = append(resp.Body[:0], bodyBuf.Bytes()...)
				}
				return resp, nil

			case MessageTypeError:
				return nil, decodeError(msg.Payload)

			case MessageTypeStreamStart:
				streamMode = true
				streamResp = decodeResponse(msg.Payload)
				if streamResp.Headers == nil {
					streamResp.Headers = make(http.Header)
				}
				bodyBuf.Reset()

			case MessageTypeStreamChunk:
				if !streamMode {
					streamMode = true
					streamResp = &HTTPResponse{
						Status:  http.StatusOK,
						Headers: make(http.Header),
					}
				}
				chunk := decodeChunk(msg.Payload)
				if len(chunk) > 0 {
					bodyBuf.Write(chunk)
				}

			case MessageTypeStreamEnd:
				if !streamMode {
					return &HTTPResponse{
						Status:  http.StatusOK,
						Headers: make(http.Header),
					}, nil
				}
				if streamResp == nil {
					streamResp = &HTTPResponse{
						Status:  http.StatusOK,
						Headers: make(http.Header),
					}
				}
				streamResp.Body = append(streamResp.Body[:0], bodyBuf.Bytes()...)
				return streamResp, nil
			}
		}
	}
}

// Stream 通过指定 Provider 执行流式 HTTP 风格请求。
// 若 Provider 返回的是一次性 http_response，本方法也会兼容转发。
func (m *Manager) Stream(ctx context.Context, providerID string, req *HTTPRequest) (<-chan StreamEvent, error) {
	if m == nil {
		return nil, fmt.Errorf("wsrelay manager is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("wsrelay request is nil")
	}

	msg := Message{
		ID:      common.GenerateMessageID(),
		Type:    MessageTypeHTTPReq,
		Payload: encodeRequest(req),
	}

	respCh, err := m.Send(ctx, providerID, msg)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamEvent)

	go func() {
		defer close(out)

		send := func(ev StreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- ev:
				return true
			}
		}

		for {
			select {
			case <-ctx.Done():
				return

			case msg, ok := <-respCh:
				if !ok {
					_ = send(StreamEvent{
						Type: MessageTypeError,
						Err:  errors.New("wsrelay stream closed"),
					})
					return
				}

				switch msg.Type {
				case MessageTypeStreamStart:
					resp := decodeResponse(msg.Payload)
					if !send(StreamEvent{
						Type:    MessageTypeStreamStart,
						Status:  resp.Status,
						Headers: resp.Headers,
					}) {
						return
					}

				case MessageTypeStreamChunk:
					if !send(StreamEvent{
						Type:    MessageTypeStreamChunk,
						Payload: decodeChunk(msg.Payload),
					}) {
						return
					}

				case MessageTypeStreamEnd:
					_ = send(StreamEvent{Type: MessageTypeStreamEnd})
					return

				case MessageTypeError:
					_ = send(StreamEvent{
						Type: MessageTypeError,
						Err:  decodeError(msg.Payload),
					})
					return

				case MessageTypeHTTPResp:
					resp := decodeResponse(msg.Payload)
					_ = send(StreamEvent{
						Type:    MessageTypeHTTPResp,
						Status:  resp.Status,
						Headers: resp.Headers,
						Payload: resp.Body,
					})
					return
				}
			}
		}
	}()

	return out, nil
}

// encodeRequest 将 HTTPRequest 编码为 WS 消息 payload。
func encodeRequest(req *HTTPRequest) map[string]any {
	headers := make(map[string]any, len(req.Headers))
	for key, values := range req.Headers {
		copied := make([]string, len(values))
		copy(copied, values)
		headers[key] = copied
	}

	return map[string]any{
		"method":  req.Method,
		"url":     req.URL,
		"headers": headers,
		"body":    string(req.Body),
		"sent_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// decodeResponse 从 WS 消息 payload 中解码 HTTP 响应。
func decodeResponse(payload map[string]any) *HTTPResponse {
	resp := &HTTPResponse{
		Status:  http.StatusOK,
		Headers: make(http.Header),
	}
	if payload == nil {
		return resp
	}

	if status, ok := numericToInt(payload["status"]); ok && status > 0 {
		resp.Status = status
	}

	if rawHeaders, ok := payload["headers"].(map[string]any); ok {
		for key, raw := range rawHeaders {
			switch typed := raw.(type) {
			case string:
				resp.Headers.Set(key, typed)
			case []string:
				for _, item := range typed {
					resp.Headers.Add(key, item)
				}
			case []any:
				for _, item := range typed {
					if s, ok := item.(string); ok {
						resp.Headers.Add(key, s)
					}
				}
			}
		}
	}

	if body, ok := payload["body"].(string); ok {
		resp.Body = []byte(body)
	}
	return resp
}

// decodeChunk 从 WS 消息 payload 中提取流式数据块。
func decodeChunk(payload map[string]any) []byte {
	if payload == nil {
		return nil
	}
	if data, ok := payload["data"].(string); ok {
		return []byte(data)
	}
	if body, ok := payload["body"].(string); ok {
		return []byte(body)
	}
	return nil
}

// decodeError 从 WS 消息 payload 中解码错误信息。
func decodeError(payload map[string]any) error {
	if payload == nil {
		return &RelayError{
			Message: "wsrelay upstream error",
		}
	}

	message := "wsrelay upstream error"
	if s, ok := payload["error"].(string); ok && s != "" {
		message = s
	}
	status, _ := numericToInt(payload["status"])

	return &RelayError{
		Message: message,
		Status:  status,
	}
}

// numericToInt 将动态类型数值安全转换为 int。
func numericToInt(v any) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
