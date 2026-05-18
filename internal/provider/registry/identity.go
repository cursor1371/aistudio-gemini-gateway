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
// Provider 不再声明模型支持范围，因此这里不解析任何模型能力字段。
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
// ConnectionID 由 wsrelay.Manager 在连接建立时赋值，这里不负责。
func (e *IdentityExtractor) Extract(r *http.Request, authResult *access.Result) (*service.RuntimeProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if e == nil {
		e = NewIdentityExtractor(config.ProviderIdentityConfig{})
	}

	id := firstValue(r, e.cfg.IDHeaderNames, e.cfg.IDQueryNames)
	label := firstValue(r, e.cfg.LabelHeaderNames, e.cfg.LabelQueryNames)
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

	if id != "" {
		if err := ValidateProviderID(id); err != nil {
			return nil, err
		}
	}
	if len(label) > 256 {
		return nil, fmt.Errorf("provider label too long")
	}
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
