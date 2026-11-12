package pseudonym

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
)

// Digester 为原始标识符生成用于身份确认的摘要。
//
// 短码只是摘要的前缀索引，真正的身份判定始终以完整摘要为准，
// 因此实现必须是确定性的，同一标识符在重启后必须得到相同摘要。
type Digester interface {
	Digest(identity string) []byte
}

// HMACDigester 使用 HMAC-SHA256 计算标识符摘要。
// 密钥只保存在服务端，使外部无法仅凭标识符反推或伪造伪名。
type HMACDigester struct {
	key []byte
}

// NewHMACDigester 返回一个基于 HMAC-SHA256 的摘要器。
// 调用方必须提供非空、高熵且持久化的密钥；密钥一旦启用就不能更换，
// 否则历史事件中的完整摘要将无法再确认身份。
func NewHMACDigester(key []byte) *HMACDigester {
	if len(key) == 0 {
		panic("pseudonym: HMAC key must not be empty")
	}
	return &HMACDigester{key: append([]byte(nil), key...)}
}

// Digest 实现 Digester。
func (d *HMACDigester) Digest(identity string) []byte {
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte(identity))
	return mac.Sum(nil)
}

// digestEncoding 是无填充、小写字母表的 base32 编码。
// 去掉填充后短码是纯字母表前缀，适合做索引且不泄露原始标识符。
var digestEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// encodeDigest 返回完整摘要的无填充 base32 表示。
func encodeDigest(digest []byte) string {
	return digestEncoding.EncodeToString(digest)
}

// maxCodeChars 是完整 SHA-256 摘要可提供的最长前缀长度。
// 32 字节经无填充 base32 编码后固定为 52 个字符。
const maxCodeChars = 52
