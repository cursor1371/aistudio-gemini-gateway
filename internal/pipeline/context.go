package pipeline

import (
	"time"

	"aistudio-gemini-gateway/internal/execution"
	selectorpkg "aistudio-gemini-gateway/internal/provider/selector"
	"aistudio-gemini-gateway/service"
)

// RequestContext 表示一次 pipeline 请求在内部流转时的业务上下文。
// 它不是 context.Context 的替代，而是"业务执行现场"的快照。
//
// 设计要点：
//   - 不长期挂载完整原始请求或原始请求体，避免大对象常驻堆。
//   - PreparedPayload 作为单工作缓冲区的最终结果向下传递给执行器。
type RequestContext struct {
	// RequestID 是本次请求的唯一标识。
	RequestID string

	// RequestedModel 是客户端原始传入的模型名（可能含 models/ 前缀或 thinking suffix）。
	RequestedModel string
	// NormalizedRequestedModel 是经过 models/ 前缀去除和 suffix 剥离后的模型名。
	NormalizedRequestedModel string
	// ResolvedModel 是经过 alias 解析后的最终 canonical 模型名。
	ResolvedModel string

	// Action 是本次请求对应的 Gemini 操作（generateContent / streamGenerateContent / countTokens）。
	Action service.Action
	// Alt 是 SSE 或其他输出格式指示（对应 ?alt= 参数）。
	Alt string
	// Streaming 表示本次请求是否为流式请求。
	Streaming bool
	// SessionID 是从请求中提取或生成的会话标识，用于 session affinity 选路。
	SessionID string

	// RequestedAt 是请求到达网关的时间。
	RequestedAt time.Time

	// ModelInfo 是从静态模型注册表中解析到的模型元信息快照。
	ModelInfo *service.ModelInfo
	// Provider 是被选中的执行 Provider 快照。
	Provider *service.RuntimeProvider
	// PreparedPayload 是经过标准化链处理后的最终请求体。
	PreparedPayload []byte

	// Attempt 是当前重试序号（从 1 开始计数）。
	Attempt int
}

// Clone 深拷贝 RequestContext。
// 该方法主要用于测试或调试场景；热路径不建议频繁使用。
func (c *RequestContext) Clone() *RequestContext {
	if c == nil {
		return nil
	}
	out := *c
	out.ModelInfo = c.ModelInfo.Clone()
	out.Provider = c.Provider.Clone()
	out.PreparedPayload = cloneBytes(c.PreparedPayload)
	return &out
}

// SelectorRequest 构造给 selector 使用的轻量选路请求。
// 使用 resolved model 作为模型标识，确保：
//   - alias 不影响选路键
//   - thinking suffix 不影响 session affinity 绑定键
func (c *RequestContext) SelectorRequest() selectorpkg.Request {
	if c == nil {
		return selectorpkg.Request{}
	}
	return selectorpkg.Request{
		RequestID: c.RequestID,
		Model:     firstNonEmpty(c.ResolvedModel, c.NormalizedRequestedModel, c.RequestedModel),
		SessionID: c.SessionID,
	}
}

// PreparedRequest 构造 Provider 执行器需要的最终执行请求。
// 该方法不复制 PreparedPayload，直接把工作缓冲区的最终结果向下传递。
func (c *RequestContext) PreparedRequest() execution.PreparedRequest {
	if c == nil {
		return execution.PreparedRequest{}
	}
	return execution.PreparedRequest{
		RequestID:      c.RequestID,
		RequestedModel: c.RequestedModel,
		ResolvedModel:  c.ResolvedModel,
		Action:         c.Action,
		Alt:            c.Alt,
		Payload:        c.PreparedPayload,
	}
}

// ResponseMetadata 返回建议附带到最终响应中的统一元数据。
func (c *RequestContext) ResponseMetadata() map[string]any {
	if c == nil {
		return nil
	}

	out := map[string]any{
		"requested_model":            c.RequestedModel,
		"normalized_requested_model": c.NormalizedRequestedModel,
		"resolved_model":             c.ResolvedModel,
		"action":                     c.Action.String(),
		"streaming":                  c.Streaming,
		"attempt":                    c.Attempt,
	}
	if c.SessionID != "" {
		out["session_id"] = c.SessionID
	}
	if c.Provider != nil {
		out["provider_id"] = c.Provider.ID
		out["provider_label"] = c.Provider.Label
	}
	return out
}
