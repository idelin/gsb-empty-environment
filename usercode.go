package netsec

import (
	"crypto/rand"
	"errors"
	"strings"
)

// UserCodeAlphabet 是 RFC 8628 user_code 允许使用的字符集，
// 去掉了元音与易混淆字符，共 20 个辅音字母。
const UserCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// UserCodeLength 是规范化后 user_code 的字符数。
const UserCodeLength = 8

// ErrMalformedUserCode 表示输入无法规范化为合法 user_code。
var ErrMalformedUserCode = errors.New("netsec: malformed user_code")

// GenerateUserCode 用加密安全随机数生成一个新的 user_code（无连字符的规范形式）。
// 采用拒绝采样避免取模偏差。
func GenerateUserCode() (string, error) {
	const alphabetSize = len(UserCodeAlphabet)
	const acceptLimit = 256 - (256 % alphabetSize) // 240
	out := make([]byte, 0, UserCodeLength)
	buf := make([]byte, UserCodeLength*2)
	for len(out) < UserCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= acceptLimit {
				continue
			}
			out = append(out, UserCodeAlphabet[int(b)%alphabetSize])
			if len(out) == UserCodeLength {
				break
			}
		}
	}
	return string(out), nil
}

// NormalizeUserCode 把用户输入规范化为大写、无连字符的形式。
// 输入中的连字符被忽略，大小写不敏感；其他字符或长度不符返回 ErrMalformedUserCode。
func NormalizeUserCode(input string) (string, error) {
	var b strings.Builder
	b.Grow(UserCodeLength)
	for _, r := range input {
		if r == '-' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if !strings.ContainsRune(UserCodeAlphabet, r) {
			return "", ErrMalformedUserCode
		}
		b.WriteRune(r)
	}
	if b.Len() != UserCodeLength {
		return "", ErrMalformedUserCode
	}
	return b.String(), nil
}

// FormatUserCode 把规范形式的 user_code 格式化为 XXXX-XXXX 的展示形式。
// 输入长度不合法时原样返回。
func FormatUserCode(code string) string {
	if len(code) != UserCodeLength {
		return code
	}
	return code[:4] + "-" + code[4:]
}
