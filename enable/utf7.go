package enable

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalidUTF7 表示修改版 UTF-7 编码非法。
var ErrInvalidUTF7 = errors.New("enable: invalid modified UTF-7 mailbox name")

// DecodeUTF7 按 RFC 3501 第 5.1.3 节解码修改版 UTF-7 邮箱名。
// '&' 起始移位序列，'-' 终止，"&-" 表示字面 '&'；
// 移位段为 Base64（',' 代替 '/'）编码的 UTF-16BE。
func DecodeUTF7(s string) (string, error) {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			if c < 0x20 || c > 0x7e {
				return "", fmt.Errorf("%w: byte %#x outside printable ASCII", ErrInvalidUTF7, c)
			}
			out.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '-')
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated shifted sequence", ErrInvalidUTF7)
		}
		seg := s[i+1 : i+end]
		if seg == "" {
			out.WriteByte('&')
			i += 2
			continue
		}
		decoded, err := decodeShifted(seg)
		if err != nil {
			return "", err
		}
		out.WriteString(decoded)
		i += end + 1
	}
	return out.String(), nil
}

func decodeShifted(seg string) (string, error) {
	b64 := strings.ReplaceAll(seg, ",", "/")
	if strings.ContainsAny(b64, "+=") {
		return "", fmt.Errorf("%w: bad base64 alphabet in shifted sequence", ErrInvalidUTF7)
	}
	if pad := len(b64) % 4; pad != 0 {
		b64 += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidUTF7, err)
	}
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("%w: odd byte count in UTF-16BE data", ErrInvalidUTF7)
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[2*i])<<8 | uint16(raw[2*i+1])
	}
	runes := utf16.Decode(u16)
	if !utf8.ValidString(string(runes)) {
		return "", fmt.Errorf("%w: invalid UTF-16 surrogate pair", ErrInvalidUTF7)
	}
	return string(runes), nil
}
