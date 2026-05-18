package access

import (
	"context"
	"net/http"
)

// Provider 是统一鉴权提供者接口。
// 支持静态密钥、JWT、外部鉴权网关等多种实现。
type Provider interface {
	// Identifier 返回提供者标识符。
	Identifier() string

	// Authenticate 对请求执行鉴权。
	// 成功时返回 Result；失败时返回 AuthError。
	Authenticate(ctx context.Context, r *http.Request) (*Result, *AuthError)
}

// Result 是鉴权成功后的结果。
type Result struct {
	// Provider 是命中鉴权的提供者标识。
	Provider string

	// Principal 是鉴权主体标识，通常为密钥指纹。
	Principal string

	// CredentialSource 是凭据来源（bearer / header / query）。
	CredentialSource string

	// CredentialName 是凭据所在的字段名（如 Authorization / X-Api-Key / key）。
	CredentialName string

	// Metadata 是附加元数据。
	Metadata map[string]string
}

// Clone 深拷贝鉴权结果。
func (r *Result) Clone() *Result {
	if r == nil {
		return nil
	}
	out := *r
	if len(r.Metadata) > 0 {
		out.Metadata = make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			out.Metadata[k] = v
		}
	}
	return &out
}

// ExtractedCredential 是从 HTTP 请求中提取出的凭据。
type ExtractedCredential struct {
	// Value 是凭据原始值。
	Value string

	// Source 是凭据来源类型（authorization_bearer / header / query）。
	Source string

	// Name 是凭据所在的字段名。
	Name string
}

// ExtractionOptions 定义凭据提取行为。
type ExtractionOptions struct {
	// AllowBearer 是否允许从 Authorization: Bearer <key> 中提取。
	AllowBearer bool

	// HeaderNames 是额外的 Header 字段名列表。
	HeaderNames []string

	// QueryNames 是额外的 Query 参数名列表。
	QueryNames []string
}

// StaticKeyProviderConfig 是静态密钥鉴权提供者的配置。
type StaticKeyProviderConfig struct {
	// Name 是提供者标识符。
	Name string

	// Keys 是允许通过的密钥列表。
	Keys []string

	// Options 定义凭据提取行为。
	Options ExtractionOptions
}
