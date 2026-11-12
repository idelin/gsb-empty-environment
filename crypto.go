package taskpkg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
)

// newDataKey 为租户生成 256 位数据加密密钥。
//
// 在真实宿主里密钥通常来自 KMS / HSM；这里使用 crypto/rand 生成，
// 保证测试路径使用的是真实密钥材料而非确定性占位值。
func newDataKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// seal 使用 AES-256-GCM 加密小对象明文。
// nonce 每次随机生成并前置在密文之前返回。
func seal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// open 是 seal 的逆操作，失败时返回 GCM 认证错误。
func open(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, ErrInvalidTicket
	}
	nonce, ciphertext := sealed[:ns], sealed[ns:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
