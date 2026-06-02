package registry

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"aistudio-gemini-gateway/internal/access"
	"aistudio-gemini-gateway/internal/common"
	"aistudio-gemini-gateway/internal/config"
	"aistudio-gemini-gateway/service"
)

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// 内置 Cookie 名兜底列表。
// 设计原则：
// 1. 不扩展配置 schema，避免扩大改动面
// 2. 只对 ID / Label 增加 Cookie 读取能力
// 3. 保持 Header / Query 优先，Cookie 仅作兜底
var (
	defaultProviderIDCookieNames    = []string{"provider_id", "providerId"}
	defaultProviderLabelCookieNames = []string{"provider_label", "providerLabel"}
)

// ValidateProviderID 校验 Provider ID 合法性。
func ValidateProviderID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("provider id cannot be empty")
	}
	if !providerIDPattern.MatchString(id) {
		return fmt.Errorf("invalid provider id: %s", id)
	}
	return nil
}

// IdentityExtractor 用于从 WS 握手请求中提取 Provider 身份。
// Provider 不再声明 supported models，
// 因此这里不再解析任何“模型支持能力”字段。
//
// 当前增强点：
// 在保持旧的 Header / Query 提取逻辑不变的基础上，
// 增加 Cookie 兜底提取能力，用于支持跨站 Cookie 注入方案：
//   - provider_id
//   - provider_label
type IdentityExtractor struct {
	cfg config.ProviderIdentityConfig
}

// NewIdentityExtractor 创建身份提取器。
func NewIdentityExtractor(cfg config.ProviderIdentityConfig) *IdentityExtractor {
	cfg.IDHeaderNames = common.NormalizeHeaderNames(cfg.IDHeaderNames)
	cfg.IDQueryNames = common.NormalizeQueryNames(cfg.IDQueryNames)

	cfg.LabelHeaderNames = common.NormalizeHeaderNames(cfg.LabelHeaderNames)
	cfg.LabelQueryNames = common.NormalizeQueryNames(cfg.LabelQueryNames)

	cfg.TagsHeaderNames = common.NormalizeHeaderNames(cfg.TagsHeaderNames)
	cfg.TagsQueryNames = common.NormalizeQueryNames(cfg.TagsQueryNames)

	cfg.PriorityHeaderNames = common.NormalizeHeaderNames(cfg.PriorityHeaderNames)
	cfg.PriorityQueryNames = common.NormalizeQueryNames(cfg.PriorityQueryNames)

	return &IdentityExtractor{cfg: cfg}
}

// Extract 从 HTTP 握手请求中提取 RuntimeProvider 的静态部分。
// 注意：
// 1. 这里不负责设置 ConnectionID，ConnectionID 由 wsrelay.Manager 在连接建立时赋值。
// 2. 这里不再处理 supported models；模型真值源已收敛到静态模型注册表。
// 3. 读取优先级：Header -> Query -> Cookie -> 默认回退
func (e *IdentityExtractor) Extract(r *http.Request, authResult *access.Result) (*service.RuntimeProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if e == nil {
		e = NewIdentityExtractor(config.ProviderIdentityConfig{})
	}

	// Provider ID 提取顺序：
	// 1. Header
	// 2. Query
	// 3. Cookie(provider_id / providerId)
	id := firstIdentityValue(
		r,
		e.cfg.IDHeaderNames,
		e.cfg.IDQueryNames,
		defaultProviderIDCookieNames,
	)

	// Provider Label 提取顺序：
	// 1. Header
	// 2. Query
	// 3. Cookie(provider_label / providerLabel)
	label := firstIdentityValue(
		r,
		e.cfg.LabelHeaderNames,
		e.cfg.LabelQueryNames,
		defaultProviderLabelCookieNames,
	)

	tags := collectCSVValues(r, e.cfg.TagsHeaderNames, e.cfg.TagsQueryNames)

	priority := 0
	priorityRaw := firstValue(r, e.cfg.PriorityHeaderNames, e.cfg.PriorityQueryNames)
	if priorityRaw != "" {
		parsed, err := strconv.Atoi(priorityRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid provider priority: %w", err)
		}
		priority = parsed
	}

	// 仅当请求显式提供了 ID 时才校验。
	// 若 ID 为空，则保持现有行为，由 wsrelay.Manager 后续自动生成默认 Provider ID。
	if id != "" {
		if err := ValidateProviderID(id); err != nil {
			return nil, err
		}
	}
	if len(label) > 256 {
		return nil, fmt.Errorf("provider label too long")
	}

	// Label 若未提供，则回退为 ID。
	// 若 ID 此时也为空，则后续 wsrelay.Manager 会自动生成 ID，并在没有 Label 时用其作默认 Label。
	if label == "" {
		label = id
	}

	metadata := service.ProviderMetadata{
		ProviderType: "aistudio",
		Attributes:   map[string]string{},
		Extra:        map[string]any{},
	}

	if authResult != nil {
		metadata.Attributes["auth_provider"] = authResult.Provider
		metadata.Attributes["auth_principal"] = authResult.Principal
		metadata.Attributes["auth_credential_source"] = authResult.CredentialSource
		metadata.Attributes["auth_credential_name"] = authResult.CredentialName
	}

	provider := &service.RuntimeProvider{
		ID:       id,
		Label:    label,
		State:    service.ProviderStateActive,
		Priority: priority,
		Capabilities: []service.ProviderCapability{
			service.ProviderCapabilityGenerateContent,
			service.ProviderCapabilityStreamGenerateContent,
			service.ProviderCapabilityCountTokens,
		},
		Tags:     common.UniqueNonEmptyStrings(tags, true),
		Metadata: metadata,
	}
	return provider, nil
}

// firstIdentityValue 按“Header -> Query -> Cookie”的顺序提取单值身份字段。
// 该函数用于 Provider ID / Label 的统一读取逻辑。
func firstIdentityValue(r *http.Request, headerNames, queryNames, cookieNames []string) string {
	if r == nil {
		return ""
	}

	// 先走旧策略：Header / Query
	if value := firstValue(r, headerNames, queryNames); value != "" {
		return value
	}

	// 再走 Cookie 兜底策略。
	return firstCookieValue(r, cookieNames)
}

// firstValue 按 Header -> Query 顺序提取单值字段。
func firstValue(r *http.Request, headerNames, queryNames []string) string {
	if r == nil {
		return ""
	}

	for _, name := range headerNames {
		values := r.Header.Values(name)
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}

	query := r.URL.Query()
	for _, name := range queryNames {
		values := query[name]
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

// firstCookieValue 按 Cookie 名顺序提取首个非空 Cookie 值。
// 仅用于 WS 握手阶段的身份标记兜底。
// 由于握手仅发生在连接建立时，性能开销可以忽略。
func firstCookieValue(r *http.Request, cookieNames []string) string {
	if r == nil || len(cookieNames) == 0 {
		return ""
	}

	for _, name := range cookieNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		cookie, err := r.Cookie(name)
		if err != nil || cookie == nil {
			continue
		}

		value := strings.TrimSpace(cookie.Value)
		if value != "" {
			return value
		}
	}

	return ""
}

// collectCSVValues 从 Header / Query 中提取 CSV 风格的多值字段（如 tags）。
func collectCSVValues(r *http.Request, headerNames, queryNames []string) []string {
	var out []string
	if r == nil {
		return nil
	}

	for _, name := range headerNames {
		values := r.Header.Values(name)
		for _, value := range values {
			out = append(out, splitCSV(value)...)
		}
	}

	query := r.URL.Query()
	for _, name := range queryNames {
		values := query[name]
		for _, value := range values {
			out = append(out, splitCSV(value)...)
		}
	}

	return common.UniqueNonEmptyStrings(out, true)
}

// splitCSV 按逗号拆分字符串列表，并去掉空白。
func splitCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}