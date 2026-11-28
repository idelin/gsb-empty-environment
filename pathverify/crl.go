package pathverify

import (
	"crypto/x509"
	"time"
)

// verifyRevocation 对链上除信任锚外的每张证书查找签发者对应的 CRL：
//   - CRL 必须能由链中签发者公钥验签；
//   - 当前时间必须落在 ThisUpdate/NextUpdate 时间窗内；
//   - 证书序列号命中吊销列表即拒绝该候选链（回溯继续尝试其他链）。
func verifyRevocation(chain []*x509.Certificate, crls []*x509.RevocationList, now time.Time) *Error {
	crlByIssuer := make(map[string][]*x509.RevocationList)
	for _, crl := range crls {
		crlByIssuer[crl.Issuer.String()] = append(crlByIssuer[crl.Issuer.String()], crl)
	}

	for i := 1; i < len(chain); i++ {
		cert := chain[i]
		issuer := chain[i-1]
		for _, crl := range crlByIssuer[cert.Issuer.String()] {
			// 校验 CRL 自身签名（RevocationList 已解析，直接对其原始 TBS 验签）。
			if err := issuer.CheckSignature(crl.SignatureAlgorithm,
				crl.RawTBSRevocationList, crl.Signature); err != nil {
				return fail(ReasonCRL,
					"CRL issued by %q signature verification failed: %v",
					crl.Issuer.String(), err)
			}
			// 时间窗：ThisUpdate 之前或 NextUpdate 之后均视为无效 CRL。
			if now.Before(crl.ThisUpdate) {
				return fail(ReasonCRL,
					"CRL issued by %q is not yet effective (ThisUpdate=%s)",
					crl.Issuer.String(), crl.ThisUpdate.UTC().Format(time.RFC3339))
			}
			if !crl.NextUpdate.IsZero() && now.After(crl.NextUpdate) {
				return fail(ReasonCRL,
					"CRL issued by %q is expired (NextUpdate=%s)",
					crl.Issuer.String(), crl.NextUpdate.UTC().Format(time.RFC3339))
			}
			for _, rc := range crl.RevokedCertificates {
				if rc.SerialNumber.Cmp(cert.SerialNumber) == 0 {
					return fail(ReasonRevoked,
						"certificate %q serial %s is revoked by CRL of %q",
						cert.Subject.String(), cert.SerialNumber.Text(16),
						crl.Issuer.String())
				}
			}
		}
	}
	return nil
}
