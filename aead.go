package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// RFC 8915 §5.1.3 注册的 AEAD 算法标识。
const (
	AEADAESGCM128 uint16 = 0x0001
	AEADAESGCM256 uint16 = 0x0002
)

// NewAEAD 按算法标识和密钥构造 AEAD。
// AES-128-GCM 需要 16 字节密钥，AES-256-GCM 需要 32 字节密钥。
func NewAEAD(algo uint16, key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKeySize, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedAEAD, err)
	}
	switch algo {
	case AEADAESGCM128:
		if len(key) != 16 {
			return nil, fmt.Errorf("%w: aes-128-gcm requires 16-octet key, got %d", ErrInvalidKeySize, len(key))
		}
	case AEADAESGCM256:
		if len(key) != 32 {
			return nil, fmt.Errorf("%w: aes-256-gcm requires 32-octet key, got %d", ErrInvalidKeySize, len(key))
		}
	default:
		return nil, fmt.Errorf("%w: algorithm id 0x%04x", ErrUnsupportedAEAD, algo)
	}
	return aead, nil
}
