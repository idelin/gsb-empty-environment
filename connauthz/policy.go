package connauthz

import (
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
)

// certKey 以“签发者 DN + 序列号”唯一标识一张证书。
// X.509/CRL 等标准机制同样以该二元组定位证书。
func certKey(issuer []byte, serial *big.Int) string {
	return hex.EncodeToString(issuer) + "/" + serial.String()
}

// idOf 返回证书的撤销标识。
func idOf(c *x509.Certificate) string {
	return certKey(c.RawIssuer, c.SerialNumber)
}

// Policy 是不可变的一代授权策略：信任根、额外已知中间证书与撤销集。
//
// Policy 一旦由 PolicyBuilder.Build 生成即不可修改；轮换策略必须整体换入
// 新一代对象，这保证了并发开流读到的策略始终自洽。
type Policy struct {
	generation uint64
	roots      *x509.CertPool
	extraCerts []*x509.Certificate
	revoked    map[string]struct{}
}

// Generation 返回该策略的代次。
func (p *Policy) Generation() uint64 { return p.generation }

// IsRevoked 报告证书是否在本代撤销集中。
func (p *Policy) IsRevoked(c *x509.Certificate) bool {
	_, ok := p.revoked[idOf(c)]
	return ok
}

// verifyOptions 按本次对端出示的证书链组装 x509 验证参数。
// 对端链中的中间证书与策略预置的已知中间证书一并作为候选路径材料。
func (p *Policy) verifyOptions(peerChain []*x509.Certificate) x509.VerifyOptions {
	intermediates := x509.NewCertPool()
	for _, c := range p.extraCerts {
		intermediates.AddCert(c)
	}
	for _, c := range peerChain[1:] {
		intermediates.AddCert(c)
	}
	return x509.VerifyOptions{
		Roots:         p.roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
}

// PolicyBuilder 以累积方式构建不可变 Policy。
// Builder 自身可复用、可在 Build 后继续修改，不影响已生成的策略。
type PolicyBuilder struct {
	mu         sync.Mutex
	generation uint64
	roots      []*x509.Certificate
	extra      []*x509.Certificate
	revoked    map[string]struct{}
}

// NewPolicyBuilder 创建代次为 generation 的构建器。
func NewPolicyBuilder(generation uint64) *PolicyBuilder {
	return &PolicyBuilder{
		generation: generation,
		revoked:    make(map[string]struct{}),
	}
}

// AddRoot 加入一张信任根。
func (b *PolicyBuilder) AddRoot(c *x509.Certificate) *PolicyBuilder {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.roots = append(b.roots, c)
	return b
}

// AddIntermediate 加入一张额外已知中间证书（例如对端链中未携带的交叉证书）。
func (b *PolicyBuilder) AddIntermediate(c *x509.Certificate) *PolicyBuilder {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.extra = append(b.extra, c)
	return b
}

// Revoke 将证书加入撤销集。撤销按（签发者, 序列号）定位到具体证书，
// 因此交叉签名的同名证书不会被连带撤销。
func (b *PolicyBuilder) Revoke(c *x509.Certificate) *PolicyBuilder {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revoked[idOf(c)] = struct{}{}
	return b
}

// Build 生成不可变策略。generation 必须大于 0。
func (b *PolicyBuilder) Build() (*Policy, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.generation == 0 {
		return nil, fmt.Errorf("connauthz: policy generation must be >= 1")
	}
	roots := x509.NewCertPool()
	for _, c := range b.roots {
		roots.AddCert(c)
	}
	revoked := make(map[string]struct{}, len(b.revoked))
	for k := range b.revoked {
		revoked[k] = struct{}{}
	}
	return &Policy{
		generation: b.generation,
		roots:      roots,
		extraCerts: append([]*x509.Certificate(nil), b.extra...),
		revoked:    revoked,
	}, nil
}
