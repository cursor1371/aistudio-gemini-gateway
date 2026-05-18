package wsrelay

import (
	"net/http"
	"strings"
)

// NewOriginValidator 创建 Origin 校验函数。
//
// mode 支持：
//   - "any"：放行全部 Origin
//   - "allow-missing"：允许无 Origin 请求；若携带 Origin 则必须在白名单中
//   - "strict"：必须携带 Origin 且必须命中白名单
//
// allowedOrigins 支持三种写法：
//   1. "*"：允许任意 Origin
//   2. 精确匹配，例如：https://ai.studio
//   3. 通配符匹配，例如：https://ais-pre-*.run.app
//
// 说明：
//   - 通配符仅支持简单的 "*"，表示匹配任意长度字符
//   - 匹配前会先做 normalizeOrigin 归一化
func NewOriginValidator(mode string, allowedOrigins []string) func(*http.Request) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "allow-missing"
	}

	exactAllowed := make(map[string]struct{}, len(allowedOrigins))
	patternAllowed := make([]string, 0, len(allowedOrigins))
	allowAny := false

	for _, item := range allowedOrigins {
		item = normalizeOrigin(item)
		if item == "" {
			continue
		}
		if item == "*" {
			allowAny = true
			continue
		}
		if strings.Contains(item, "*") {
			patternAllowed = append(patternAllowed, item)
			continue
		}
		exactAllowed[item] = struct{}{}
	}

	return func(r *http.Request) bool {
		if allowAny || mode == "any" {
			return true
		}
		if r == nil {
			return mode != "strict"
		}

		origin := normalizeOrigin(r.Header.Get("Origin"))

		switch mode {
		case "strict":
			if origin == "" {
				return false
			}
			return isOriginAllowed(origin, exactAllowed, patternAllowed)

		case "allow-missing":
			if origin == "" {
				return true
			}
			return isOriginAllowed(origin, exactAllowed, patternAllowed)

		default:
			// 未知模式按保守策略处理：允许无 Origin，但携带 Origin 时须匹配。
			if origin == "" {
				return true
			}
			return isOriginAllowed(origin, exactAllowed, patternAllowed)
		}
	}
}

// isOriginAllowed 判断一个归一化后的 origin 是否命中允许列表。
func isOriginAllowed(origin string, exact map[string]struct{}, patterns []string) bool {
	if origin == "" {
		return false
	}

	if _, ok := exact[origin]; ok {
		return true
	}

	for _, pattern := range patterns {
		if matchOriginPattern(pattern, origin) {
			return true
		}
	}
	return false
}

// matchOriginPattern 支持简单的 "*" 通配符匹配。
// 例如：
//   - https://ais-pre-*.run.app
//   - https://ais-dev-*.run.app
func matchOriginPattern(pattern, origin string) bool {
	pattern = normalizeOrigin(pattern)
	origin = normalizeOrigin(origin)

	if pattern == "" || origin == "" {
		return false
	}
	if pattern == "*" {
		return true
	}

	pi, oi := 0, 0
	starIdx := -1
	matchIdx := 0

	for oi < len(origin) {
		if pi < len(pattern) && pattern[pi] == origin[oi] {
			pi++
			oi++
			continue
		}
		if pi < len(pattern) && pattern[pi] == '*' {
			starIdx = pi
			matchIdx = oi
			pi++
			continue
		}
		if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			oi = matchIdx
			continue
		}
		return false
	}

	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// normalizeOrigin 归一化 Origin 值：去除首尾空格、尾部斜杠、统一小写。
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	origin = strings.TrimSuffix(origin, "/")
	return strings.ToLower(origin)
}
