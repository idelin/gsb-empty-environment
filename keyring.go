package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
)

var (
	// ErrEmptyTenant 表示租户 ID 为空，伪名化必须绑定租户域。
	ErrEmptyTenant = errors.New("netsec: empty tenant id")
	// ErrEmptyFieldType 表示字段类型为空，伪名化必须绑定字段域。
	ErrEmptyFieldType = errors.New("netsec: empty field type")
	// ErrEmptySecret 表示原始秘密为空，空值不允许伪名化。
	ErrEmptySecret = errors.New("netsec: empty secret")
	// ErrInputTooLarge 表示租户、字段或秘密长度超出 uint32 长度前缀的编码范围。
	ErrInputTooLarge = errors.New("netsec: input too large for length-prefixed encoding")
	// ErrWeakKey 表示注册的根密钥不足 32 字节。
	ErrWeakKey = errors.New("netsec: root key must be at least 32 bytes")
	// ErrUnknownGeneration 表示密钥环中不存在请求的密钥代次。
	ErrUnknownGeneration = errors.New("netsec: unknown key generation")
)

// keyDomain 是根密钥 -> 代次密钥派生的域分隔标签，防止与其他用途混用。
var keyDomain = []byte("netsec/pseudonym/v1/hmac-sha256/key-generation")

// Keyring 保存各代次根密钥，并派生出代次专属 HMAC 密钥。
//
// 代次绑定通过两层实现：根密钥按代次派生出互不相同的 HMAC 密钥，
// 派生出的令牌同时在编码中携带代次。根密钥永不直接参与事件 HMAC，
// 也不会出现在映射表中。
type Keyring struct {
	mu   sync.RWMutex
	keys map[uint32][]byte
}

// NewKeyring 创建空密钥环。
func NewKeyring() *Keyring {
	return &Keyring{keys: make(map[uint32][]byte)}
}

// Add 注册一个密钥代次。根密钥会被复制保存，避免调用方后续切片修改影响密钥环。
func (k *Keyring) Add(generation uint32, rootKey []byte) error {
	if len(rootKey) < 32 {
		return ErrWeakKey
	}
	stored := make([]byte, len(rootKey))
	copy(stored, rootKey)

	k.mu.Lock()
	defer k.mu.Unlock()
	if k.keys == nil {
		k.keys = make(map[uint32][]byte)
	}
	k.keys[generation] = stored
	return nil
}

// Has 报告密钥环是否包含指定代次。
func (k *Keyring) Has(generation uint32) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	_, ok := k.keys[generation]
	return ok
}

// Generations 返回已注册的代次列表，顺序不保证，调用方排序后使用。
func (k *Keyring) Generations() []uint32 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]uint32, 0, len(k.keys))
	for gen := range k.keys {
		out = append(out, gen)
	}
	return out
}

// generationKey 按 HMAC(rootKey, "域标签" || uint32BE(代次)) 派生代次密钥。
func (k *Keyring) generationKey(generation uint32) ([]byte, error) {
	k.mu.RLock()
	rootKey, ok := k.keys[generation]
	k.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownGeneration
	}

	mac := hmac.New(sha256.New, rootKey)
	mac.Write(keyDomain)
	var genBuf [4]byte
	binary.BigEndian.PutUint32(genBuf[:], generation)
	mac.Write(genBuf[:])
	return mac.Sum(nil), nil
}
