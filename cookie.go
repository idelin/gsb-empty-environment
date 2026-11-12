package netsec

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
)

// cookieVersion 是本实现不透明 Cookie 线上格式的版本号，便于将来升级。
const cookieVersion byte = 1

// cookieNonceSize AEAD nonce 长度（AES-GCM 标准 12 字节）。
const cookieNonceSize = 12

// CookieSecrets 是 NTS-KE 协商出的一对流量密钥。
// C2S 用于校验客户端请求方向的 Authenticator，S2C 用于签发响应方向的 Authenticator。
type CookieSecrets struct {
	AEAD uint16
	C2S  []byte
	S2C  []byte
}

// cookieKey 是密钥环中的单个对称密钥。
type cookieKey struct {
	id   uint16
	aead cipher.AEAD
}

// CookieKeyRing 管理 Cookie 保护密钥，支持轮换：
// 最新密钥签发新 Cookie，全部在环密钥都可解封历史 Cookie。
type CookieKeyRing struct {
	mu   sync.RWMutex
	keys []cookieKey
	next uint16
}

// NewCookieKeyRing 用初始保护密钥创建密钥环，密钥 ID 从 1 开始。
func NewCookieKeyRing(algo uint16, key []byte) (*CookieKeyRing, error) {
	ring := &CookieKeyRing{}
	if err := ring.Rotate(algo, key); err != nil {
		return nil, err
	}
	return ring, nil
}

// Rotate 加入一个新的保护密钥并成为当前签发密钥；旧密钥保留以解封存量 Cookie。
func (r *CookieKeyRing) Rotate(algo uint16, key []byte) error {
	aead, err := NewAEAD(algo, key)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	r.keys = append(r.keys, cookieKey{id: r.next, aead: aead})
	return nil
}

// GenerateSecrets 生成一对指定算法的随机流量密钥。
func GenerateSecrets(algo uint16) (CookieSecrets, error) {
	keySize, err := keySizeFor(algo)
	if err != nil {
		return CookieSecrets{}, err
	}
	c2s := make([]byte, keySize)
	s2c := make([]byte, keySize)
	if _, err := rand.Read(c2s); err != nil {
		return CookieSecrets{}, err
	}
	if _, err := rand.Read(s2c); err != nil {
		return CookieSecrets{}, err
	}
	return CookieSecrets{AEAD: algo, C2S: c2s, S2C: s2c}, nil
}

// Mint 把一次会话的流量密钥封进不透明 Cookie（RFC 8915 §5.2）。
//
// 线上格式：version(1) || keyID(2) || nonce(12) ||
//
//	AEAD_seal(plaintext=algo(2)||C2S||S2C, aad=version||keyID)
func (r *CookieKeyRing) Mint(secrets CookieSecrets) ([]byte, error) {
	if _, err := NewAEAD(secrets.AEAD, secrets.C2S); err != nil {
		return nil, err
	}
	if _, err := NewAEAD(secrets.AEAD, secrets.S2C); err != nil {
		return nil, err
	}
	r.mu.RLock()
	current := r.keys[len(r.keys)-1]
	r.mu.RUnlock()

	plaintext := make([]byte, 2+len(secrets.C2S)+len(secrets.S2C))
	binary.BigEndian.PutUint16(plaintext[0:2], secrets.AEAD)
	copy(plaintext[2:], secrets.C2S)
	copy(plaintext[2+len(secrets.C2S):], secrets.S2C)

	nonce := make([]byte, cookieNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	header := make([]byte, 3)
	header[0] = cookieVersion
	binary.BigEndian.PutUint16(header[1:3], current.id)

	ciphertext := current.aead.Seal(nil, nonce, plaintext, header)
	cookie := make([]byte, 0, len(header)+len(nonce)+len(ciphertext))
	cookie = append(cookie, header...)
	cookie = append(cookie, nonce...)
	cookie = append(cookie, ciphertext...)
	return cookie, nil
}

// Unwrap 尝试用密钥环中的任一密钥解开 Cookie，还原流量密钥。
func (r *CookieKeyRing) Unwrap(cookie []byte) (CookieSecrets, error) {
	if len(cookie) < 1+2+cookieNonceSize+cipherOverheadSize {
		return CookieSecrets{}, fmt.Errorf("%w: cookie truncated", ErrUnknownCookie)
	}
	if cookie[0] != cookieVersion {
		return CookieSecrets{}, fmt.Errorf("%w: unsupported cookie version %d", ErrUnknownCookie, cookie[0])
	}
	keyID := binary.BigEndian.Uint16(cookie[1:3])
	nonce := cookie[3 : 3+cookieNonceSize]
	ciphertext := cookie[3+cookieNonceSize:]
	header := cookie[0:3]

	r.mu.RLock()
	defer r.mu.RUnlock()
	var lastErr error
	for _, key := range r.keys {
		if key.id != keyID {
			continue
		}
		plaintext, err := key.aead.Open(nil, nonce, ciphertext, header)
		if err != nil {
			lastErr = err
			continue
		}
		return decodeSecrets(plaintext)
	}
	if lastErr != nil {
		return CookieSecrets{}, fmt.Errorf("%w: %v", ErrUnknownCookie, lastErr)
	}
	return CookieSecrets{}, fmt.Errorf("%w: key id %d not in ring", ErrUnknownCookie, keyID)
}

func decodeSecrets(plaintext []byte) (CookieSecrets, error) {
	if len(plaintext) < 4 || (len(plaintext)-2)%2 != 0 {
		return CookieSecrets{}, fmt.Errorf("%w: malformed cookie payload", ErrUnknownCookie)
	}
	algo := binary.BigEndian.Uint16(plaintext[0:2])
	keySize, err := keySizeFor(algo)
	if err != nil {
		return CookieSecrets{}, fmt.Errorf("%w: %v", ErrUnknownCookie, err)
	}
	if len(plaintext) != 2+2*keySize {
		return CookieSecrets{}, fmt.Errorf("%w: payload length %d does not match algorithm", ErrUnknownCookie, len(plaintext))
	}
	secrets := CookieSecrets{
		AEAD: algo,
		C2S:  append([]byte(nil), plaintext[2:2+keySize]...),
		S2C:  append([]byte(nil), plaintext[2+keySize:]...),
	}
	return secrets, nil
}

// keySizeFor 返回 AEAD 算法的对称密钥长度。
func keySizeFor(algo uint16) (int, error) {
	switch algo {
	case AEADAESGCM128:
		return 16, nil
	case AEADAESGCM256:
		return 32, nil
	default:
		return 0, fmt.Errorf("%w: algorithm id 0x%04x", ErrUnsupportedAEAD, algo)
	}
}
