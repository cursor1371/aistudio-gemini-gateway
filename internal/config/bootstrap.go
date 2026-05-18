package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadWithEnvBootstrap 按“文件优先加载，再用环境变量覆盖”的策略加载配置。
// 默认行为：
// 1. 如果 config.yaml 已存在，则直接使用该文件启动
// 2. 如果 config.yaml 不存在，则尝试从环境变量渲染自生成 config.yaml
// 3. 如果环境变量和 config.yaml 同时存在，环境变量优先，以环境变量有的值覆盖 config.yaml 对应值
//
// 说明：
// - 若 config 文件不存在且环境变量也未提供任何支持项，则使用默认配置启动
// - 若需要写 config.yaml 但文件系统只读，则记录 stderr 警告，并继续使用内存配置启动
func LoadWithEnvBootstrap(path string, optional bool) (*Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "config.yaml"
	}

	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info != nil && !info.IsDir():
		cfg, err := Load(path)
		if err != nil {
			return nil, err
		}

		changed, applied, err := ApplyEnvOverrides(cfg)
		if err != nil {
			return nil, err
		}
		if changed {
			cfg, err = Prepare(cfg)
			if err != nil {
				return nil, err
			}
			bootstrapWarnf("config bootstrap: applied environment overrides on top of %q: %s", path, strings.Join(applied, ","))
		}
		return cfg, nil

	case statErr == nil && info != nil && info.IsDir():
		return nil, fmt.Errorf("配置路径 %q 是目录，不能作为配置文件使用", path)

	case errors.Is(statErr, os.ErrNotExist):
		cfg := DefaultConfig()

		changed, applied, err := ApplyEnvOverrides(cfg)
		if err != nil {
			return nil, err
		}

		cfg, err = Prepare(cfg)
		if err != nil {
			return nil, err
		}

		if changed {
			bootstrapWarnf("config bootstrap: config file %q not found, bootstrapping from environment: %s", path, strings.Join(applied, ","))
			if err := renderConfigFile(path, cfg); err != nil {
				bootstrapWarnf("config bootstrap: failed to write rendered config to %q: %v (continuing with in-memory config)", path, err)
			} else {
				bootstrapWarnf("config bootstrap: rendered config written to %q", path)
			}
			return cfg, nil
		}

		// 与默认行为保持一致：即使没有文件也允许启动。
		// optional 参数在这里保留接口兼容性，不再用于阻止启动。
		if !optional {
			bootstrapWarnf("config bootstrap: config file %q not found and no environment overrides detected, using default config", path)
		}
		return cfg, nil

	default:
		return nil, fmt.Errorf("读取配置文件失败: %w", statErr)
	}
}

func renderConfigFile(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	data, err := cfg.ToYAML()
	if err != nil {
		return err
	}

	// 配置中可能包含敏感 key，因此默认使用 0600。
	return os.WriteFile(path, data, 0o600)
}

func bootstrapWarnf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
}
