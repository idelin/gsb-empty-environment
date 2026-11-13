package stun

import (
	"crypto/hmac"
	"crypto/sha1"
)

// hmacSHA1 仅供测试构造合法报文时使用。
func hmacSHA1(key, data []byte) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
