package wsrelay

// MessageType 表示 WS 中继协议中的消息类型。
type MessageType string

const (
	// MessageTypeHTTPReq 表示网关发给 Provider 的 HTTP 请求包。
	MessageTypeHTTPReq MessageType = "http_request"
	// MessageTypeHTTPResp 表示 Provider 返回的一次性 HTTP 响应。
	MessageTypeHTTPResp MessageType = "http_response"
	// MessageTypeStreamStart 表示流式响应开始。
	MessageTypeStreamStart MessageType = "stream_start"
	// MessageTypeStreamChunk 表示流式数据块。
	MessageTypeStreamChunk MessageType = "stream_chunk"
	// MessageTypeStreamEnd 表示流式响应结束。
	MessageTypeStreamEnd MessageType = "stream_end"
	// MessageTypeError 表示错误包。
	MessageTypeError MessageType = "error"
	// MessageTypePing 表示应用层 ping。
	MessageTypePing MessageType = "ping"
	// MessageTypePong 表示应用层 pong。
	MessageTypePong MessageType = "pong"
)

// Terminal 判断消息类型是否为终止消息。
// 终止消息包括：http_response、stream_end、error。
func (t MessageType) Terminal() bool {
	switch t {
	case MessageTypeHTTPResp, MessageTypeStreamEnd, MessageTypeError:
		return true
	default:
		return false
	}
}

// Message 是 WS Relay 使用的统一 JSON 消息结构。
type Message struct {
	ID      string         `json:"id"`
	Type    MessageType    `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}
