package netsec

import (
	"crypto/sha1"
)

// nativePasswordResponse 计算 mysql_native_password 的认证响应：
// SHA1(password) XOR SHA1(scramble + SHA1(SHA1(password)))。
// 仅用于测试，证明带有 scramble 的认证响应只会在 TLS 通道上发送。
func nativePasswordResponse(password string, scramble []byte) []byte {
	hash := sha1.New()
	hash.Write([]byte(password))
	passSHA1 := hash.Sum(nil)

	hash = sha1.New()
	hash.Write(passSHA1)
	passSHA1SHA1 := hash.Sum(nil)

	hash = sha1.New()
	hash.Write(scramble)
	hash.Write(passSHA1SHA1)
	scrambleHash := hash.Sum(nil)

	resp := make([]byte, len(passSHA1))
	for i := range passSHA1 {
		resp[i] = passSHA1[i] ^ scrambleHash[i]
	}
	return resp
}
