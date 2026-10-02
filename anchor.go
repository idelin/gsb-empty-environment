package anchor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// 锚定签名载荷：domain || seq(8) || timestamp(8) || prevHash(32) || merkleRoot(32)
var anchorDomain = []byte("MERKLE-ANCHOR-v1")

const sigLen = 64 // r(32) || s(32)，P-256

// Anchor 是一次外部存证锚定记录：某时刻某默克尔根已被签名固定。
type Anchor struct {
	Seq       uint64   // 单调递增序号（防重放）
	Timestamp int64    // 锚定时间戳（业务时钟，如 t=1000）
	PrevHash  [32]byte // 上一次锚定的哈希，构成哈希链（防重放/防乱序）
	Root      [32]byte // 被锚定的默克尔根
	Signature []byte   // 对 Payload() 的 ECDSA 签名
}

// Payload 返回被签名的规范化字节串。
func (a *Anchor) Payload() []byte {
	buf := make([]byte, 0, len(anchorDomain)+8+8+32+32)
	buf = append(buf, anchorDomain...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], a.Seq)
	buf = append(buf, tmp[:]...)
	binary.BigEndian.PutUint64(tmp[:], uint64(a.Timestamp))
	buf = append(buf, tmp[:]...)
	buf = append(buf, a.PrevHash[:]...)
	buf = append(buf, a.Root[:]...)
	return buf
}

// Hash 返回锚定记录自身的哈希，用于链接下一次锚定。
func (a *Anchor) Hash() [32]byte {
	return sha256.Sum256(a.Payload())
}

// Anchorer 定时将默克尔根与时间戳打包签名的锚定组件。
type Anchorer struct {
	key     *ecdsa.PrivateKey
	now     func() int64 // 可注入的时钟，便于测试与仿真
	seq     uint64
	prev    [32]byte
	started bool
}

// GenerateKey 生成锚定签名密钥（ECDSA P-256）。
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// NewAnchorer 创建锚定器。now 返回当前业务时间戳。
func NewAnchorer(key *ecdsa.PrivateKey, now func() int64) *Anchorer {
	return &Anchorer{key: key, now: now}
}

// PublicKey 返回锚定器公钥，供验证方离线验证。
func (a *Anchorer) PublicKey() *ecdsa.PublicKey {
	return &a.key.PublicKey
}

// AnchorRoot 将默克尔根与当前时间戳打包签名，产生一条锚定记录。
func (a *Anchorer) AnchorRoot(root [32]byte) (*Anchor, error) {
	a.seq++
	anc := &Anchor{
		Seq:       a.seq,
		Timestamp: a.now(),
		PrevHash:  a.prev,
		Root:      root,
	}
	sig, err := sign(a.key, anc.Payload())
	if err != nil {
		return nil, err
	}
	anc.Signature = sig
	a.prev = anc.Hash()
	a.started = true
	return anc, nil
}

func sign(key *ecdsa.PrivateKey, payload []byte) ([]byte, error) {
	digest := sha256.Sum256(payload)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}
	sig := make([]byte, sigLen)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)
	return sig, nil
}

// VerifySignature 验证锚定记录的签名是否由给定公钥签发。
func VerifySignature(pub *ecdsa.PublicKey, a *Anchor) bool {
	if len(a.Signature) != sigLen {
		return false
	}
	r := new(big.Int).SetBytes(a.Signature[:32])
	s := new(big.Int).SetBytes(a.Signature[32:])
	digest := sha256.Sum256(a.Payload())
	return ecdsa.Verify(pub, digest[:], r, s)
}

// Verifier 验证方：维护锚定链状态，拒绝重放、乱序与伪造的锚定。
type Verifier struct {
	pub     *ecdsa.PublicKey
	nextSeq uint64
	prev    [32]byte
	seen    map[[32]byte]bool
}

// NewVerifier 创建验证方，pub 为锚定器公钥。
func NewVerifier(pub *ecdsa.PublicKey) *Verifier {
	return &Verifier{pub: pub, nextSeq: 1, seen: make(map[[32]byte]bool)}
}

// RegisterAnchor 校验并登记一条锚定记录。
// 校验：签名有效、序号严格递增衔接、前向哈希链衔接、未曾登记（防重放）。
func (v *Verifier) RegisterAnchor(a *Anchor) error {
	if a == nil {
		return errors.New("nil anchor")
	}
	h := a.Hash()
	if v.seen[h] {
		return fmt.Errorf("replayed anchor (seq=%d)", a.Seq)
	}
	if a.Seq != v.nextSeq {
		return fmt.Errorf("anchor seq mismatch: got %d, want %d", a.Seq, v.nextSeq)
	}
	if a.PrevHash != v.prev {
		return fmt.Errorf("anchor chain broken at seq=%d", a.Seq)
	}
	if !VerifySignature(v.pub, a) {
		return fmt.Errorf("invalid anchor signature (seq=%d)", a.Seq)
	}
	v.seen[h] = true
	v.prev = h
	v.nextSeq++
	return nil
}
