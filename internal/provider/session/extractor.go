package session

import (
	"fmt"
	"hash/fnv"
	"strings"

	"aistudio-gemini-gateway/service"
	"github.com/tidwall/gjson"
)

// Extractor 定义 Session ID 提取接口。
// 该接口由 Pipeline 使用；selector 不自行提取 session。
type Extractor interface {
	Extract(req *service.GatewayRequest) string
}

// DefaultExtractor 是默认 Session ID 提取器。
// 提取优先级：
// 1. req.SessionID（调用方显式传入）
// 2. Header
// 3. Query
// 4. metadata / body 中的显式 session 字段
// 5. 稳定内容哈希兜底
type DefaultExtractor struct {
	HeaderNames  []string
	QueryNames   []string
	BodyFields   []string
	FallbackHash bool
}

// NewDefaultExtractor 创建默认提取器。
func NewDefaultExtractor() *DefaultExtractor {
	return &DefaultExtractor{
		HeaderNames:  []string{"X-Session-ID"},
		QueryNames:   []string{"session_id", "conversation_id"},
		BodyFields:   []string{"session_id", "conversation_id"},
		// 轻量部署场景下，默认不再对“无显式 session”的请求做完整 JSON 遍历与内容哈希，
		// 以减少热路径 CPU 开销。若业务确有需求，可在后续自定义 extractor 中自行开启。
		FallbackHash: false,
	}
}

// Extract 提取 session ID。
func (e *DefaultExtractor) Extract(req *service.GatewayRequest) string {
	if req == nil {
		return ""
	}

	// 1. 显式传入的 SessionID 优先级最高。
	if direct := strings.TrimSpace(req.SessionID); direct != "" {
		return direct
	}

	// 2. Header。
	if value := extractFromHeaders(req, e.HeaderNames); value != "" {
		return "header:" + value
	}

	// 3. Query。
	if value := extractFromQuery(req, e.QueryNames); value != "" {
		return "query:" + value
	}

	// 4. Metadata 中的显式 session 字段。
	if value := extractFromMetadata(req.Metadata, "session_id", "conversation_id", "user_id"); value != "" {
		return "meta:" + value
	}

	// 5. Body 中的显式字段。
	if value := extractExplicitFromPayload(req.Payload, e.BodyFields); value != "" {
		return value
	}

	// 6. 稳定内容哈希兜底。
	if e != nil && e.FallbackHash {
		return extractStableContentHash(req.Payload)
	}
	return ""
}

func extractFromHeaders(req *service.GatewayRequest, names []string) string {
	if req == nil || len(req.Headers) == 0 {
		return ""
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values := req.Headers.Values(name)
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func extractFromQuery(req *service.GatewayRequest, names []string) string {
	if req == nil || len(req.Query) == 0 {
		return ""
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values := req.Query[name]
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func extractFromMetadata(meta map[string]any, keys ...string) string {
	if len(meta) == 0 {
		return ""
	}
	for _, key := range keys {
		raw, ok := meta[key]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func extractExplicitFromPayload(payload []byte, fields []string) string {
	if len(payload) == 0 || !gjson.ValidBytes(payload) || len(fields) == 0 {
		return ""
	}

	// 先尝试 metadata.user_id，兼容部分客户端。
	if userID := strings.TrimSpace(gjson.GetBytes(payload, "metadata.user_id").String()); userID != "" {
		return "user:" + userID
	}

	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		value := strings.TrimSpace(gjson.GetBytes(payload, field).String())
		if value != "" {
			switch field {
			case "conversation_id":
				return "conv:" + value
			case "session_id":
				return "session:" + value
			default:
				return value
			}
		}
	}
	return ""
}

// extractStableContentHash 生成稳定内容哈希。
// 使用 system prompt + 第一轮 user + 第一轮 assistant 的组合作为回退会话键。
func extractStableContentHash(payload []byte) string {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return ""
	}

	var systemPrompt, firstUserMsg, firstAssistantMsg string

	// Gemini systemInstruction。
	sysInstr := gjson.GetBytes(payload, "systemInstruction.parts")
	if sysInstr.Exists() && sysInstr.IsArray() {
		sysInstr.ForEach(func(_, part gjson.Result) bool {
			if text := strings.TrimSpace(part.Get("text").String()); text != "" && systemPrompt == "" {
				systemPrompt = truncateString(text, 100)
				return false
			}
			return true
		})
	}

	// Gemini contents。
	contents := gjson.GetBytes(payload, "contents")
	if contents.Exists() && contents.IsArray() {
		contents.ForEach(func(_, msg gjson.Result) bool {
			role := strings.TrimSpace(msg.Get("role").String())
			msg.Get("parts").ForEach(func(_, part gjson.Result) bool {
				text := strings.TrimSpace(part.Get("text").String())
				if text == "" {
					return true
				}
				switch role {
				case "user":
					if firstUserMsg == "" {
						firstUserMsg = truncateString(text, 100)
					}
				case "model":
					if firstAssistantMsg == "" {
						firstAssistantMsg = truncateString(text, 100)
					}
				}
				return false
			})
			if firstUserMsg != "" && firstAssistantMsg != "" {
				return false
			}
			return true
		})
	}

	// 兼容 messages / instructions 风格。
	if systemPrompt == "" {
		if instr := strings.TrimSpace(gjson.GetBytes(payload, "instructions").String()); instr != "" {
			systemPrompt = truncateString(instr, 100)
		}
	}
	if firstUserMsg == "" {
		messages := gjson.GetBytes(payload, "messages")
		if messages.Exists() && messages.IsArray() {
			messages.ForEach(func(_, msg gjson.Result) bool {
				role := strings.TrimSpace(msg.Get("role").String())
				content := extractMessageContent(msg.Get("content"))
				if content == "" {
					return true
				}
				switch role {
				case "system":
					if systemPrompt == "" {
						systemPrompt = truncateString(content, 100)
					}
				case "user":
					if firstUserMsg == "" {
						firstUserMsg = truncateString(content, 100)
					}
				case "assistant":
					if firstAssistantMsg == "" {
						firstAssistantMsg = truncateString(content, 100)
					}
				}
				if firstUserMsg != "" && firstAssistantMsg != "" {
					return false
				}
				return true
			})
		}
	}

	if systemPrompt == "" && firstUserMsg == "" {
		return ""
	}

	shortHash := computeStableHash(systemPrompt, firstUserMsg, "")
	if firstAssistantMsg == "" {
		return shortHash
	}
	return computeStableHash(systemPrompt, firstUserMsg, firstAssistantMsg)
}

func extractMessageContent(content gjson.Result) string {
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String())
	}
	if !content.IsArray() {
		return ""
	}

	var texts []string
	content.ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() == "text" {
			if text := strings.TrimSpace(part.Get("text").String()); text != "" {
				texts = append(texts, text)
			}
		}
		return true
	})
	return strings.TrimSpace(strings.Join(texts, " "))
}

func computeStableHash(systemPrompt, userMsg, assistantMsg string) string {
	h := fnv.New64a()
	if systemPrompt != "" {
		_, _ = h.Write([]byte("sys:" + systemPrompt + "\n"))
	}
	if userMsg != "" {
		_, _ = h.Write([]byte("usr:" + userMsg + "\n"))
	}
	if assistantMsg != "" {
		_, _ = h.Write([]byte("ast:" + assistantMsg + "\n"))
	}
	return fmt.Sprintf("msg:%016x", h.Sum64())
}

func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
