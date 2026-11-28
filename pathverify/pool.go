package pathverify

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// CertPool 是证书容器，既可承载信任锚（Trust Anchors），
// 也可承载非信任中间证书池（Inter.g. TLS 对端下发的 intermediates）。
type CertPool struct {
	certs []*x509.Certificate
	byFP  map[string]*x509.Certificate
}

// NewCertPool 创建空证书池。
func NewCertPool() *CertPool {
	return &CertPool{byFP: make(map[string]*x509.Certificate)}
}

// AddCert 加入一张已解析证书，按 DER 指纹去重。
func (p *CertPool) AddCert(c *x509.Certificate) {
	if c == nil {
		return
	}
	fp := fingerprint(c)
	if _, exists := p.byFP[fp]; exists {
		return
	}
	p.byFP[fp] = c
	p.certs = append(p.certs, c)
}

// AddDER 加入一张 DER 编码证书。
func (p *CertPool) AddDER(b []byte) error {
	c, err := x509.ParseCertificate(b)
	if err != nil {
		return fmt.Errorf("pathverify: parse DER certificate: %w", err)
	}
	p.AddCert(c)
	return nil
}

// AddPEM 加入一个或多个 PEM 编码证书（CERTIFICATE 块），返回加入数量。
func (p *CertPool) AddPEM(b []byte) (int, error) {
	added := 0
	rest := b
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			if added == 0 {
				return 0, fmt.Errorf("pathverify: no PEM CERTIFICATE block found")
			}
			return added, nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if err := p.AddDER(block.Bytes); err != nil {
			return added, err
		}
		added++
	}
}

// Len 返回池内去重后的证书数量。
func (p *CertPool) Len() int { return len(p.certs) }

func (p *CertPool) get(fp string) (*x509.Certificate, bool) {
	c, ok := p.byFP[fp]
	return c, ok
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return string(sum[:])
}
