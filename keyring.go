package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// envelopeVersion 是票据信封格式版本。
	envelopeVersion byte = 1
	// nonceSize 是 AES-GCM 随机数长度（标准 96 位 nonce）。
	nonceSize = 12
	// keyIDSize 是密钥标识长度。
	keyIDSize = 4
)

var (
	// ErrUnknownKey 表示票据由密钥环中不存在（或已过宽限期）的密钥签发。
	ErrUnknownKey = errors.New("netsec: ticket key not found or past grace period")
	// ErrMalformedTicket 表示票据结构无法解析。
	ErrMalformedTicket = errors.New("netsec: malformed ticket")
	// ErrTicketAuth 表示 AEAD 认证失败：密文、AAD（含租户绑定）或标签被篡改。
	ErrTicketAuth = errors.New("netsec: ticket authentication failed")
)

// ticketKey 是一把 AEAD 密钥及其生命周期信息。
type ticketKey struct {
	id        [keyIDSize]byte
	aead      cipher.AEAD
	createdAt uint64
	// retiredAt 为零表示密钥仍处于活跃状态；非零表示仅在宽限期内可解密。
	retiredAt uint64
}

// Keyring 管理票据加密密钥。
//
// 同一时刻只有一把活跃密钥用于签发新票据；轮换后旧密钥在宽限期内仍可
// 解密历史票据，超过宽限期即失效，从而保证旧票据无法无限期使用。
type Keyring struct {
	active      *ticketKey
	retired     []*ticketKey
	gracePeriod uint64
	now         func() uint64
}

// NewKeyring 创建密钥环。key 必须是 16、24 或 32 字节（对应 AES-128/192/256），
// gracePeriodSeconds 为旧密钥轮换后的解密宽限期。
func NewKeyring(key []byte, createdAt uint64, gracePeriodSeconds uint64) (*Keyring, error) {
	active, err := newTicketKey(key, createdAt)
	if err != nil {
		return nil, err
	}
	return &Keyring{
		active:      active,
		gracePeriod: gracePeriodSeconds,
		now:         unixNow,
	}, nil
}

func newTicketKey(key []byte, createdAt uint64) (*ticketKey, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid ticket key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: initialize AEAD: %w", err)
	}
	tk := &ticketKey{
		aead:      aead,
		createdAt: createdAt,
	}
	if _, err := rand.Read(tk.id[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate key id: %w", err)
	}
	return tk, nil
}

// ActiveID 返回当前活跃密钥的标识副本。
func (k *Keyring) ActiveID() [keyIDSize]byte {
	return k.active.id
}

// Rotate 用新密钥替换活跃密钥，旧密钥进入只读宽限期。
func (k *Keyring) Rotate(newKey []byte, at uint64) error {
	next, err := newTicketKey(newKey, at)
	if err != nil {
		return err
	}
	if next.id == k.active.id {
		return errors.New("netsec: rotated key id collided with active key")
	}
	k.active.retiredAt = at
	k.retired = append(k.retired, k.active)
	k.active = next
	k.evictExpired(at)
	return nil
}

// evictExpired 移除已超出宽限期的退役密钥。
func (k *Keyring) evictExpired(now uint64) {
	kept := k.retired[:0]
	for _, tk := range k.retired {
		if now < tk.retiredAt+k.gracePeriod {
			kept = append(kept, tk)
		}
	}
	k.retired = kept
}

func (k *Keyring) lookup(id [keyIDSize]byte, now uint64) (*ticketKey, error) {
	if k.active.id == id {
		return k.active, nil
	}
	for _, tk := range k.retired {
		if tk.id == id {
			if now >= tk.retiredAt+k.gracePeriod {
				return nil, ErrUnknownKey
			}
			return tk, nil
		}
	}
	return nil, ErrUnknownKey
}

// Seal 用活跃密钥以 AES-GCM 密封 plaintext，aad 作为附加认证数据绑定。
//
// 票据信封布局：
//
//	version(1) | key_id(4) | nonce(12) | ciphertext || gcm_tag
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate nonce: %w", err)
	}

	ticket := make([]byte, 0, 1+keyIDSize+nonceSize+len(plaintext)+k.active.aead.Overhead())
	ticket = append(ticket, envelopeVersion)
	ticket = append(ticket, k.active.id[:]...)
	ticket = append(ticket, nonce...)
	ticket = k.active.aead.Seal(ticket, nonce, plaintext, aad)
	return ticket, nil
}

// Open 解析信封并在密钥环中查找对应密钥，验证 AAD 后解密。
func (k *Keyring) Open(ticket, aad []byte) ([]byte, error) {
	if len(ticket) < 1+keyIDSize+nonceSize+1 {
		return nil, ErrMalformedTicket
	}
	if ticket[0] != envelopeVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedTicket, ticket[0])
	}

	var id [keyIDSize]byte
	copy(id[:], ticket[1:1+keyIDSize])
	nonce := ticket[1+keyIDSize : 1+keyIDSize+nonceSize]
	ciphertext := ticket[1+keyIDSize+nonceSize:]

	key, err := k.lookup(id, k.now())
	if err != nil {
		return nil, err
	}
	plaintext, err := key.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTicketAuth, err)
	}
	return plaintext, nil
}

// additionalData 构造与租户和密钥标识绑定的 AAD，实现域分离与跨租户防护。
func additionalData(tenant string, keyID [keyIDSize]byte) []byte {
	aad := make([]byte, 0, len("netsec-ticket-v1")+1+8+len(tenant)+keyIDSize)
	aad = append(aad, "netsec-ticket-v1"...)
	aad = append(aad, 0)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tenant)))
	aad = append(aad, length[:]...)
	aad = append(aad, tenant...)
	aad = append(aad, keyID[:]...)
	return aad
}
