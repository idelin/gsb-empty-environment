package netsec

import (
	"errors"
	"strings"
)

// OPC (ECMA-376 Part 2 / ISO/IEC 29500-2) 部件名校验。
//
// 部件名是类 URI 的绝对路径：以 "/" 开头，以 "/" 分隔多个段，
// 每个段为非空、非点段、带扩展名的合法名称（根级 [Content_Types].xml
// 是唯一允许无扩展名的例外）。

const (
	// maxPartNameLength 是 OPC 规范给出的部件名长度上限。
	maxPartNameLength = 65535

	// maxSegmentLength 是单个段的长度上限。
	maxSegmentLength = 255
)

var (
	// ErrInvalidPartName 表示字符串不符合 OPC 部件名规则。
	ErrInvalidPartName = errors.New("opc: invalid part name")
	// ErrDuplicatePart 表示部件与已登记部件规范化后重名。
	ErrDuplicatePart = errors.New("opc: duplicate part name")
	// ErrEmptyPart 表示目标部件名集合中没有该部件。
	ErrEmptyPart = errors.New("opc: part not found")
)

// reservedDecoded 是任何段（解码后）都不允许出现的字符。
// 括号 "[" "]" 仅允许出现在根级 [Content_Types].xml 中，单独处理。
const reservedDecoded = `<>":*?|/\#` +
	"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" +
	"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f" +
	"\x7f"

// decodeSegment 对单个段做百分号解码并执行编码层面的校验。
//
// 返回解码后的段。任何格式错误的转义、NUL 字节，或编码后的路径分隔符
// （%2F、%5C）都会被拒绝——后者保证解码不会改变路径层级。
func decodeSegment(raw string) (string, error) {
	if !strings.ContainsRune(raw, '%') {
		return raw, nil
	}
	var builder strings.Builder
	builder.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch != '%' {
			builder.WriteByte(ch)
			continue
		}
		if i+2 >= len(raw) || !isHex(raw[i+1]) || !isHex(raw[i+2]) {
			return "", ErrInvalidPartName
		}
		decoded := fromHex(raw[i+1])<<4 | fromHex(raw[i+2])
		switch {
		case decoded == 0:
			return "", ErrInvalidPartName
		case decoded == '/' || decoded == '\\':
			return "", ErrInvalidPartName
		}
		builder.WriteByte(decoded)
		i += 2
	}
	return builder.String(), nil
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func fromHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

// validatePartName 按 OPC 规则校验部件名，返回解码后的规范化名称。
func validatePartName(name string) (string, error) {
	if name == "" || name[0] != '/' {
		return "", ErrInvalidPartName
	}
	if len(name) > maxPartNameLength {
		return "", ErrInvalidPartName
	}
	segments := strings.Split(name[1:], "/")
	decodedSegments := make([]string, 0, len(segments))
	for index, raw := range segments {
		last := index == len(segments)-1
		if raw == "" { // 空段（重复 "/"）或结尾 "/"
			return "", ErrInvalidPartName
		}
		if len(raw) > maxSegmentLength {
			return "", ErrInvalidPartName
		}
		// 根级 [Content_Types].xml 是规范显式允许的括号例外。
		if index == 0 && raw == "[Content_Types].xml" {
			decodedSegments = append(decodedSegments, raw)
			continue
		}
		decoded, err := decodeSegment(raw)
		if err != nil {
			return "", err
		}
		if decoded == "." || decoded == ".." {
			return "", ErrInvalidPartName
		}
		if strings.ContainsAny(decoded, reservedDecoded) ||
			strings.ContainsRune(decoded, '[') || strings.ContainsRune(decoded, ']') {
			return "", ErrInvalidPartName
		}
		if strings.HasSuffix(decoded, ".") || strings.HasSuffix(decoded, " ") {
			return "", ErrInvalidPartName
		}
		if last {
			dot := strings.LastIndexByte(decoded, '.')
			if dot < 0 || dot == len(decoded)-1 {
				return "", ErrInvalidPartName
			}
		}
		decodedSegments = append(decodedSegments, decoded)
	}
	return "/" + strings.Join(decodedSegments, "/"), nil
}

// canonicalKey 生成用于重复部件名检测的规范化键。
//
// 比较基于解码后的段，采用不区分大小写（ordinal ignore-case）的方式，
// 使大小写变体与百分号编码变体都无法在大小写不敏感的文件系统上相互覆盖。
func canonicalKey(decodedName string) string {
	return strings.ToLower(decodedName)
}
