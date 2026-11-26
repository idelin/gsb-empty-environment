package policycheck

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Digest 是带算法前缀的内容摘要。
type Digest string

// String 返回摘要字符串。
func (d Digest) String() string { return string(d) }

// HashCanonical 对 JSON 规范化结果加入域分隔标签后计算 SHA-256。
func HashCanonical(domain string, value any) Digest {
	b, err := json.Marshal(value)
	if err != nil {
		b = []byte(err.Error())
	}
	h := sha256.New()
	_, _ = h.Write([]byte("policycheck/v1/" + domain + "\n"))
	_, _ = h.Write(b)
	sum := h.Sum(nil)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func hmacSHA256(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func equalMAC(a, b []byte) bool { return hmac.Equal(a, b) }
