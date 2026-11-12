package netsec

import (
	"encoding/binary"
	"fmt"
)

// RFC 8915 §5.1.2 定义的 NTS 扩展字段类型号。
const (
	ExtUniqueIdentifier     uint16 = 0x0104 // RFC 8915 §5.1.2
	ExtNTSCookie            uint16 = 0x0204 // RFC 8915 §5.1.2
	ExtNTSAuthenticator     uint16 = 0x0404 // RFC 8915 §5.1.2
	ExtNTSCookiePlaceholder uint16 = 0x0304 // 仅 NTS-KE 使用，NTP 报文中不允许出现
)

const (
	// extHeaderSize 每个扩展字段 2 字节类型 + 2 字节总长（RFC 5905 §7.5）。
	extHeaderSize = 4
	// MinUniqueIdentifierSize RFC 8915 §5.1.2 要求 UID 至少 32 字节。
	MinUniqueIdentifierSize = 32
)

// Extension 表示一个已解析的 NTP 扩展字段。
//
// Body 保留线上原始字节（含填充），因此重新拼装响应或计算 AAD 时与对端
// 发出的字节完全一致；Value 去掉尾部零填充，便于长度校验与等值比较。
type Extension struct {
	Type  uint16
	Body  []byte
	Value []byte
}

// MarshalExtension 按 RFC 5905 §7.5 的线上格式编码扩展字段，
// 总长（含 4 字节头）向上填充到 4 字节倍数。
func MarshalExtension(extType uint16, body []byte) []byte {
	fieldLen := extHeaderSize + len(body)
	if rem := fieldLen % 4; rem != 0 {
		fieldLen += 4 - rem
	}
	out := make([]byte, fieldLen)
	binary.BigEndian.PutUint16(out[0:2], extType)
	binary.BigEndian.PutUint16(out[2:4], uint16(fieldLen))
	copy(out[extHeaderSize:], body)
	return out
}

// ParseExtensions 解析 48 字节 NTP 固定头之后的全部扩展字段。
// 要求扩展区按 4 字节对齐、每个字段长度自洽且无尾部残留。
func ParseExtensions(rest []byte) ([]Extension, error) {
	if len(rest)%4 != 0 {
		return nil, ErrBadAlignment
	}
	var exts []Extension
	for offset := 0; offset < len(rest); {
		if len(rest)-offset < extHeaderSize {
			return nil, ErrBadAlignment
		}
		fieldLen := int(binary.BigEndian.Uint16(rest[offset+2 : offset+4]))
		if fieldLen < extHeaderSize || fieldLen%4 != 0 || offset+fieldLen > len(rest) {
			return nil, fmt.Errorf("%w: field at offset %d", ErrBadAlignment, offset)
		}
		ext := Extension{
			Type: binary.BigEndian.Uint16(rest[offset : offset+2]),
			Body: append([]byte(nil), rest[offset+extHeaderSize:offset+fieldLen]...),
		}
		ext.Value = trimZeroPadding(ext.Body)
		exts = append(exts, ext)
		offset += fieldLen
	}
	return exts, nil
}

// trimZeroPadding 去掉 RFC 5905 §7.5 规定的全零尾部填充。
func trimZeroPadding(body []byte) []byte {
	end := len(body)
	for end > 0 && body[end-1] == 0 {
		end--
	}
	return body[:end]
}

// isKnownNTPExtension 校验扩展类型是否为 NTS NTP 报文允许的三种类型。
func isKnownNTPExtension(extType uint16) bool {
	switch extType {
	case ExtUniqueIdentifier, ExtNTSCookie, ExtNTSAuthenticator:
		return true
	default:
		return false
	}
}
