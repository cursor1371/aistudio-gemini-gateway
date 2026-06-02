package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const envPrefix = "AIGW_"

// ApplyEnvOverrides 将支持的环境变量覆盖到配置对象上。
// 返回值：
// 1. changed: 是否命中了任何支持的环境变量
// 2. applied: 实际命中的环境变量名列表（仅 key，不含 value）
// 3. err: 解析失败时返回错误
func ApplyEnvOverrides(cfg *Config) (changed bool, applied []string, err error) {
	if cfg == nil {
		return false, nil, fmt.Errorf("config is nil")
	}

	apply := func(name string, fn func(string) error) error {
		raw, ok := lookupEnv(name)
		if !ok {
			return nil
		}
		if err := fn(raw); err != nil {
			return fmt.Errorf("环境变量 %s 解析失败: %w", name, err)
		}
		changed = true
		applied = append(applied, name)
		return nil
	}

	// =========================
	// server
	// =========================

	if err := apply("SERVER_HOST", func(raw string) error {
		cfg.Server.Host = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("SERVER_PORT", func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		cfg.Server.Port = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("SERVER_READ_TIMEOUT", func(raw string) error {
		cfg.Server.ReadTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("SERVER_WRITE_TIMEOUT", func(raw string) error {
		cfg.Server.WriteTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("SERVER_IDLE_TIMEOUT", func(raw string) error {
		cfg.Server.IdleTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("SERVER_SHUTDOWN_TIMEOUT", func(raw string) error {
		cfg.Server.ShutdownTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("SERVER_TLS_ENABLED", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Server.TLS.Enabled = value
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("SERVER_TLS_CERT_FILE", func(raw string) error {
		cfg.Server.TLS.CertFile = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("SERVER_TLS_KEY_FILE", func(raw string) error {
		cfg.Server.TLS.KeyFile = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// access.http
	// =========================

	if err := apply("ACCESS_HTTP_ENABLED", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Access.HTTP.Enabled = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_HTTP_KEYS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.HTTP.Keys = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_HTTP_ALLOW_BEARER", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Access.HTTP.AllowBearer = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_HTTP_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.HTTP.HeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_HTTP_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.HTTP.QueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// access.cors
	// =========================

	if err := apply("ACCESS_CORS_ENABLED", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.Enabled = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_ALLOWED_ORIGINS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.AllowedOrigins = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_ALLOWED_METHODS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.AllowedMethods = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_ALLOWED_HEADERS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.AllowedHeaders = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_EXPOSE_HEADERS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.ExposeHeaders = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_ALLOW_CREDENTIALS", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.AllowCredentials = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ACCESS_CORS_MAX_AGE_SECONDS", func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		cfg.Access.CORS.MaxAgeSeconds = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// websocket
	// =========================

	if err := apply("WEBSOCKET_PATH", func(raw string) error {
		cfg.WebSocket.Path = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_HANDSHAKE_TIMEOUT", func(raw string) error {
		cfg.WebSocket.HandshakeTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("WEBSOCKET_READ_TIMEOUT", func(raw string) error {
		cfg.WebSocket.ReadTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("WEBSOCKET_WRITE_TIMEOUT", func(raw string) error {
		cfg.WebSocket.WriteTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_MAX_MESSAGE_SIZE", func(raw string) error {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		cfg.WebSocket.MaxMessageSize = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_HEARTBEAT_PING_INTERVAL", func(raw string) error {
		cfg.WebSocket.Heartbeat.PingInterval = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("WEBSOCKET_HEARTBEAT_PONG_TIMEOUT", func(raw string) error {
		cfg.WebSocket.Heartbeat.PongTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_ORIGIN_MODE", func(raw string) error {
		cfg.WebSocket.Origin.Mode = raw
		return nil
	}); err != nil {
		return false, nil, err
	}
	if err := apply("WEBSOCKET_ORIGIN_ALLOWED_ORIGINS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Origin.AllowedOrigins = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_AUTH_ENABLED", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Auth.Enabled = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_AUTH_KEYS", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Auth.Keys = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_AUTH_ALLOW_BEARER", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Auth.AllowBearer = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_AUTH_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Auth.HeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_AUTH_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.Auth.QueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_ID_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.IDHeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_ID_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.IDQueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_LABEL_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.LabelHeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_LABEL_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.LabelQueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_TAGS_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.TagsHeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_TAGS_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.TagsQueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_PRIORITY_HEADER_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.PriorityHeaderNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("WEBSOCKET_PROVIDER_IDENTITY_PRIORITY_QUERY_NAMES", func(raw string) error {
		values, err := parseStringSliceEnv(raw)
		if err != nil {
			return err
		}
		cfg.WebSocket.ProviderIdentity.PriorityQueryNames = values
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// routing
	// =========================

	if err := apply("ROUTING_STRATEGY", func(raw string) error {
		cfg.Routing.Strategy = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_SESSION_AFFINITY_TTL", func(raw string) error {
		cfg.Routing.SessionAffinityTTL = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_BOOTSTRAP_RETRIES", func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		cfg.Routing.BootstrapRetries = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_PROVIDER_COOLDOWN", func(raw string) error {
		cfg.Routing.ProviderCooldown = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_BOOTSTRAP_TIMEOUT", func(raw string) error {
		cfg.Routing.BootstrapTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_STREAM_IDLE_TIMEOUT", func(raw string) error {
		cfg.Routing.StreamIdleTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("ROUTING_NON_STREAM_TIMEOUT", func(raw string) error {
		cfg.Routing.NonStreamTimeout = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// models
	// =========================

	if err := apply("MODELS_SOURCE", func(raw string) error {
		cfg.Models.Source = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// gemini
	// =========================

	if err := apply("GEMINI_API_VERSION", func(raw string) error {
		cfg.Gemini.APIVersion = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("GEMINI_SAFETY_DEFAULTS_MODE", func(raw string) error {
		cfg.Gemini.SafetyDefaultsMode = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("GEMINI_IMAGE_PREVIEW_COMPATIBILITY", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Gemini.ImagePreviewCompatibility = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("GEMINI_THINKING_MODE", func(raw string) error {
		cfg.Gemini.Thinking.Mode = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("GEMINI_THINKING_STRICT_VALIDATION", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Gemini.Thinking.StrictValidation = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	// =========================
	// logging
	// =========================

	if err := apply("LOGGING_LEVEL", func(raw string) error {
		cfg.Logging.Level = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("LOGGING_FORMAT", func(raw string) error {
		cfg.Logging.Format = raw
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("LOGGING_ADD_SOURCE", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Logging.AddSource = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	if err := apply("LOGGING_ACCESS_LOG", func(raw string) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		cfg.Logging.AccessLog = value
		return nil
	}); err != nil {
		return false, nil, err
	}

	sort.Strings(applied)
	return changed, applied, nil
}

func lookupEnv(name string) (string, bool) {
	key := envPrefix + strings.TrimSpace(name)
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func parseStringSliceEnv(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// 先尝试 JSON 数组。
	if strings.HasPrefix(raw, "[") {
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err == nil {
			return normalizeEnvStringSlice(values), nil
		}
	}

	parts := strings.Split(raw, ",")
	return normalizeEnvStringSlice(parts), nil
}

func normalizeEnvStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}