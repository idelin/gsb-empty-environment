package pathverify

import "crypto/x509"

// CertSummary 是证书在诊断信息中的轻量摘要，避免调用方依赖完整证书结构。
type CertSummary struct {
	Subject      string
	Issuer       string
	SerialNumber string
	SHA256       string
}

func summarize(c *x509.Certificate) *CertSummary {
	return &CertSummary{
		Subject:      c.Subject.String(),
		Issuer:       c.Issuer.String(),
		SerialNumber: c.SerialNumber.Text(16),
		SHA256:       fingerprint(c),
	}
}

func summarizeChain(chain []*x509.Certificate) []*CertSummary {
	out := make([]*CertSummary, 0, len(chain))
	for _, c := range chain {
		out = append(out, summarize(c))
	}
	return out
}
