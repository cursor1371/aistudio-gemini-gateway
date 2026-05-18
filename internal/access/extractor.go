package access

import (
	"net/http"
	"strings"

	"aistudio-gemini-gateway/internal/common"
)

// 凭据来源常量。
const (
	credentialSourceAuthorizationBearer = "authorization_bearer"
	credentialSourceHeader              = "header"
	credentialSourceQuery               = "query"
)

// DefaultHTTPExtractionOptions 返回 HTTP API 默认凭据提取配置。
func DefaultHTTPExtractionOptions() ExtractionOptions {
	return ExtractionOptions{
		AllowBearer: true,
		HeaderNames: []string{"X-Goog-Api-Key", "X-Api-Key"},
		QueryNames:  []string{"key", "auth_token"},
	}
}

// DefaultWebSocketExtractionOptions 返回 WS 握手默认凭据提取配置。
func DefaultWebSocketExtractionOptions() ExtractionOptions {
	return ExtractionOptions{
		AllowBearer: true,
		HeaderNames: []string{"X-Goog-Api-Key", "X-Api-Key"},
		QueryNames:  []string{"key", "auth_token"},
	}
}

// NormalizeExtractionOptions 规范化凭据提取配置。
func NormalizeExtractionOptions(opts ExtractionOptions) ExtractionOptions {
	out := opts
	out.HeaderNames = common.NormalizeHeaderNames(out.HeaderNames)
	out.QueryNames = common.NormalizeQueryNames(out.QueryNames)
	return out
}

// ExtractCredential 从 HTTP 请求中提取凭据。
// 提取顺序：Bearer -> Header -> Query。
// 返回第一个匹配的非空凭据。
func ExtractCredential(r *http.Request, opts ExtractionOptions) (ExtractedCredential, bool) {
	if r == nil {
		return ExtractedCredential{}, false
	}
	opts = NormalizeExtractionOptions(opts)

	// 1. 尝试 Authorization: Bearer <token>。
	if opts.AllowBearer {
		if token := extractBearerToken(r.Header.Get("Authorization")); token != "" {
			return ExtractedCredential{
				Value:  token,
				Source: credentialSourceAuthorizationBearer,
				Name:   "Authorization",
			}, true
		}
	}

	// 2. 尝试配置的 Header 字段。
	for _, name := range opts.HeaderNames {
		values := r.Header.Values(name)
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			return ExtractedCredential{
				Value:  trimmed,
				Source: credentialSourceHeader,
				Name:   name,
			}, true
		}
	}

	// 3. 尝试配置的 Query 参数。
	query := r.URL.Query()
	for _, name := range opts.QueryNames {
		values := query[name]
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			return ExtractedCredential{
				Value:  trimmed,
				Source: credentialSourceQuery,
				Name:   name,
			}, true
		}
	}

	return ExtractedCredential{}, false
}

// extractBearerToken 从 Authorization 头中提取 Bearer token。
func extractBearerToken(headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return ""
	}
	const prefix = "bearer "
	if len(headerValue) < len(prefix) {
		return ""
	}
	if !strings.EqualFold(headerValue[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(headerValue[len(prefix):])
}
