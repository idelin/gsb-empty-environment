package netsec

import (
	"encoding/binary"
	"errors"
	"time"
)

// SCT 线格式解析错误，调用方可用 errors.Is 区分。
var (
	// ErrMalformedSCTList 表示 SCT 列表的外层长度或内容不合法。
	ErrMalformedSCTList = errors.New("sct policy: malformed SCT list")
	// ErrMalformedSCT 表示单份 SCT 的序列化内容不合法。
	ErrMalformedSCT = errors.New("sct policy: malformed SCT")
)

// ParseSCTList 解析 RFC 6962 第 3.3 节的 SerializedSCTList：
//
//	SerializedSCTList ::= vector<0..2^16-1> of SerializedSCT
//	SerializedSCT     ::= vector<0..2^16-1> of SignedCertificateTimestamp
//
// 证书 OID 1.3.6.1.4.1.11129.2.4.2 扩展的 extnValue OCTET STRING
// 内容，以及 TLS SignedCertificateTimestampList 握手消息的 payload，
// 都采用这一编码。
func ParseSCTList(data []byte) ([]*SCT, error) {
	if len(data) < 2 {
		return nil, ErrMalformedSCTList
	}
	listLen := int(binary.BigEndian.Uint16(data))
	if listLen != len(data)-2 {
		return nil, ErrMalformedSCTList
	}

	rest := data[2:]
	var scts []*SCT
	for len(rest) > 0 {
		if len(rest) < 2 {
			return nil, ErrMalformedSCTList
		}
		entryLen := int(binary.BigEndian.Uint16(rest))
		rest = rest[2:]
		if entryLen == 0 || entryLen > len(rest) {
			return nil, ErrMalformedSCTList
		}

		sct, err := ParseSCT(rest[:entryLen])
		if err != nil {
			return nil, err
		}
		scts = append(scts, sct)
		rest = rest[entryLen:]
	}
	return scts, nil
}

// ParseSCT 解析单份序列化的 SignedCertificateTimestamp：
//
//	struct {
//	    Version sct_version;
//	    LogID id;
//	    uint64 timestamp;
//	    CtExtensions extensions;
//	    digitally-signed struct { ... } signature;
//	} SignedCertificateTimestamp;
func ParseSCT(data []byte) (*SCT, error) {
	// version(1) + log_id(32) + timestamp(8) + extensions 长度(2) 是最小前缀。
	const minLen = 1 + 32 + 8 + 2
	if len(data) < minLen {
		return nil, ErrMalformedSCT
	}

	sct := &SCT{Version: SCTVersion(data[0])}
	off := 1
	copy(sct.LogID[:], data[off:off+32])
	off += 32

	sct.Timestamp = time.UnixMilli(int64(binary.BigEndian.Uint64(data[off : off+8]))).UTC()
	off += 8

	extLen := int(binary.BigEndian.Uint16(data[off : off+2]))
	off += 2
	if extLen > len(data)-off {
		return nil, ErrMalformedSCT
	}
	if extLen > 0 {
		sct.Extensions = append([]byte(nil), data[off:off+extLen]...)
	}
	off += extLen

	// DigitallySigned ::= struct {
	//     HashAlgorithm hash_algorithm;       // 1 字节
	//     SignatureAlgorithm sig_algorithm;  // 1 字节
	//     opaque signature<0..2^16-1>;       // u16 长度前缀 + 签名
	// }
	dsTail := data[off:]
	if len(dsTail) < 4 {
		return nil, ErrMalformedSCT
	}
	sigLen := int(binary.BigEndian.Uint16(dsTail[2:4]))
	if sigLen == 0 || 4+sigLen != len(dsTail) {
		return nil, ErrMalformedSCT
	}
	// 保留完整 DigitallySigned，供注入的签名验证器选择算法并验证。
	sct.Signature = append([]byte(nil), dsTail...)

	return sct, nil
}
