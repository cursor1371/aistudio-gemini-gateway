package execution

import (
	"net/http"
	"time"

	"aistudio-gemini-gateway/internal/wsrelay"
	"aistudio-gemini-gateway/service"
)

// PreparedRequest 表示已完成全部标准化处理的最终请求。
// 在进入 Provider 执行器之前，请求体已经过以下处理：
//   - Gemini 请求规范化
//   - thinking 配置处理
//   - 网关内部字段剥离
//   - 图片预览兼容修复
//
// 该结构只保留执行所需的最小字段，不携带业务元数据，
// 也不提供深拷贝入口——调用方应保证所有权语义清晰。
type PreparedRequest struct {
	// RequestID 是本次请求的唯一标识。
	RequestID string

	// RequestedModel 是客户端原始请求中的模型名。
	RequestedModel string

	// ResolvedModel 是经过 alias / normalize 后的最终模型名。
	ResolvedModel string

	// Action 是本次请求的动作类型。
	Action service.Action

	// Alt 是流式输出格式控制参数（如 sse）。
	Alt string

	// Payload 是已完成标准化的最终请求体。
	// 该切片在整个执行链中视为"所有权转移"，调用方不应再修改。
	Payload []byte
}

// UpstreamResponse 表示 Provider 执行器返回的一次性（非流式）响应。
type UpstreamResponse struct {
	// ProviderID 是执行本次请求的 Provider 标识。
	ProviderID string

	// Action 是本次请求对应的动作类型。
	Action service.Action

	// URL 是实际发往上游的请求地址。
	URL string

	// StatusCode 是上游返回的 HTTP 状态码。
	StatusCode int

	// Headers 是上游返回的 HTTP 响应头。
	Headers http.Header

	// Payload 是上游返回的响应体。
	Payload []byte

	// ReceivedAt 是收到上游响应的时间。
	ReceivedAt time.Time
}

// Clone 深拷贝 UpstreamResponse。
func (r *UpstreamResponse) Clone() *UpstreamResponse {
	if r == nil {
		return nil
	}
	out := *r
	out.Headers = cloneHeader(r.Headers)
	out.Payload = cloneBytes(r.Payload)
	return &out
}

// StreamResponse 表示 Provider 执行器建立成功的流式响应句柄。
// 调用方应持续消费 Events 通道直到关闭或遇到终止事件。
type StreamResponse struct {
	// ProviderID 是执行本次请求的 Provider 标识。
	ProviderID string

	// Action 是本次请求对应的动作类型。
	Action service.Action

	// URL 是实际发往上游的请求地址。
	URL string

	// StartedAt 是流式连接建立的时间。
	StartedAt time.Time

	// Events 是流式事件通道。
	// 通道关闭表示流式传输结束。
	Events <-chan wsrelay.StreamEvent
}

// cloneBytes 深拷贝字节切片。
func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// cloneHeader 深拷贝 HTTP 响应头。
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
