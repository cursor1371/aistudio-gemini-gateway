package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"aistudio-gemini-gateway/internal/common"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// 公开加载入口
// ---------------------------------------------------------------------------

// Load 从指定文件加载配置。
func Load(path string) (*Config, error) {
	return LoadOptional(path, false)
}

// LoadOptional 从指定文件加载配置。
// 当 optional=true 且文件不存在时，返回默认配置而不是错误。
func LoadOptional(path string, optional bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if optional && (os.IsNotExist(err) || errors.Is(err, syscall.EISDIR)) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	return LoadBytes(data)
}

// LoadReader 从 io.Reader 加载配置。
func LoadReader(r io.Reader) (*Config, error) {
	if r == nil {
		return DefaultConfig(), nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("读取配置数据失败: %w", err)
	}
	return LoadBytes(data)
}

// LoadBytes 从 YAML 字节加载配置。
// 启用 KnownFields(true) 严格校验，拒绝当前 schema 中未定义的字段。
func LoadBytes(data []byte) (*Config, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return DefaultConfig(), nil
	}

	cfg := DefaultConfig()

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 确保文件中只有一个 YAML 文档。
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("配置文件只允许包含一个 YAML 文档")
		}
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return Prepare(cfg)
}

// ---------------------------------------------------------------------------
// Prepare / Clone / Redact / Serialize
// ---------------------------------------------------------------------------

// Prepare 对外部传入配置做最终准备：克隆 -> 默认值 -> 归一化 -> 校验。
func Prepare(cfg *Config) (*Config, error) {
	if cfg == nil {
		return DefaultConfig(), nil
	}
	cloned := cfg.Clone()
	cloned.ApplyDefaults()
	cloned.Normalize()
	if err := cloned.Validate(); err != nil {
		return nil, err
	}
	return cloned, nil
}

// Clone 深拷贝配置。
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c

	out.Access.HTTP = cloneAccessPolicy(c.Access.HTTP)
	out.Access.CORS = cloneCORSConfig(c.Access.CORS)

	out.WebSocket.Auth = cloneAccessPolicy(c.WebSocket.Auth)
	out.WebSocket.Origin = cloneOriginPolicyConfig(c.WebSocket.Origin)
	out.WebSocket.ProviderIdentity = cloneProviderIdentityConfig(c.WebSocket.ProviderIdentity)

	out.Models = cloneModelsConfig(c.Models)
	out.Gemini.DefaultSafetySettings = cloneSafetySettings(c.Gemini.DefaultSafetySettings)

	return &out
}

// Redacted 返回脱敏后的配置副本（密钥部分被遮蔽）。
func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}
	out := c.Clone()
	out.Access.HTTP.Keys = redactStringSlice(out.Access.HTTP.Keys)
	out.WebSocket.Auth.Keys = redactStringSlice(out.WebSocket.Auth.Keys)
	return out
}

// ToYAML 序列化为 YAML。
func (c *Config) ToYAML() ([]byte, error) {
	if c == nil {
		return yaml.Marshal(DefaultConfig())
	}
	return yaml.Marshal(c)
}

// ToRedactedYAML 序列化脱敏后的配置为 YAML。
func (c *Config) ToRedactedYAML() ([]byte, error) {
	if c == nil {
		return yaml.Marshal(DefaultConfig().Redacted())
	}
	return yaml.Marshal(c.Redacted())
}

// ---------------------------------------------------------------------------
// 内部：深拷贝辅助
// ---------------------------------------------------------------------------

func cloneAccessPolicy(in AccessPolicy) AccessPolicy {
	out := in
	out.Keys = common.CloneStringSlice(in.Keys)
	out.HeaderNames = common.CloneStringSlice(in.HeaderNames)
	out.QueryNames = common.CloneStringSlice(in.QueryNames)
	return out
}

func cloneCORSConfig(in CORSConfig) CORSConfig {
	out := in
	out.AllowedOrigins = common.CloneStringSlice(in.AllowedOrigins)
	out.AllowedMethods = common.CloneStringSlice(in.AllowedMethods)
	out.AllowedHeaders = common.CloneStringSlice(in.AllowedHeaders)
	out.ExposeHeaders = common.CloneStringSlice(in.ExposeHeaders)
	return out
}

func cloneOriginPolicyConfig(in OriginPolicyConfig) OriginPolicyConfig {
	out := in
	out.AllowedOrigins = common.CloneStringSlice(in.AllowedOrigins)
	return out
}

func cloneProviderIdentityConfig(in ProviderIdentityConfig) ProviderIdentityConfig {
	out := in
	out.IDHeaderNames = common.CloneStringSlice(in.IDHeaderNames)
	out.IDQueryNames = common.CloneStringSlice(in.IDQueryNames)
	out.LabelHeaderNames = common.CloneStringSlice(in.LabelHeaderNames)
	out.LabelQueryNames = common.CloneStringSlice(in.LabelQueryNames)
	out.TagsHeaderNames = common.CloneStringSlice(in.TagsHeaderNames)
	out.TagsQueryNames = common.CloneStringSlice(in.TagsQueryNames)
	out.PriorityHeaderNames = common.CloneStringSlice(in.PriorityHeaderNames)
	out.PriorityQueryNames = common.CloneStringSlice(in.PriorityQueryNames)
	return out
}

func cloneModelsConfig(in ModelsConfig) ModelsConfig {
	out := in
	out.Entries = cloneModelEntries(in.Entries)
	out.Aliases = cloneModelAliases(in.Aliases)
	return out
}

func cloneModelEntries(in []ModelEntry) []ModelEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelEntry, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].SupportedActions = common.CloneStringSlice(in[i].SupportedActions)
		out[i].SupportedGenerationMethods = common.CloneStringSlice(in[i].SupportedGenerationMethods)
		out[i].SupportedInputModalities = common.CloneStringSlice(in[i].SupportedInputModalities)
		out[i].SupportedOutputModalities = common.CloneStringSlice(in[i].SupportedOutputModalities)
		out[i].Thinking = cloneModelThinkingConfig(in[i].Thinking)
	}
	return out
}

func cloneModelThinkingConfig(in *ModelThinkingConfig) *ModelThinkingConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Levels = common.CloneStringSlice(in.Levels)
	return &out
}

func cloneModelAliases(in []ModelAlias) []ModelAlias {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelAlias, len(in))
	copy(out, in)
	return out
}

func cloneSafetySettings(in []SafetySetting) []SafetySetting {
	if len(in) == 0 {
		return nil
	}
	out := make([]SafetySetting, len(in))
	copy(out, in)
	return out
}

func redactStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i := range in {
		out[i] = common.RedactString(in[i])
	}
	return out
}
