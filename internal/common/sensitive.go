package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	urlpkg "net/url"
	"strings"
)

// IsSensitiveName 判断字段名是否属于敏感字段。
// 该判断用于配置脱敏、日志脱敏、Header/Query 脱敏。
func IsSensitiveName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}

	// 精确匹配。
	switch lower {
	case "authorization",
		"proxy-authorization",
		"x-goog-api-key",
		"x-api-key",
		"api-key",
		"api_key",
		"apikey",
		"auth_token",
		"token",
		"access_token",
		"refresh_token",
		"secret",
		"secret-key",
		"password",
		"cookie",
		"set-cookie":
		return true
	}

	// 模糊匹配。
	if strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "cookie") {
		return true
	}

	return false
}

// RedactString 对敏感字符串做展示脱敏。
// 只保留极少量头尾字符，避免日志中泄露真实密钥。
func RedactString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	if len(value) <= 8 {
		return value[:1] + "***" + value[len(value)-1:]
	}
	if len(value) <= 16 {
		return value[:2] + "..." + value[len(value)-2:]
	}
	return value[:3] + "..." + value[len(value)-3:]
}

// ShortFingerprint 返回值的稳定短指纹。
// 适合在日志或鉴权 principal 中代替真实密钥。
func ShortFingerprint(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:4])
}

// RedactURLString 对 URL 中的 userinfo 和敏感 query 参数做脱敏。
func RedactURLString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := urlpkg.Parse(raw)
	if err != nil {
		return raw
	}

	if parsed.User != nil {
		username := parsed.User.Username()
		if username != "" {
			if _, hasPwd := parsed.User.Password(); hasPwd {
				parsed.User = urlpkg.UserPassword(RedactString(username), "****")
			} else {
				parsed.User = urlpkg.User(RedactString(username))
			}
		}
	}

	query := parsed.Query()
	for key, values := range query {
		if !IsSensitiveName(key) {
			continue
		}
		for i := range values {
			values[i] = RedactString(values[i])
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// RedactByName 按字段名决定是否对字符串进行脱敏。
func RedactByName(name, value string) string {
	if IsSensitiveName(name) {
		return RedactString(value)
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lower, "url") {
		return RedactURLString(value)
	}
	return value
}

// RedactHeader 脱敏 HTTP Header。
func RedactHeader(in http.Header) http.Header {
	if len(in) == 0 {
		return nil
	}
	out := make(http.Header, len(in))
	for key, values := range in {
		cloned := make([]string, len(values))
		copy(cloned, values)
		if IsSensitiveName(key) {
			for i := range cloned {
				cloned[i] = RedactString(cloned[i])
			}
		}
		out[key] = cloned
	}
	return out
}

// RedactValues 脱敏 URL Query。
func RedactValues(in urlpkg.Values) urlpkg.Values {
	if len(in) == 0 {
		return nil
	}
	out := make(urlpkg.Values, len(in))
	for key, values := range in {
		cloned := make([]string, len(values))
		copy(cloned, values)
		if IsSensitiveName(key) {
			for i := range cloned {
				cloned[i] = RedactString(cloned[i])
			}
		}
		out[key] = cloned
	}
	return out
}

// RedactStringMap 脱敏 map[string]string。
func RedactStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = RedactByName(key, value)
	}
	return out
}

// RedactAnyMap 脱敏 map[string]any。
func RedactAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = RedactValue(key, value)
	}
	return out
}

// RedactValue 递归脱敏任意值。
func RedactValue(name string, value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return RedactByName(name, typed)
	case []string:
		out := make([]string, len(typed))
		for i := range typed {
			out[i] = RedactByName(name, typed[i])
		}
		return out
	case []byte:
		if IsSensitiveName(name) {
			return []byte(RedactString(string(typed)))
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(name)), "url") {
			return []byte(RedactURLString(string(typed)))
		}
		cloned := make([]byte, len(typed))
		copy(cloned, typed)
		return cloned
	case json.RawMessage:
		if IsSensitiveName(name) {
			return json.RawMessage([]byte(`"****"`))
		}
		cloned := make([]byte, len(typed))
		copy(cloned, typed)
		return json.RawMessage(cloned)
	case http.Header:
		return RedactHeader(typed)
	case urlpkg.Values:
		return RedactValues(typed)
	case map[string]string:
		return RedactStringMap(typed)
	case map[string]any:
		return RedactAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = RedactValue(name, typed[i])
		}
		return out
	default:
		return typed
	}
}
