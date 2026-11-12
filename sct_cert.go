package netsec

import (
	"crypto/x509"
	"encoding/asn1"
)

// oidEmbeddedSCT 是证书内嵌 SCT 列表扩展的 OID（1.3.6.1.4.1.11129.2.4.2）。
var oidEmbeddedSCT = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// EmbeddedSCTs 从证书的内嵌 SCT 扩展中解析 SCT 列表。
// 证书不含该扩展时返回 (nil, nil)，由调用方决定是否视为策略失败。
func EmbeddedSCTs(cert *x509.Certificate) ([]*SCT, error) {
	if cert == nil {
		return nil, ErrMalformedSCTList
	}
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidEmbeddedSCT) {
			continue
		}
		// x509.Certificate.Extensions 中的 Value 已剥掉外层 OCTET STRING，
		// 直接就是 SerializedSCTList。
		return ParseSCTList(ext.Value)
	}
	return nil, nil
}
