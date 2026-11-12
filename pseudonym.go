package netsec

import (
	"context"
	"crypto/subtle"
)

// Pseudonymizer 在给定密钥环上生成与验证伪名令牌。
//
// 它是无状态的：所有密钥状态保存在 Keyring 中，可安全并发使用，
// 供 Go 追踪宿主在采集、转发与换代流水线中共享。
type Pseudonymizer struct {
	keyring *Keyring
}

// NewPseudonymizer 基于密钥环创建伪名器。
func NewPseudonymizer(keyring *Keyring) *Pseudonymizer {
	if keyring == nil {
		keyring = NewKeyring()
	}
	return &Pseudonymizer{keyring: keyring}
}

// Keyring 返回底层密钥环，供换代流程注册新代次。
func (p *Pseudonymizer) Keyring() *Keyring {
	return p.keyring
}

// Pseudonymize 为 (租户, 字段类型, 原始秘密) 在指定代次派生稳定伪名。
// 相同输入与代次始终产生相同令牌；租户或字段不同则结果互不相关。
func (p *Pseudonymizer) Pseudonymize(generation uint32, tenantID, fieldType string, secret []byte) (Token, error) {
	generationKey, err := p.keyring.generationKey(generation)
	if err != nil {
		return Token{}, err
	}
	digest, err := hmacDigest(generationKey, tenantID, fieldType, secret)
	if err != nil {
		return Token{}, err
	}
	return Token{Generation: generation, Digest: digest}, nil
}

// Verify 以恒定时间校验令牌是否与输入三元组在其声明代次下匹配。
// 令牌代次未注册时返回 ErrUnknownGeneration，避免把旧代次令牌当作有效数据静默放行。
func (p *Pseudonymizer) Verify(token Token, tenantID, fieldType string, secret []byte) (bool, error) {
	generationKey, err := p.keyring.generationKey(token.Generation)
	if err != nil {
		return false, err
	}
	digest, err := hmacDigest(generationKey, tenantID, fieldType, secret)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(digest[:], token.Digest[:]) == 1, nil
}

// tenantContextKey 是租户 ID 在 context 中的私有键类型。
type tenantContextKey struct{}

// WithTenant 将租户 ID 注入上下文，追踪宿主可在跨服务调用边界透传。
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenantID)
}

// TenantFromContext 取回 WithTenant 注入的租户 ID；未注入时返回空串。
func TenantFromContext(ctx context.Context) string {
	tenantID, _ := ctx.Value(tenantContextKey{}).(string)
	return tenantID
}

// PseudonymizeFromContext 使用上下文中的租户 ID 派生伪名，
// 缺失租户时返回 ErrEmptyTenant，保证不会在无租户域的情况下生成可跨租户关联的标识。
func (p *Pseudonymizer) PseudonymizeFromContext(ctx context.Context, generation uint32, fieldType string, secret []byte) (Token, error) {
	return p.Pseudonymize(generation, TenantFromContext(ctx), fieldType, secret)
}
