package recovery

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// NonceSize 是 AES-GCM 标准 nonce 长度。
const NonceSize = 12

// ErrNonceReuse 表示试图以同一 (密钥代次, 公开计划, 槽号) 密封不同
// 事务摘要,即同一 nonce 密封另一内容。
var ErrNonceReuse = errors.New("recovery: 同一 nonce 不得密封另一内容")

// SealRequest 描述一次实际密封的绑定关系:密钥代次、公开计划、槽号与
// 事务摘要。四元组共同决定 nonce 与关联数据。
type SealRequest struct {
	KeyGen uint64   // 密钥代次
	Plan   string   // 公开计划标识
	Slot   uint64   // 槽号
	Digest [32]byte // 事务摘要
}

// AssociatedData 返回绑定密钥代次、公开计划、槽号与事务摘要的关联数据。
func (r SealRequest) AssociatedData() []byte {
	var tmp [8]byte
	buf := make([]byte, 0, 8+len(r.Plan)+8+len(r.Digest))
	binary.BigEndian.PutUint64(tmp[:], r.KeyGen)
	buf = append(buf, tmp[:]...)
	buf = append(buf, r.Plan...)
	binary.BigEndian.PutUint64(tmp[:], r.Slot)
	buf = append(buf, tmp[:]...)
	buf = append(buf, r.Digest[:]...)
	return buf
}

// nonce 由 (密钥代次, 公开计划, 槽号) 确定性导出;事务摘要只进入关联
// 数据,因此同一四元组配不同摘要会产生 nonce 复用,必须被 FrameStore
// 拒绝。
func (r SealRequest) nonce() [NonceSize]byte {
	h := sha256.New()
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], r.KeyGen)
	h.Write(tmp[:])
	h.Write([]byte(r.Plan))
	binary.BigEndian.PutUint64(tmp[:], r.Slot)
	h.Write(tmp[:])
	sum := h.Sum(nil)
	var n [NonceSize]byte
	copy(n[:], sum[:NonceSize])
	return n
}

// SealedFrame 是已密封帧及其持久回执内容。
type SealedFrame struct {
	Nonce      [NonceSize]byte
	AAD        []byte
	Ciphertext []byte
}

// Binder 执行实际 AEAD 密封:keyGen 选择密钥代次,nonce 与 aad 由
// SealRequest 派生。
type Binder func(keyGen uint64, nonce, plaintext, aad []byte) ([]byte, error)

type frameKey struct {
	keyGen uint64
	plan   string
	slot   uint64
}

type record struct {
	digest [32]byte
	frame  SealedFrame
}

// FrameStore 是已密封帧的持久回执库,强制执行密封纪律:
//   - 结果不明时重试同一 (密钥代次, 公开计划, 槽号, 事务摘要),
//     幂等取回同一已密封帧,不会再次调用 Binder;
//   - 同一 (密钥代次, 公开计划, 槽号) 配不同事务摘要,拒绝密封,
//     返回 ErrNonceReuse。
type FrameStore struct {
	mu     sync.Mutex
	frames map[frameKey]record
}

// NewFrameStore 返回空的持久回执库。
func NewFrameStore() *FrameStore {
	return &FrameStore{frames: map[frameKey]record{}}
}

// Seal 按密封纪律密封或取回帧。
func (s *FrameStore) Seal(req SealRequest, payload []byte, bind Binder) (SealedFrame, error) {
	key := frameKey{keyGen: req.KeyGen, plan: req.Plan, slot: req.Slot}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.frames[key]; ok {
		if rec.digest == req.Digest {
			return rec.frame, nil
		}
		return SealedFrame{}, ErrNonceReuse
	}
	nonce := req.nonce()
	ct, err := bind(req.KeyGen, nonce[:], payload, req.AssociatedData())
	if err != nil {
		return SealedFrame{}, err
	}
	f := SealedFrame{Nonce: nonce, AAD: req.AssociatedData(), Ciphertext: ct}
	s.frames[key] = record{digest: req.Digest, frame: f}
	return f, nil
}

// Retrieve 按 (密钥代次, 公开计划, 槽号) 取回已密封帧。
func (s *FrameStore) Retrieve(keyGen uint64, plan string, slot uint64) (SealedFrame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.frames[frameKey{keyGen: keyGen, plan: plan, slot: slot}]
	return rec.frame, ok
}

// NewGCMBinder 用每个密钥代次一把密钥构造 AES-GCM 绑定器。
func NewGCMBinder(keys map[uint64][]byte) (Binder, error) {
	aeads := map[uint64]cipher.AEAD{}
	for gen, key := range keys {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("recovery: 密钥代次 %d: %w", gen, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("recovery: 密钥代次 %d: %w", gen, err)
		}
		if aead.NonceSize() != NonceSize {
			return nil, fmt.Errorf("recovery: 密钥代次 %d 的 nonce 长度 %d 与 %d 不符", gen, aead.NonceSize(), NonceSize)
		}
		aeads[gen] = aead
	}
	return func(keyGen uint64, nonce, plaintext, aad []byte) ([]byte, error) {
		aead, ok := aeads[keyGen]
		if !ok {
			return nil, fmt.Errorf("recovery: 未知密钥代次 %d", keyGen)
		}
		return aead.Seal(nil, nonce, plaintext, aad), nil
	}, nil
}
