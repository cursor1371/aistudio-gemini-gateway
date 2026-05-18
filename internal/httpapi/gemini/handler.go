package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aistudio-gemini-gateway/internal/common"
	httpmiddleware "aistudio-gemini-gateway/internal/httpapi/middleware"
	"aistudio-gemini-gateway/internal/observability"
	core "aistudio-gemini-gateway/service"
)

// Request 是 HTTP API 层传递给后端的统一请求结构。
type Request struct {
	RequestID string
	Model     string
	Action    string
	Headers   http.Header
	Query     url.Values
	Payload   []byte
	Metadata  map[string]any
	SessionID string
	Alt       string
}

// Response 是 HTTP API 层使用的非流式响应。
type Response struct {
	RequestID  string
	StatusCode int
	Headers    http.Header
	Payload    []byte
	Metadata   map[string]any
}

// StreamChunk 是 HTTP API 层使用的流式响应片段。
type StreamChunk struct {
	Payload  []byte
	Metadata map[string]any
	Err      error
}

// StreamResult 是 HTTP API 层使用的流式结果句柄。
type StreamResult struct {
	RequestID  string
	StatusCode int
	Headers    http.Header
	Chunks     <-chan StreamChunk
}

// Backend 是 Gemini HTTP 处理层依赖的抽象后端接口。
type Backend interface {
	ListModels(ctx context.Context) ([]map[string]any, error)
	GetModel(ctx context.Context, model string) (map[string]any, error)
	Generate(ctx context.Context, req *Request) (*Response, error)
	Stream(ctx context.Context, req *Request) (*StreamResult, error)
	CountTokens(ctx context.Context, req *Request) (*Response, error)
}

// Handler 是 Gemini HTTP API 路由处理器。
// 负责处理 /v1beta/models 下所有 Gemini 标准接口路由。
type Handler struct {
	backend      Backend
	logger       observability.Logger
	maxBodyBytes int64
}

// NewHandler 创建 Gemini Handler。
func NewHandler(backend Backend, logger observability.Logger, maxBodyBytes int64) *Handler {
	if logger == nil {
		logger = observability.NoopLogger{}
	}
	if maxBodyBytes <= 0 {
		maxBodyBytes = 32 << 20 // 32 MiB
	}
	return &Handler{
		backend:      backend,
		logger:       logger,
		maxBodyBytes: maxBodyBytes,
	}
}

// ServeHTTP 统一路由分发 /v1beta/models 下的 Gemini 请求。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.backend == nil {
		httpmiddleware.WriteError(w, errors.New("gemini backend is not configured"))
		return
	}

	path := strings.TrimSpace(r.URL.Path)
	base := "/v1beta/models"

	switch {
	// GET /v1beta/models
	case path == base || path == base+"/":
		if !strings.EqualFold(r.Method, http.MethodGet) {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.handleListModels(w, r)
		return

	// /v1beta/models/{model} 或 /v1beta/models/{model}:{action}
	case strings.HasPrefix(path, base+"/"):
		rest := strings.TrimPrefix(path, base+"/")
		rest = strings.TrimPrefix(rest, "/")
		if rest == "" {
			if !strings.EqualFold(r.Method, http.MethodGet) {
				writeMethodNotAllowed(w, http.MethodGet)
				return
			}
			h.handleListModels(w, r)
			return
		}

		// 路径中带 : 表示 action 调用
		if strings.Contains(rest, ":") {
			model, action, ok := splitModelAction(rest)
			if !ok {
				http.NotFound(w, r)
				return
			}
			switch action {
			case "generateContent":
				if !strings.EqualFold(r.Method, http.MethodPost) {
					writeMethodNotAllowed(w, http.MethodPost)
					return
				}
				h.handleGenerateContent(w, r, model)
				return

			case "streamGenerateContent":
				if !strings.EqualFold(r.Method, http.MethodPost) {
					writeMethodNotAllowed(w, http.MethodPost)
					return
				}
				h.handleStreamGenerateContent(w, r, model)
				return

			case "countTokens":
				if !strings.EqualFold(r.Method, http.MethodPost) {
					writeMethodNotAllowed(w, http.MethodPost)
					return
				}
				h.handleCountTokens(w, r, model)
				return

			default:
				http.NotFound(w, r)
				return
			}
		}

		// 不带 : 的路径视为 GET /v1beta/models/{model}
		if !strings.EqualFold(r.Method, http.MethodGet) {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.handleGetModel(w, r, rest)
		return

	default:
		http.NotFound(w, r)
		return
	}
}

// handleListModels 处理模型列表请求。
func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.backend.ListModels(r.Context())
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleGetModel 处理单个模型查询请求。
func (h *Handler) handleGetModel(w http.ResponseWriter, r *http.Request, model string) {
	item, err := h.backend.GetModel(r.Context(), normalizePathModel(model))
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleGenerateContent 处理非流式内容生成请求。
func (h *Handler) handleGenerateContent(w http.ResponseWriter, r *http.Request, model string) {
	req, err := h.buildRequest(r, normalizePathModel(model), "generateContent")
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}

	resp, err := h.backend.Generate(r.Context(), req)
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	writeResponse(w, resp)
}

// handleCountTokens 处理 Token 计数请求。
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	req, err := h.buildRequest(r, normalizePathModel(model), "countTokens")
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}

	resp, err := h.backend.CountTokens(r.Context(), req)
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	writeResponse(w, resp)
}

// handleStreamGenerateContent 处理流式内容生成请求。
func (h *Handler) handleStreamGenerateContent(w http.ResponseWriter, r *http.Request, model string) {
	req, err := h.buildRequest(r, normalizePathModel(model), "streamGenerateContent")
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}

	result, err := h.backend.Stream(r.Context(), req)
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	if result == nil {
		httpmiddleware.WriteError(w, errors.New("stream backend returned nil result"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpmiddleware.WriteError(w, errors.New("streaming is not supported by current response writer"))
		return
	}

	useSSE := shouldUseSSE(req.Alt)

	// 设置响应头。
	if useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	} else {
		if ct := strings.TrimSpace(result.Headers.Get("Content-Type")); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
	}

	copyResponseHeaders(w.Header(), result.Headers)

	statusCode := result.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	flusher.Flush()

	// 消费流式事件。
	for {
		select {
		case <-r.Context().Done():
			return

		case chunk, ok := <-result.Chunks:
			if !ok {
				return
			}

			if chunk.Err != nil {
				if useSSE {
					writeSSEError(w, chunk.Err)
					flusher.Flush()
				}
				return
			}

			if len(chunk.Payload) == 0 {
				continue
			}

			if useSSE {
				writeSSEPayload(w, chunk.Payload)
			} else {
				_, _ = w.Write(chunk.Payload)
			}
			flusher.Flush()
		}
	}
}

// buildRequest 从 HTTP 请求中构造后端请求对象。
// 注意：请求体读取后不再额外复制，直接作为当前请求的所有权移交。
// Header 和 Query 仍然拷贝，避免共享可变对象。
func (h *Handler) buildRequest(r *http.Request, model, action string) (*Request, error) {
	if r == nil {
		return nil, errors.New("request is nil")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) && maxBytesErr != nil {
			return nil, core.NewRequestError(
				"请求体过大",
				err,
				core.WithStatusCode(http.StatusRequestEntityTooLarge),
				core.WithPublicMessage("请求体过大"),
				core.WithModel(model),
			)
		}
		return nil, err
	}

	requestID := common.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = common.GenerateRequestID()
	}

	queryValues := r.URL.Query()

	// 仅在鉴权结果存在时才分配 metadata，减少不必要的小对象分配。
	var metadata map[string]any
	if authResult, ok := httpmiddleware.AccessResultFromContext(r.Context()); ok && authResult != nil {
		metadata = map[string]any{
			"principal":       authResult.Principal,
			"source":          authResult.CredentialSource,
			"access_provider": authResult.Provider,
			"credential_name": authResult.CredentialName,
		}
	}

	// 提取 session ID：优先 Header -> Query。
	sessionID := strings.TrimSpace(r.Header.Get("X-Session-ID"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(queryValues.Get("session_id"))
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(queryValues.Get("conversation_id"))
	}

	return &Request{
		RequestID: requestID,
		Model:     model,
		Action:    action,
		Headers:   r.Header,
		Query:     queryValues,
		Payload:   body,
		Metadata:  metadata,
		SessionID: sessionID,
		Alt:       extractAlt(queryValues),
	}, nil
}

// writeResponse 把非流式后端响应写入 HTTP。
func writeResponse(w http.ResponseWriter, resp *Response) {
	if resp == nil {
		httpmiddleware.WriteError(w, errors.New("empty response"))
		return
	}

	copyResponseHeaders(w.Header(), resp.Headers)

	statusCode := resp.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}

	contentType := strings.TrimSpace(resp.Headers.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)

	w.WriteHeader(statusCode)
	if len(resp.Payload) > 0 {
		_, _ = w.Write(resp.Payload)
	}
}

// writeJSON 写入标准 JSON 响应。
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		httpmiddleware.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}

// writeMethodNotAllowed 返回 405 Method Not Allowed。
func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	if strings.TrimSpace(allow) != "" {
		w.Header().Set("Allow", allow)
	}
	writeStatusError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// writeStatusError 返回 Gemini 风格的错误 JSON。
func writeStatusError(w http.ResponseWriter, statusCode int, message string) {
	body := map[string]any{
		"error": map[string]any{
			"code":    statusCode,
			"message": message,
			"status":  geminiStatus(statusCode),
		},
	}
	writeJSON(w, statusCode, body)
}

// writeSSEError 在 SSE 流中写出一个错误事件。
func writeSSEError(w http.ResponseWriter, err error) {
	body := buildErrorBody(err)
	_, _ = w.Write([]byte("event: error\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n\n"))
}

// writeSSEPayload 将 payload 写入 SSE 流。
// 若上游 chunk 已经是完整 SSE 帧，则原样透传，避免形成 data: data: {...} 的双重包裹。
// 若上游 chunk 是纯 JSON payload，则包装为标准 SSE data 帧。
func writeSSEPayload(w http.ResponseWriter, payload []byte) {
	if len(payload) == 0 {
		return
	}

	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return
	}

	// 上游已经是 SSE 帧格式，直接透传。
	if looksLikeSSEWirePayload(trimmed) {
		_, _ = w.Write(payload)

		// 保证事件之间有空行分隔。
		if bytes.HasSuffix(payload, []byte("\n\n")) || bytes.HasSuffix(payload, []byte("\r\n\r\n")) {
			return
		}
		switch {
		case bytes.HasSuffix(payload, []byte("\r\n")):
			_, _ = w.Write([]byte("\r\n"))
		case bytes.HasSuffix(payload, []byte("\n")):
			_, _ = w.Write([]byte("\n"))
		default:
			_, _ = w.Write([]byte("\n\n"))
		}
		return
	}

	// 纯 JSON payload 包装为标准 SSE 帧。
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n\n"))
}

// buildErrorBody 构造 Gemini 风格的 JSON 错误体。
func buildErrorBody(err error) []byte {
	type httpStatusProvider interface {
		HTTPStatus() int
		SafeMessage() string
	}

	statusCode := http.StatusInternalServerError
	message := "internal server error"

	var provider httpStatusProvider
	if errors.As(err, &provider) && provider != nil {
		if code := provider.HTTPStatus(); code > 0 {
			statusCode = code
		}
		if msg := strings.TrimSpace(provider.SafeMessage()); msg != "" {
			message = msg
		}
	} else if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}

	body := map[string]any{
		"error": map[string]any{
			"code":    statusCode,
			"message": message,
			"status":  geminiStatus(statusCode),
		},
	}

	data, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return []byte(`{"error":{"code":500,"message":"internal server error","status":"INTERNAL"}}`)
	}
	return data
}

// geminiStatus 将 HTTP 状态码映射为 Gemini API 标准 status 字符串。
func geminiStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	default:
		if statusCode >= 500 {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}

// copyResponseHeaders 按固定白名单透传上游响应头到下游。
// 仅允许透传以下响应头：
// - Retry-After
// - X-Request-ID
func copyResponseHeaders(dst, src http.Header) {
	if dst == nil || len(src) == 0 {
		return
	}
	for key, values := range src {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "retry-after", "x-request-id":
			for _, value := range values {
				dst.Add(key, value)
			}
		}
	}
}

// splitModelAction 从路径中拆分模型名与动作名。
// 例如 "gemini-2.5-pro:generateContent" -> ("gemini-2.5-pro", "generateContent", true)
func splitModelAction(rest string) (model string, action string, ok bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", false
	}
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 || idx >= len(rest)-1 {
		return "", "", false
	}
	model = strings.TrimSpace(rest[:idx])
	action = strings.TrimSpace(rest[idx+1:])
	if model == "" || action == "" {
		return "", "", false
	}
	return model, action, true
}

// extractAlt 从 URL Query 中提取 alt 参数。
func extractAlt(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	for _, key := range []string{"alt", "$alt"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

// shouldUseSSE 判断是否使用 SSE 输出格式。
// 仅当 alt 为空或显式指定为 "sse" 时使用 SSE。
func shouldUseSSE(alt string) bool {
	alt = strings.TrimSpace(alt)
	return alt == "" || strings.EqualFold(alt, "sse")
}

// normalizePathModel 归一化 URL 路径中的模型名。
// 去除 models/ 前缀。
func normalizePathModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(model), "models/") {
		model = model[len("models/"):]
	}
	return strings.TrimSpace(model)
}

// looksLikeSSEWirePayload 判断 payload 是否已经是 SSE 帧格式。
func looksLikeSSEWirePayload(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	return bytes.HasPrefix(payload, []byte("data:")) ||
		bytes.HasPrefix(payload, []byte("event:")) ||
		bytes.HasPrefix(payload, []byte(":"))
}

// cloneHeader 深拷贝 HTTP Header。
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

// cloneValues 深拷贝 URL Query。
func cloneValues(in url.Values) url.Values {
	if len(in) == 0 {
		return nil
	}
	out := make(url.Values, len(in))
	for k, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[k] = copied
	}
	return out
}
