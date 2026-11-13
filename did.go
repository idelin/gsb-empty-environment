package netsec

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// did:key 方法中 Ed25519 公钥的多码基前缀（varint 编码的 0xed），
// 后接 32 字节原始公钥，整体再做 multibase base58btc 之外的简化处理：
// 这里使用 multibase base64url（无填充）前缀 'u'，避免引入外部依赖。
const (
	didPrefix     = "did:key:"
	multibaseU    = "u"
	ed25519TagLen = 2
)

var ed25519Multicodec = []byte{0xed, 0x01}

// DIDFromPublicKey 把 Ed25519 公钥编码为 did:key 标识符。
func DIDFromPublicKey(key ed25519.PublicKey) string {
	if len(key) != ed25519.PublicKeySize {
		return ""
	}
	raw := make([]byte, 0, ed25519TagLen+len(key))
	raw = append(raw, ed25519Multicodec...)
	raw = append(raw, key...)
	return didPrefix + multibaseU + base64.RawURLEncoding.EncodeToString(raw)
}

// PublicKeyFromDID 从 did:key 标识符还原 Ed25519 公钥。
func PublicKeyFromDID(did string) (ed25519.PublicKey, error) {
	if !strings.HasPrefix(did, didPrefix+multibaseU) {
		return nil, fmt.Errorf("%w: unsupported DID method: %s", ErrMalformedToken, did)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(did, didPrefix+multibaseU))
	if err != nil {
		return nil, fmt.Errorf("%w: bad DID encoding: %v", ErrMalformedToken, err)
	}
	if len(raw) != ed25519TagLen+ed25519.PublicKeySize ||
		raw[0] != ed25519Multicodec[0] || raw[1] != ed25519Multicodec[1] {
		return nil, fmt.Errorf("%w: DID is not an Ed25519 did:key", ErrMalformedToken)
	}
	return ed25519.PublicKey(raw[ed25519TagLen:]), nil
}
