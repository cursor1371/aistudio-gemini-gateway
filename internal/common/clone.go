package common

import (
	"encoding/json"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// CloneBytes 深拷贝字节切片。
func CloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// CloneStringSlice 深拷贝字符串切片。
func CloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// CloneStringMap 深拷贝 map[string]string。
func CloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// CloneAnyMap 深拷贝 map[string]any。
func CloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = DeepCloneAny(v)
	}
	return out
}

// CloneHeader 深拷贝 HTTP Header。
func CloneHeader(in http.Header) http.Header {
	if len(in) == 0 {
		return nil
	}
	out := make(http.Header, len(in))
	for k, values := range in {
		cloned := make([]string, len(values))
		copy(cloned, values)
		out[k] = cloned
	}
	return out
}

// CloneValues 深拷贝 URL Query。
func CloneValues(in url.Values) url.Values {
	if len(in) == 0 {
		return nil
	}
	out := make(url.Values, len(in))
	for k, values := range in {
		cloned := make([]string, len(values))
		copy(cloned, values)
		out[k] = cloned
	}
	return out
}

// DeepCloneAny 递归深拷贝常见动态值类型。
// 主要服务于配置、元数据等动态结构的安全复制。
func DeepCloneAny(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case []byte:
		return CloneBytes(typed)
	case json.RawMessage:
		return json.RawMessage(CloneBytes(typed))
	case []string:
		return CloneStringSlice(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = DeepCloneAny(typed[i])
		}
		return out
	case map[string]any:
		return CloneAnyMap(typed)
	case map[string]string:
		return CloneStringMap(typed)
	case map[any]any:
		out := make(map[any]any, len(typed))
		for k, val := range typed {
			out[k] = DeepCloneAny(val)
		}
		return out
	case http.Header:
		return CloneHeader(typed)
	case url.Values:
		return CloneValues(typed)
	default:
		return typed
	}
}

// UniqueNonEmptyStrings 对字符串切片执行去空、去重并保持原始顺序。
// caseInsensitive 为 true 时，去重按小写比较。
func UniqueNonEmptyStrings(in []string, caseInsensitive bool) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := trimmed
		if caseInsensitive {
			key = strings.ToLower(trimmed)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeHeaderNames 对 HTTP Header 名称进行 trim、MIME 规范化、去重。
func NormalizeHeaderNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeQueryNames 对 URL Query 参数名称进行 trim、小写、去重。
func NormalizeQueryNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
