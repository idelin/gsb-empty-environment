package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"time"
)

// oidSCTList 是证书内嵌 SCT 列表扩展的 OID（RFC 6962 第 3.3 节）：
// 1.3.6.1.4.1.11129.2.4.2。
var oidSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// 解析过程中的结构错误。
var (
	errMalformedSCTList     = errors.New("sctpolicy: malformed SCT list extension")
	errMalformedSCTEntry    = errors.New("sctpolicy: malformed serialized SCT entry")
	errMalformedSignedBytes = errors.New("sctpolicy: malformed DigitallySigned structure")
)

const (
	// sctV1LogIDLen 是 RFC 6962 规定的 log_id 长度（SHA-256，32 字节）。
	sctV1LogIDLen = 32
	// 固定部分：version(1) + log_id(32) + timestamp(8) + extensions_len(2)。
	sctFixedHeaderLen = 1 + sctV1LogIDLen + 8 + 2
)

// ExtractEmbeddedSCTs 从证书的 SCT 列表扩展中解析出全部 SCT。
// 扩展的外层是 OCTET STRING（pkix.Extension.Value），其内容为
// RFC 6962 定义的 SerializedSCTList：2 字节总长度后接若干
// “2 字节条目长度 + SerializedSCT”的串联。证书未携带该扩展时
// 返回空切片且无错误。
func ExtractEmbeddedSCTs(cert *x509.Certificate) ([]*SCT, error) {
	if cert == nil {
		return nil, errNoCertificate
	}
	raw := findExtensionValue(cert, oidSCTList)
	if raw == nil {
		return nil, nil
	}

	// 扩展值为 OCTET STRING 包裹的 SerializedSCTList。
	var serializedList []byte
	if _, err := asn1.Unmarshal(raw, &serializedList); err != nil {
		return nil, errMalformedSCTList
	}
	return parseSCTList(serializedList)
}

// findExtensionValue 在证书扩展中查找指定 OID 的原始 value；
// 同时检查 x509.Certificate 已解析与未解析两类扩展，避免遗漏。
func findExtensionValue(cert *x509.Certificate, oid asn1.ObjectIdentifier) []byte {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oid) {
			return ext.Value
		}
	}
	for _, ext := range cert.ExtraExtensions {
		if ext.Id.Equal(oid) {
			return ext.Value
		}
	}
	return nil
}

// parseSCTList 解析 SerializedSCTList 字节流。
func parseSCTList(data []byte) ([]*SCT, error) {
	if len(data) < 2 {
		return nil, errMalformedSCTList
	}
	totalLen := int(uint16(data[0])<<8 | uint16(data[1]))
	data = data[2:]
	if totalLen != len(data) {
		return nil, errMalformedSCTList
	}

	var scts []*SCT
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, errMalformedSCTList
		}
		entryLen := int(uint16(data[0])<<8 | uint16(data[1]))
		data = data[2:]
		if entryLen == 0 || entryLen > len(data) {
			return nil, errMalformedSCTList
		}
		sct, err := parseSCT(data[:entryLen])
		if err != nil {
			return nil, err
		}
		scts = append(scts, sct)
		data = data[entryLen:]
	}
	return scts, nil
}

// parseSCT 解析单个 SerializedSCT：
//
//	struct {
//	    Version sct_version;          // 1 字节
//	    LogID id;                     // 32 字节
//	    uint64 timestamp;             // 8 字节，毫秒
//	    CtExtensions extensions;      // 2 字节长度前缀
//	    DigitallySigned signature;
//	}
func parseSCT(entry []byte) (*SCT, error) {
	if len(entry) < sctFixedHeaderLen {
		return nil, errMalformedSCTEntry
	}
	pos := 0
	sct := &SCT{Version: entry[pos]}
	pos++

	sct.LogID = append([]byte(nil), entry[pos:pos+sctV1LogIDLen]...)
	pos += sctV1LogIDLen

	var timestampMillis uint64
	for _, b := range entry[pos : pos+8] {
		timestampMillis = timestampMillis<<8 | uint64(b)
	}
	pos += 8
	sct.Timestamp = time.UnixMilli(int64(timestampMillis)).UTC()

	extLen := int(uint16(entry[pos])<<8 | uint16(entry[pos+1]))
	pos += 2
	if extLen > len(entry)-pos {
		return nil, errMalformedSCTEntry
	}
	sct.Extensions = append([]byte(nil), entry[pos:pos+extLen]...)
	pos += extLen

	digitallySigned := entry[pos:]
	sigHash, sigAlgo, sig, err := parseDigitallySigned(digitallySigned)
	if err != nil {
		return nil, err
	}
	sct.DigitallySigned = append([]byte(nil), digitallySigned...)
	sct.SignatureHash = sigHash
	sct.SignatureAlgo = sigAlgo
	sct.Signature = sig
	return sct, nil
}

// parseDigitallySigned 解析 RFC 5246 DigitallySigned：
// SignatureAndHashAlgorithm(2 字节) + 2 字节签名长度 + 签名。
func parseDigitallySigned(data []byte) (hash, algo uint8, sig []byte, err error) {
	if len(data) < 4 {
		return 0, 0, nil, errMalformedSignedBytes
	}
	hash = data[0]
	algo = data[1]
	sigLen := int(uint16(data[2])<<8 | uint16(data[3]))
	if sigLen != len(data)-4 {
		return 0, 0, nil, errMalformedSignedBytes
	}
	return hash, algo, append([]byte(nil), data[4:]...), nil
}
