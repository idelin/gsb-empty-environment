package netsec

import "encoding/binary"

// MaxUncompressedLength 是 RFC 8879 允许的最大解压长度 (2^24-1)。
const MaxUncompressedLength = 1<<24 - 1

// 线上格式(RFC 8879 第 4 节 + 本题约定的字典扩展):
//
//	struct {
//	    CertificateCompressionAlgorithm algorithm;   // uint16
//	    uint24 uncompressed_length;
//	    uint24 dictionary_id;                         // 本题扩展, 0 = 无字典
//	    opaque compressed_certificate_message<1..2^24-1>;
//	} CompressedCertificate;
const compressedHeaderLen = 2 + 3 + 3 + 3

func appendUint24(b []byte, v uint32) []byte {
	return append(b, byte(v>>16), byte(v>>8), byte(v))
}

func readUint24(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}

// MarshalCompressedCertificate 序列化 CompressedCertificate。
func MarshalCompressedCertificate(alg uint16, dictID uint32, uncompressedLen uint32, body []byte) ([]byte, error) {
	if uncompressedLen > MaxUncompressedLength {
		return nil, alert(AlertInternalError, "uncompressed_length %d exceeds 2^24-1", uncompressedLen)
	}
	if dictID > 0xFFFFFF {
		return nil, alert(AlertInternalError, "dictionary ID %d exceeds uint24", dictID)
	}
	if len(body) == 0 || len(body) > MaxUncompressedLength {
		return nil, alert(AlertInternalError, "compressed body length %d out of range", len(body))
	}
	b := make([]byte, 0, compressedHeaderLen+len(body))
	b = binary.BigEndian.AppendUint16(b, alg)
	b = appendUint24(b, uncompressedLen)
	b = appendUint24(b, dictID)
	b = appendUint24(b, uint32(len(body)))
	return append(b, body...), nil
}

// ParseCompressedCertificate 解析 CompressedCertificate 线上格式。
func ParseCompressedCertificate(b []byte) (alg uint16, uncompressedLen uint32, dictID uint32, body []byte, err error) {
	if len(b) < compressedHeaderLen {
		return 0, 0, 0, nil, alert(AlertDecodeError, "CompressedCertificate too short: %d bytes", len(b))
	}
	alg = binary.BigEndian.Uint16(b[0:2])
	uncompressedLen = readUint24(b[2:5])
	dictID = readUint24(b[5:8])
	bodyLen := int(readUint24(b[8:11]))
	if bodyLen == 0 {
		return 0, 0, 0, nil, alert(AlertDecodeError, "empty compressed_certificate_message")
	}
	if len(b)-compressedHeaderLen != bodyLen {
		return 0, 0, 0, nil, alert(AlertDecodeError,
			"compressed body length mismatch: header %d, got %d", bodyLen, len(b)-compressedHeaderLen)
	}
	return alg, uncompressedLen, dictID, b[compressedHeaderLen:], nil
}
