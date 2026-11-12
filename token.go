package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

// tokenMagic 是伪名令牌字符串的人类可读前缀，便于宿主侧识别与过滤。
const tokenMagic = "pn1"

// digestSize 是 HMAC-SHA256 输出长度。
const digestSize = sha256.Size

// maxUint32 是长度前缀字段可表达的最大长度。
const maxUint32 = int(^uint32(0))

var (
	// ErrMalformedToken 表示令牌字符串不符合伪名令牌编码格式。
	ErrMalformedToken = errors.New("netsec: malformed pseudonym token")
)

// Token 是一个不可逆向的稳定伪名。
//
// 线路编码为 "pn1." + base64url(版本|代次 uint32 BE|HMAC-SHA256 摘要)，
// 摘要对 (租户 ID, 字段类型, 原始秘密) 在指定密钥代次下进行域分隔 HMAC 计算。
// 代次随令牌自描述，宿主可据此选择验证密钥，且令牌本身不携带租户或字段明文。
type Token struct {
	Generation uint32
	Digest     [digestSize]byte
}

// String 返回令牌的稳定字符串形式，可作为事件键、引用键与映射表键。
func (t Token) String() string {
	raw := make([]byte, 1+4+digestSize)
	raw[0] = 1
	binary.BigEndian.PutUint32(raw[1:5], t.Generation)
	copy(raw[5:], t.Digest[:])
	return tokenMagic + "." + base64.RawURLEncoding.EncodeToString(raw)
}

// ParseToken 解析 ParseToken/Token.String 产生的令牌字符串。
func ParseToken(s string) (Token, error) {
	prefix := tokenMagic + "."
	if len(s) <= len(prefix) || s[:len(prefix)] != prefix {
		return Token{}, ErrMalformedToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(prefix):])
	if err != nil {
		return Token{}, ErrMalformedToken
	}
	if len(raw) != 1+4+digestSize || raw[0] != 1 {
		return Token{}, ErrMalformedToken
	}
	var token Token
	token.Generation = binary.BigEndian.Uint32(raw[1:5])
	copy(token.Digest[:], raw[5:])
	return token, nil
}

// Equal 以恒定时间比较两个令牌，避免宿主侧比对引入时序侧信道。
func (t Token) Equal(other Token) bool {
	if t.Generation != other.Generation {
		return false
	}
	return subtle.ConstantTimeCompare(t.Digest[:], other.Digest[:]) == 1
}

// encodeDomainMessage 将租户 ID、字段类型与原始秘密编码为无歧义的 HMAC 消息。
//
// 每个字段前置 uint32 大端长度，避免拼接歧义（例如租户 "ab" + 字段 "c" 与
// 租户 "a" + 字段 "bc" 产生相同拼接结果）。空输入直接报错，防止域退化。
func encodeDomainMessage(tenantID, fieldType string, secret []byte) ([]byte, error) {
	if tenantID == "" {
		return nil, ErrEmptyTenant
	}
	if fieldType == "" {
		return nil, ErrEmptyFieldType
	}
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}
	if len(tenantID) > maxUint32 || len(fieldType) > maxUint32 || len(secret) > maxUint32 {
		return nil, ErrInputTooLarge
	}
	msg := make([]byte, 0, 12+len(tenantID)+len(fieldType)+len(secret))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(tenantID)))
	msg = append(msg, lenBuf[:]...)
	msg = append(msg, tenantID...)
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(fieldType)))
	msg = append(msg, lenBuf[:]...)
	msg = append(msg, fieldType...)
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(secret)))
	msg = append(msg, lenBuf[:]...)
	msg = append(msg, secret...)
	return msg, nil
}

// hmacDigest 使用给定代次密钥计算域分隔 HMAC-SHA256 摘要。
func hmacDigest(generationKey []byte, tenantID, fieldType string, secret []byte) ([digestSize]byte, error) {
	msg, err := encodeDomainMessage(tenantID, fieldType, secret)
	if err != nil {
		return [digestSize]byte{}, err
	}
	mac := hmac.New(sha256.New, generationKey)
	mac.Write(msg)
	var digest [digestSize]byte
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}
