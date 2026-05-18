package access

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"aistudio-gemini-gateway/internal/common"
)

// StaticKeyProvider 是静态密钥鉴权提供者。
// 用于 HTTP API 与 WS 握手阶段的基础访问控制。
type StaticKeyProvider struct {
	name    string
	options ExtractionOptions
	keys    map[string]string // 原始密钥 -> 主体标识
}

// NewStaticKeyProvider 创建静态密钥鉴权提供者。
// 要求至少配置一个密钥和一种凭据提取来源。
func NewStaticKeyProvider(cfg StaticKeyProviderConfig) (*StaticKeyProvider, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "static-key"
	}

	normalizedKeys := common.UniqueNonEmptyStrings(cfg.Keys, false)
	if len(normalizedKeys) == 0 {
		return nil, fmt.Errorf("static key provider %q requires at least one key", name)
	}

	options := NormalizeExtractionOptions(cfg.Options)
	if !options.AllowBearer && len(options.HeaderNames) == 0 && len(options.QueryNames) == 0 {
		return nil, fmt.Errorf("static key provider %q has no extraction sources configured", name)
	}

	keyMap := make(map[string]string, len(normalizedKeys))
	for _, key := range normalizedKeys {
		keyMap[key] = "key:" + common.ShortFingerprint(key)
	}

	return &StaticKeyProvider{
		name:    name,
		options: options,
		keys:    keyMap,
	}, nil
}

// Identifier 返回提供者标识符。
func (p *StaticKeyProvider) Identifier() string {
	if p == nil {
		return ""
	}
	return p.name
}

// Authenticate 执行静态密钥鉴权。
// 提取凭据后与已知密钥列表比对，匹配成功则返回鉴权结果。
func (p *StaticKeyProvider) Authenticate(ctx context.Context, r *http.Request) (*Result, *AuthError) {
	_ = ctx

	if p == nil {
		return nil, NewNotHandledError()
	}

	cred, ok := ExtractCredential(r, p.options)
	if !ok {
		return nil, NewNoCredentialsError()
	}

	principal, exists := p.keys[cred.Value]
	if !exists {
		return nil, NewInvalidCredentialError()
	}

	return &Result{
		Provider:         p.name,
		Principal:        principal,
		CredentialSource: cred.Source,
		CredentialName:   cred.Name,
		Metadata: map[string]string{
			"credential_fingerprint": common.ShortFingerprint(cred.Value),
			"credential_source":      cred.Source,
			"credential_name":        cred.Name,
		},
	}, nil
}
