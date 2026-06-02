package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"aistudio-gemini-gateway/internal/common"
)

// ---------------------------------------------------------------------------
// ValidationError
// ---------------------------------------------------------------------------

// ValidationError 表示配置校验失败。
type ValidationError struct {
	Problems []string
}

// Error 实现 error 接口。
func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return ""
	}
	return "配置校验失败: " + strings.Join(e.Problems, "; ")
}

// ---------------------------------------------------------------------------
// 内部校验器
// ---------------------------------------------------------------------------

type validator struct {
	problems []string
}

func (v *validator) add(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

// ---------------------------------------------------------------------------
// ApplyDefaults
// ---------------------------------------------------------------------------

// ApplyDefaults 为未配置的字段填充默认值，不会覆盖用户显式设置。
func (c *Config) ApplyDefaults() {
	if c == nil {
		return
	}

	// server
	if strings.TrimSpace(c.Server.Host) == "" {
		c.Server.Host = DefaultHost
	}
	if c.Server.Port == 0 {
		c.Server.Port = DefaultPort
	}
	if strings.TrimSpace(c.Server.ReadTimeout) == "" {
		c.Server.ReadTimeout = DefaultServerReadTimeout
	}
	if strings.TrimSpace(c.Server.WriteTimeout) == "" {
		c.Server.WriteTimeout = DefaultServerWriteTimeout
	}
	if strings.TrimSpace(c.Server.IdleTimeout) == "" {
		c.Server.IdleTimeout = DefaultServerIdleTimeout
	}
	if strings.TrimSpace(c.Server.ShutdownTimeout) == "" {
		c.Server.ShutdownTimeout = DefaultServerShutdownTimeout
	}

	// access
	applyAccessPolicyDefaults(&c.Access.HTTP, DefaultHTTPHeaderNames, DefaultHTTPQueryNames)
	if c.Access.CORS.Enabled {
		if len(c.Access.CORS.AllowedMethods) == 0 {
			c.Access.CORS.AllowedMethods = common.CloneStringSlice(DefaultCORSAllowedMethods)
		}
		if len(c.Access.CORS.AllowedHeaders) == 0 {
			c.Access.CORS.AllowedHeaders = common.CloneStringSlice(DefaultCORSAllowedHeaders)
		}
		if len(c.Access.CORS.ExposeHeaders) == 0 {
			c.Access.CORS.ExposeHeaders = common.CloneStringSlice(DefaultCORSExposeHeaders)
		}
		if c.Access.CORS.MaxAgeSeconds <= 0 {
			c.Access.CORS.MaxAgeSeconds = DefaultCORSMaxAgeSeconds
		}
	}

	// websocket
	if strings.TrimSpace(c.WebSocket.Path) == "" {
		c.WebSocket.Path = DefaultWebSocketPath
	}
	if strings.TrimSpace(c.WebSocket.HandshakeTimeout) == "" {
		c.WebSocket.HandshakeTimeout = DefaultWSHandshakeTimeout
	}
	if strings.TrimSpace(c.WebSocket.ReadTimeout) == "" {
		c.WebSocket.ReadTimeout = DefaultWSReadTimeout
	}
	if strings.TrimSpace(c.WebSocket.WriteTimeout) == "" {
		c.WebSocket.WriteTimeout = DefaultWSWriteTimeout
	}
	if c.WebSocket.MaxMessageSize <= 0 {
		c.WebSocket.MaxMessageSize = DefaultWSMaxMessageSize
	}
	if strings.TrimSpace(c.WebSocket.Heartbeat.PingInterval) == "" {
		c.WebSocket.Heartbeat.PingInterval = DefaultWSPingInterval
	}
	if strings.TrimSpace(c.WebSocket.Heartbeat.PongTimeout) == "" {
		c.WebSocket.Heartbeat.PongTimeout = DefaultWSPongTimeout
	}
	if strings.TrimSpace(c.WebSocket.Origin.Mode) == "" {
		c.WebSocket.Origin.Mode = DefaultOriginMode
	}
	applyAccessPolicyDefaults(&c.WebSocket.Auth, DefaultWSHeaderNames, DefaultWSQueryNames)
	applyProviderIdentityDefaults(&c.WebSocket.ProviderIdentity)

	// routing
	if strings.TrimSpace(c.Routing.Strategy) == "" {
		c.Routing.Strategy = DefaultRoutingStrategy
	}
	if strings.TrimSpace(c.Routing.SessionAffinityTTL) == "" {
		c.Routing.SessionAffinityTTL = DefaultSessionAffinityTTL
	}
	if c.Routing.BootstrapRetries < 0 {
		c.Routing.BootstrapRetries = 0
	}
	if strings.TrimSpace(c.Routing.ProviderCooldown) == "" {
		c.Routing.ProviderCooldown = DefaultProviderCooldown
	}
	if strings.TrimSpace(c.Routing.BootstrapTimeout) == "" {
		c.Routing.BootstrapTimeout = DefaultBootstrapTimeout
	}
	if strings.TrimSpace(c.Routing.StreamIdleTimeout) == "" {
		c.Routing.StreamIdleTimeout = DefaultStreamIdleTimeout
	}
	if strings.TrimSpace(c.Routing.NonStreamTimeout) == "" {
		c.Routing.NonStreamTimeout = DefaultNonStreamTimeout
	}

	// models
	if strings.TrimSpace(c.Models.Source) == "" {
		c.Models.Source = DefaultModelsSource
	}

	// gemini
	if strings.TrimSpace(c.Gemini.APIVersion) == "" {
		c.Gemini.APIVersion = DefaultGeminiAPIVersion
	}
	if strings.TrimSpace(c.Gemini.SafetyDefaultsMode) == "" {
		c.Gemini.SafetyDefaultsMode = DefaultGeminiSafetyMode
	}
	if strings.TrimSpace(c.Gemini.Thinking.Mode) == "" {
		c.Gemini.Thinking.Mode = DefaultGeminiThinkingMode
	}

	// logging
	if strings.TrimSpace(c.Logging.Level) == "" {
		c.Logging.Level = DefaultLoggingLevel
	}
	if strings.TrimSpace(c.Logging.Format) == "" {
		c.Logging.Format = DefaultLoggingFormat
	}
}

// ---------------------------------------------------------------------------
// Normalize
// ---------------------------------------------------------------------------

// Normalize 对配置做 trim、去重、命名归一化。
func (c *Config) Normalize() {
	if c == nil {
		return
	}

	c.Server.Host = strings.TrimSpace(c.Server.Host)
	c.Server.ReadTimeout = strings.TrimSpace(c.Server.ReadTimeout)
	c.Server.WriteTimeout = strings.TrimSpace(c.Server.WriteTimeout)
	c.Server.IdleTimeout = strings.TrimSpace(c.Server.IdleTimeout)
	c.Server.ShutdownTimeout = strings.TrimSpace(c.Server.ShutdownTimeout)
	c.Server.TLS.CertFile = strings.TrimSpace(c.Server.TLS.CertFile)
	c.Server.TLS.KeyFile = strings.TrimSpace(c.Server.TLS.KeyFile)

	normalizeAccessPolicy(&c.Access.HTTP)
	normalizeCORSConfig(&c.Access.CORS)

	c.WebSocket.Path = strings.TrimSpace(c.WebSocket.Path)
	c.WebSocket.HandshakeTimeout = strings.TrimSpace(c.WebSocket.HandshakeTimeout)
	c.WebSocket.ReadTimeout = strings.TrimSpace(c.WebSocket.ReadTimeout)
	c.WebSocket.WriteTimeout = strings.TrimSpace(c.WebSocket.WriteTimeout)
	c.WebSocket.Heartbeat.PingInterval = strings.TrimSpace(c.WebSocket.Heartbeat.PingInterval)
	c.WebSocket.Heartbeat.PongTimeout = strings.TrimSpace(c.WebSocket.Heartbeat.PongTimeout)
	c.WebSocket.Origin.Mode = normalizeEnum(c.WebSocket.Origin.Mode)
	c.WebSocket.Origin.AllowedOrigins = common.UniqueNonEmptyStrings(c.WebSocket.Origin.AllowedOrigins, false)
	normalizeAccessPolicy(&c.WebSocket.Auth)
	normalizeProviderIdentityConfig(&c.WebSocket.ProviderIdentity)

	c.Routing.Strategy = normalizeEnum(c.Routing.Strategy)
	c.Routing.SessionAffinityTTL = strings.TrimSpace(c.Routing.SessionAffinityTTL)
	c.Routing.ProviderCooldown = strings.TrimSpace(c.Routing.ProviderCooldown)
	c.Routing.BootstrapTimeout = strings.TrimSpace(c.Routing.BootstrapTimeout)
	c.Routing.StreamIdleTimeout = strings.TrimSpace(c.Routing.StreamIdleTimeout)
	c.Routing.NonStreamTimeout = strings.TrimSpace(c.Routing.NonStreamTimeout)

	c.Models.Source = normalizeEnum(c.Models.Source)
	c.Models.Entries = normalizeModelEntries(c.Models.Entries)
	c.Models.Aliases = normalizeModelAliases(c.Models.Aliases)

	c.Gemini.APIVersion = strings.TrimSpace(c.Gemini.APIVersion)
	c.Gemini.SafetyDefaultsMode = normalizeEnum(c.Gemini.SafetyDefaultsMode)
	c.Gemini.Thinking.Mode = normalizeEnum(c.Gemini.Thinking.Mode)
	for i := range c.Gemini.DefaultSafetySettings {
		c.Gemini.DefaultSafetySettings[i].Category = strings.TrimSpace(c.Gemini.DefaultSafetySettings[i].Category)
		c.Gemini.DefaultSafetySettings[i].Threshold = strings.TrimSpace(c.Gemini.DefaultSafetySettings[i].Threshold)
	}

	c.Logging.Level = normalizeEnum(c.Logging.Level)
	c.Logging.Format = normalizeEnum(c.Logging.Format)
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

// Validate 校验配置合法性。
func (c *Config) Validate() error {
	if c == nil {
		return &ValidationError{Problems: []string{"配置对象不能为空"}}
	}

	v := &validator{}

	// --- server ---
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		v.add("server.port 必须在 0~65535 之间")
	}
	validateDuration(v, "server.read-timeout", c.Server.ReadTimeout)
	validateDuration(v, "server.write-timeout", c.Server.WriteTimeout)
	validateDuration(v, "server.idle-timeout", c.Server.IdleTimeout)
	validateDuration(v, "server.shutdown-timeout", c.Server.ShutdownTimeout)
	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" {
			v.add("server.tls.cert-file 不能为空")
		}
		if c.Server.TLS.KeyFile == "" {
			v.add("server.tls.key-file 不能为空")
		}
	}

	// --- access ---
	validateAccessPolicy(v, "access.http", c.Access.HTTP)
	if c.Access.CORS.Enabled {
		if len(c.Access.CORS.AllowedOrigins) == 0 {
			v.add("access.cors.allowed-origins 不能为空")
		}
		if c.Access.CORS.AllowCredentials {
			for _, origin := range c.Access.CORS.AllowedOrigins {
				if strings.TrimSpace(origin) == "*" {
					v.add("access.cors.allow-credentials=true 时，allowed-origins 不能包含 *")
					break
				}
			}
		}
		if c.Access.CORS.MaxAgeSeconds < 0 {
			v.add("access.cors.max-age-seconds 不能小于 0")
		}
	}

	// --- websocket ---
	if !strings.HasPrefix(c.WebSocket.Path, "/") {
		v.add("websocket.path 必须以 / 开头")
	}
	validateDuration(v, "websocket.handshake-timeout", c.WebSocket.HandshakeTimeout)
	validateDuration(v, "websocket.read-timeout", c.WebSocket.ReadTimeout)
	validateDuration(v, "websocket.write-timeout", c.WebSocket.WriteTimeout)
	validateDuration(v, "websocket.heartbeat.ping-interval", c.WebSocket.Heartbeat.PingInterval)
	validateDuration(v, "websocket.heartbeat.pong-timeout", c.WebSocket.Heartbeat.PongTimeout)
	if c.WebSocket.MaxMessageSize <= 0 {
		v.add("websocket.max-message-size 必须大于 0")
	}
	switch c.WebSocket.Origin.Mode {
	case "any", "allow-missing", "strict":
	default:
		v.add("websocket.origin.mode 只能是 any / allow-missing / strict")
	}
	if c.WebSocket.Origin.Mode == "strict" && len(c.WebSocket.Origin.AllowedOrigins) == 0 {
		v.add("websocket.origin.mode=strict 时，allowed-origins 不能为空")
	}
	validateAccessPolicy(v, "websocket.auth", c.WebSocket.Auth)

	// --- routing ---
	switch c.Routing.Strategy {
	case "round_robin", "fill_first", "session_affinity":
	default:
		v.add("routing.strategy 只能是 round_robin / fill_first / session_affinity")
	}
	validateDuration(v, "routing.session-affinity-ttl", c.Routing.SessionAffinityTTL)
	validateDuration(v, "routing.provider-cooldown", c.Routing.ProviderCooldown)
	validateDuration(v, "routing.bootstrap-timeout", c.Routing.BootstrapTimeout)
	validateDuration(v, "routing.stream-idle-timeout", c.Routing.StreamIdleTimeout)
	validateDuration(v, "routing.non-stream-timeout", c.Routing.NonStreamTimeout)
	if c.Routing.BootstrapRetries < 0 {
		v.add("routing.bootstrap-retries 不能小于 0")
	}

	// --- models ---
	validateModelsConfig(v, c.Models)

	// --- gemini ---
	if strings.TrimSpace(c.Gemini.APIVersion) == "" {
		v.add("gemini.api-version 不能为空")
	}
	switch c.Gemini.SafetyDefaultsMode {
	case "auto", "off":
	default:
		v.add("gemini.safety-defaults-mode 只能是 auto / off")
	}
	switch c.Gemini.Thinking.Mode {
	case "auto", "off":
	default:
		v.add("gemini.thinking.mode 只能是 auto / off")
	}
	for i, item := range c.Gemini.DefaultSafetySettings {
		if strings.TrimSpace(item.Category) == "" {
			v.add("gemini.default-safety-settings[%d].category 不能为空", i)
		}
		if strings.TrimSpace(item.Threshold) == "" {
			v.add("gemini.default-safety-settings[%d].threshold 不能为空", i)
		}
	}

	// --- logging ---
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		v.add("logging.level 只能是 debug / info / warn / error")
	}
	switch c.Logging.Format {
	case "text", "json":
	default:
		v.add("logging.format 只能是 text / json")
	}

	return v.err()
}

// ---------------------------------------------------------------------------
// ParseDurationOrDefault
// ---------------------------------------------------------------------------

// ParseFlexibleDuration 解析 duration。
// 兼容规则：
// 1. 若值带单位，如 30s / 5m / 1h，则直接按标准 duration 解析
// 2. 若值为纯整数，如 30 / 300，则按“秒”解释
func ParseFlexibleDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// 纯整数按“秒”解释，兼容无服务器平台常见环境变量写法。
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}

	return time.ParseDuration(value)
}

// ParseDurationOrDefault 将 duration 字符串解析为 time.Duration。
// 若 value 为空则返回 fallback。
// 兼容纯数字秒值，例如：30 -> 30s。
func ParseDurationOrDefault(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return ParseFlexibleDuration(value)
}

// ---------------------------------------------------------------------------
// 内部：默认值填充辅助
// ---------------------------------------------------------------------------

func applyAccessPolicyDefaults(policy *AccessPolicy, defaultHeaders, defaultQueries []string) {
	if policy == nil {
		return
	}
	// 仅当来源配置整体为空时才填默认来源，避免覆盖用户有意配置的"只允许 query"等场景。
	if !policy.AllowBearer && len(policy.HeaderNames) == 0 && len(policy.QueryNames) == 0 {
		policy.AllowBearer = true
		policy.HeaderNames = common.CloneStringSlice(defaultHeaders)
		policy.QueryNames = common.CloneStringSlice(defaultQueries)
	}
}

func applyProviderIdentityDefaults(identity *ProviderIdentityConfig) {
	if identity == nil {
		return
	}
	if len(identity.IDHeaderNames) == 0 && len(identity.IDQueryNames) == 0 {
		identity.IDHeaderNames = common.CloneStringSlice(DefaultProviderIDHeaderNames)
		identity.IDQueryNames = common.CloneStringSlice(DefaultProviderIDQueryNames)
	}
	if len(identity.LabelHeaderNames) == 0 && len(identity.LabelQueryNames) == 0 {
		identity.LabelHeaderNames = common.CloneStringSlice(DefaultProviderLabelHeaderNames)
		identity.LabelQueryNames = common.CloneStringSlice(DefaultProviderLabelQueryNames)
	}
	if len(identity.TagsHeaderNames) == 0 && len(identity.TagsQueryNames) == 0 {
		identity.TagsHeaderNames = common.CloneStringSlice(DefaultProviderTagsHeaderNames)
		identity.TagsQueryNames = common.CloneStringSlice(DefaultProviderTagsQueryNames)
	}
	if len(identity.PriorityHeaderNames) == 0 && len(identity.PriorityQueryNames) == 0 {
		identity.PriorityHeaderNames = common.CloneStringSlice(DefaultProviderPriorityHeaderNames)
		identity.PriorityQueryNames = common.CloneStringSlice(DefaultProviderPriorityQueryNames)
	}
}

// ---------------------------------------------------------------------------
// 内部：归一化辅助
// ---------------------------------------------------------------------------

func normalizeAccessPolicy(policy *AccessPolicy) {
	if policy == nil {
		return
	}
	policy.Keys = common.UniqueNonEmptyStrings(policy.Keys, false)
	policy.HeaderNames = common.NormalizeHeaderNames(policy.HeaderNames)
	policy.QueryNames = common.NormalizeQueryNames(policy.QueryNames)
}

func normalizeCORSConfig(cfg *CORSConfig) {
	if cfg == nil {
		return
	}
	cfg.AllowedOrigins = common.UniqueNonEmptyStrings(cfg.AllowedOrigins, false)
	cfg.AllowedMethods = normalizeHTTPMethods(cfg.AllowedMethods)
	cfg.AllowedHeaders = common.NormalizeHeaderNames(cfg.AllowedHeaders)
	cfg.ExposeHeaders = common.NormalizeHeaderNames(cfg.ExposeHeaders)
}

func normalizeProviderIdentityConfig(identity *ProviderIdentityConfig) {
	if identity == nil {
		return
	}
	identity.IDHeaderNames = common.NormalizeHeaderNames(identity.IDHeaderNames)
	identity.IDQueryNames = common.NormalizeQueryNames(identity.IDQueryNames)
	identity.LabelHeaderNames = common.NormalizeHeaderNames(identity.LabelHeaderNames)
	identity.LabelQueryNames = common.NormalizeQueryNames(identity.LabelQueryNames)
	identity.TagsHeaderNames = common.NormalizeHeaderNames(identity.TagsHeaderNames)
	identity.TagsQueryNames = common.NormalizeQueryNames(identity.TagsQueryNames)
	identity.PriorityHeaderNames = common.NormalizeHeaderNames(identity.PriorityHeaderNames)
	identity.PriorityQueryNames = common.NormalizeQueryNames(identity.PriorityQueryNames)
}

func normalizeModelEntries(entries []ModelEntry) []ModelEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]ModelEntry, 0, len(entries))
	for _, item := range entries {
		item.Name = normalizeModelKey(item.Name)
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		item.Description = strings.TrimSpace(item.Description)
		item.Version = strings.TrimSpace(item.Version)
		item.SupportedActions = normalizeStringSliceKeepCase(item.SupportedActions)
		item.SupportedGenerationMethods = normalizeStringSliceKeepCase(item.SupportedGenerationMethods)
		item.SupportedInputModalities = normalizeStringSliceKeepCase(item.SupportedInputModalities)
		item.SupportedOutputModalities = normalizeStringSliceKeepCase(item.SupportedOutputModalities)
		item.Thinking = normalizeModelThinkingConfig(item.Thinking)
		out = append(out, item)
	}
	return out
}

func normalizeModelThinkingConfig(in *ModelThinkingConfig) *ModelThinkingConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Levels = normalizeLowercaseSlice(in.Levels)
	return &out
}

func normalizeModelAliases(in []ModelAlias) []ModelAlias {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelAlias, 0, len(in))
	seen := make(map[string]struct{}, len(in))

	for _, item := range in {
		item.Alias = normalizeModelKey(item.Alias)
		item.Target = normalizeModelKey(item.Target)

		if item.Alias == "" || item.Target == "" {
			out = append(out, item)
			continue
		}
		key := item.Alias + "->" + item.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeHTTPMethods(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		method := strings.ToUpper(strings.TrimSpace(item))
		if method == "" {
			continue
		}
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		out = append(out, method)
	}
	return out
}

func normalizeStringSliceKeepCase(in []string) []string {
	return common.UniqueNonEmptyStrings(in, false)
}

func normalizeLowercaseSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// normalizeModelKey 将模型名归一化：去掉 models/ 前缀、去掉 thinking suffix、转小写。
func normalizeModelKey(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "models/") {
		model = model[len("models/"):]
	}
	if strings.HasSuffix(model, ")") {
		if idx := strings.LastIndex(model, "("); idx > 0 {
			model = model[:idx]
		}
	}
	return strings.ToLower(strings.TrimSpace(model))
}

func normalizeEnum(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// ---------------------------------------------------------------------------
// 内部：校验辅助
// ---------------------------------------------------------------------------

func validateAccessPolicy(v *validator, path string, policy AccessPolicy) {
	if !policy.Enabled {
		return
	}
	if len(policy.Keys) == 0 {
		v.add("%s.keys 不能为空", path)
	}
	if !policy.AllowBearer && len(policy.HeaderNames) == 0 && len(policy.QueryNames) == 0 {
		v.add("%s 至少需要一种认证来源（bearer / header / query）", path)
	}
}

func validateModelsConfig(v *validator, cfg ModelsConfig) {
	switch cfg.Source {
	case "embedded", "custom":
	default:
		v.add("models.source 只能是 embedded / custom")
		return
	}

	if cfg.Source == "embedded" && len(cfg.Entries) > 0 {
		v.add("models.entries 仅在 models.source=custom 时允许配置")
	}
	if cfg.Source == "custom" && len(cfg.Entries) == 0 {
		v.add("models.source=custom 时，models.entries 不能为空")
	}

	allowedActions := map[string]struct{}{
		"generateContent":       {},
		"streamGenerateContent": {},
		"countTokens":           {},
	}
	allowedThinkingLevels := map[string]struct{}{
		"minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
	}

	entryNames := make(map[string]struct{}, len(cfg.Entries))
	for i, entry := range cfg.Entries {
		if entry.Name == "" {
			v.add("models.entries[%d].name 不能为空", i)
		} else {
			if _, exists := entryNames[entry.Name]; exists {
				v.add("models.entries[%d].name 与其他模型重复：%s", i, entry.Name)
			}
			entryNames[entry.Name] = struct{}{}
		}

		if len(entry.SupportedActions) == 0 && len(entry.SupportedGenerationMethods) == 0 {
			v.add("models.entries[%d] 至少需要配置 supported-actions 或 supported-generation-methods", i)
		}
		for j, action := range entry.SupportedActions {
			if _, ok := allowedActions[action]; !ok {
				v.add("models.entries[%d].supported-actions[%d] 非法：%s", i, j, action)
			}
		}

		if entry.Thinking != nil {
			if entry.Thinking.Min < 0 {
				v.add("models.entries[%d].thinking.min 不能小于 0", i)
			}
			if entry.Thinking.Max < 0 {
				v.add("models.entries[%d].thinking.max 不能小于 0", i)
			}
			if entry.Thinking.Max > 0 && entry.Thinking.Min > entry.Thinking.Max {
				v.add("models.entries[%d].thinking.min 不能大于 thinking.max", i)
			}
			for j, level := range entry.Thinking.Levels {
				if _, ok := allowedThinkingLevels[level]; !ok {
					v.add("models.entries[%d].thinking.levels[%d] 非法：%s", i, j, level)
				}
			}
		}
	}

	// 校验 alias target 是否指向已知模型。
	knownTargets := buildKnownModelTargetSet(cfg)
	aliasSeen := make(map[string]struct{}, len(cfg.Aliases))
	for i, alias := range cfg.Aliases {
		if alias.Alias == "" {
			v.add("models.aliases[%d].alias 不能为空", i)
		}
		if alias.Target == "" {
			v.add("models.aliases[%d].target 不能为空", i)
		}
		if alias.Alias == alias.Target && alias.Alias != "" {
			v.add("models.aliases[%d] 的 alias 与 target 不能相同：%s", i, alias.Alias)
		}
		if alias.Alias != "" {
			if _, exists := aliasSeen[alias.Alias]; exists {
				v.add("models.aliases[%d].alias 重复：%s", i, alias.Alias)
			}
			aliasSeen[alias.Alias] = struct{}{}
		}
		if alias.Target != "" {
			if _, ok := knownTargets[alias.Target]; !ok {
				v.add("models.aliases[%d].target 未指向已知模型：%s", i, alias.Target)
			}
		}
	}
}

// buildKnownModelTargetSet 根据 source 构建已知模型名集合，用于 alias 校验。
func buildKnownModelTargetSet(cfg ModelsConfig) map[string]struct{} {
	out := make(map[string]struct{})

	switch cfg.Source {
	case "embedded":
		for _, item := range DefaultEmbeddedModelNames {
			item = normalizeModelKey(item)
			if item != "" {
				out[item] = struct{}{}
			}
		}
	case "custom":
		for _, entry := range cfg.Entries {
			if entry.Name != "" {
				out[entry.Name] = struct{}{}
			}
		}
	}

	return out
}

func validateDuration(v *validator, path, value string) {
	if strings.TrimSpace(value) == "" {
		v.add("%s 不能为空", path)
		return
	}
	if _, err := ParseFlexibleDuration(strings.TrimSpace(value)); err != nil {
		v.add("%s 不是合法 duration: %v", path, err)
	}
}