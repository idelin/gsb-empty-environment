package merkleanchor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
	"time"
)

// Anchor 是一次外部存证锚定记录：
// 锚定器将 (链标识, 单调序号, 时间戳, 默克尔根, 随机数) 打包签名，
// 形成“该根哈希在该时间戳之前已经存在”的密码学证据。
type Anchor struct {
	ChainID   string   // 业务链/系统标识，防止跨系统重放
	Seq       uint64   // 严格递增序号，防重放
	Timestamp int64    // 锚定时刻（Unix 秒）
	Root      [32]byte // 被锚定的默克尔根
	Nonce     [16]byte // 每次锚定的随机数，防重放
	Signature []byte   // 对上述全部字段的 ECDSA P-256 签名（r||s，64 字节）
}

// payload 返回被签名的规范化字节编码。
func (a *Anchor) payload() []byte {
	buf := make([]byte, 0, 2+len(a.ChainID)+8+8+32+16)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(a.ChainID)))
	buf = append(buf, l[:]...)
	buf = append(buf, a.ChainID...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], a.Seq)
	buf = append(buf, n[:]...)
	binary.BigEndian.PutUint64(n[:], uint64(a.Timestamp))
	buf = append(buf, n[:]...)
	buf = append(buf, a.Root[:]...)
	buf = append(buf, a.Nonce[:]...)
	return buf
}

// digest 返回待签名负载的 SHA-256 摘要。
func (a *Anchor) digest() [32]byte {
	return sha256.Sum256(a.payload())
}

// sign 用私钥对负载签名，输出固定的 r||s（各 32 字节）编码。
func signPayload(priv *ecdsa.PrivateKey, payload []byte) ([]byte, error) {
	digest := sha256.Sum256(payload)
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):], sb)
	return sig, nil
}

// verifyPayload 校验 r||s 编码的签名。
func verifyPayload(pub *ecdsa.PublicKey, payload, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	digest := sha256.Sum256(payload)
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, digest[:], r, s)
}

// Anchorer 是定时锚定器：持有签名私钥，按序号为默克尔根出具时间戳签名。
type Anchorer struct {
	priv    *ecdsa.PrivateKey
	chainID string
	seq     uint64
	// Now 返回当前 Unix 秒，可注入假时钟用于测试。
	Now func() int64
}

// NewAnchorer 生成密钥对并创建锚定器。chainID 标识本系统，隔离跨域重放。
func NewAnchorer(chainID string) (*Anchorer, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Anchorer{
		priv:    priv,
		chainID: chainID,
		Now:     func() int64 { return time.Now().Unix() },
	}, nil
}

// PublicKey 返回锚定器公钥，供验证方离线校验。
func (a *Anchorer) PublicKey() *ecdsa.PublicKey { return &a.priv.PublicKey }

// Anchor 在当前时刻对 root 进行锚定：打包时间戳并签名。
func (a *Anchorer) Anchor(root [32]byte) (*Anchor, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	a.seq++
	anchor := &Anchor{
		ChainID:   a.chainID,
		Seq:       a.seq,
		Timestamp: a.Now(),
		Root:      root,
		Nonce:     nonce,
	}
	sig, err := signPayload(a.priv, anchor.payload())
	if err != nil {
		return nil, err
	}
	anchor.Signature = sig
	return anchor, nil
}

// 验证错误。
var (
	ErrBadSignature = errors.New("anchor: signature invalid")
	ErrReplay       = errors.New("anchor: replay detected (seq or nonce already seen)")
	ErrFutureAnchor = errors.New("anchor: timestamp is in the future")
	ErrWrongChain   = errors.New("anchor: unexpected chain id")
)

// Verifier 校验锚定记录，并维护防重放状态。
// maxClockSkew 是允许的时间戳超前秒数（容忍时钟偏差）。
type Verifier struct {
	pub          *ecdsa.PublicKey
	chainID      string
	maxClockSkew int64
	lastSeq      uint64
	seenNonces   map[[16]byte]bool
}

// NewVerifier 用锚定器公钥构造验证器。
func NewVerifier(pub *ecdsa.PublicKey, chainID string, maxClockSkew int64) *Verifier {
	return &Verifier{
		pub:          pub,
		chainID:      chainID,
		maxClockSkew: maxClockSkew,
		seenNonces:   make(map[[16]byte]bool),
	}
}

// VerifyAnchor 校验锚定记录的签名、时间戳与防重放约束。
// now 为验证时刻（Unix 秒）。校验通过后记录序号与随机数，重放将被拒绝。
func (v *Verifier) VerifyAnchor(a *Anchor, now int64) error {
	if a.ChainID != v.chainID {
		return ErrWrongChain
	}
	if !verifyPayload(v.pub, a.payload(), a.Signature) {
		return ErrBadSignature
	}
	if a.Timestamp > now+v.maxClockSkew {
		return ErrFutureAnchor
	}
	if a.Seq <= v.lastSeq || v.seenNonces[a.Nonce] {
		return ErrReplay
	}
	v.lastSeq = a.Seq
	v.seenNonces[a.Nonce] = true
	return nil
}
