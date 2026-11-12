package netsec

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// authenticatorNonceSize AES-GCM nonce 长度。
const authenticatorNonceSize = 12

// cipherOverheadSize AES-GCM 认证标签长度（16 字节）。
const cipherOverheadSize = 16

// Authenticator 是 NTS Authenticator 扩展字段的解析视图（RFC 8915 §5.1.4）。
type Authenticator struct {
	AEADID     uint16
	Nonce      []byte
	Ciphertext []byte // 含 AEAD 认证标签
}

// marshalAuthenticatorBody 编码 Authenticator 字段体：
// AEAD ID(2) || nonce 长度(2) || nonce || 密文长度(2) || 密文。
func marshalAuthenticatorBody(algo uint16, nonce, ciphertext []byte) []byte {
	body := make([]byte, 8+len(nonce)+len(ciphertext))
	binary.BigEndian.PutUint16(body[0:2], algo)
	binary.BigEndian.PutUint16(body[2:4], uint16(len(nonce)))
	copy(body[4:4+len(nonce)], nonce)
	binary.BigEndian.PutUint16(body[4+len(nonce):6+len(nonce)], uint16(len(ciphertext)))
	copy(body[6+len(nonce):], ciphertext)
	return body
}

// parseAuthenticator 解析 Authenticator 字段体并校验长度自洽。
func parseAuthenticator(body []byte) (Authenticator, error) {
	if len(body) < 8 {
		return Authenticator{}, ErrAuthenticatorMalformed
	}
	auth := Authenticator{
		AEADID: binary.BigEndian.Uint16(body[0:2]),
	}
	nonceLen := int(binary.BigEndian.Uint16(body[2:4]))
	if nonceLen == 0 || 4+nonceLen+2 > len(body) {
		return Authenticator{}, ErrAuthenticatorMalformed
	}
	auth.Nonce = body[4 : 4+nonceLen]
	cipherLen := int(binary.BigEndian.Uint16(body[4+nonceLen : 6+nonceLen]))
	// 扩展字段按 4 字节对齐，字段体尾部可能有零填充，只校验声明长度不越界。
	if 6+nonceLen+cipherLen > len(body) {
		return Authenticator{}, ErrAuthenticatorMalformed
	}
	auth.Ciphertext = body[6+nonceLen : 6+nonceLen+cipherLen]
	return auth, nil
}

// sealAuthenticator 用给定密钥对 Encrypted-Extension 区封签，
// 返回可直接放入报文的 Authenticator 扩展字段（含 4 字节扩展头）。
//
// aad 必须是完整 NTP 头加上 Authenticator 之前的全部扩展字段的原始字节，
// 这保证头部与前置扩展（含 UID、Cookie）任何比特被篡改都会导致校验失败。
func sealAuthenticator(aead cipher.AEAD, algo uint16, aad, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, authenticatorNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return MarshalExtension(ExtNTSAuthenticator, marshalAuthenticatorBody(algo, nonce, ciphertext)), nil
}

// openAuthenticator 用给定密钥验证并解密 Authenticator。
// 任何头部/前置扩展/密文篡改都会使 AEAD.Open 失败，返回 ErrAuthenticationFailed。
func openAuthenticator(aead cipher.AEAD, auth Authenticator, aad []byte) ([]byte, error) {
	if len(auth.Nonce) != authenticatorNonceSize {
		return nil, fmt.Errorf("%w: unexpected nonce length %d", ErrAuthenticationFailed, len(auth.Nonce))
	}
	plaintext, err := aead.Open(nil, auth.Nonce, auth.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthenticationFailed, err)
	}
	return plaintext, nil
}
