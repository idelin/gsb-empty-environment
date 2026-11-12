package taskpkg

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Hash 是事件内容的 SHA-256 摘要，同时充当事件 ID。
type Hash [32]byte

// ZeroHash 表示“无前驱”。
var ZeroHash Hash

// NodeID 标识一个审计节点。
type NodeID string

// Kind 是事件类型。撤权与操作的先后判定与类型无关，
// 类型仅用于负载语义。
type Kind uint8

const (
	KindOperation Kind = iota + 1
	KindRevocation
	KindCheckpoint
)

// Event 是一条审计事件。除 Sig 外的所有字段都进入摘要与签名，
// 因此节点代次、严格序号、前事件摘要与跨节点引用均被密码学绑定。
// WallClock 仅用于展示，绝不参与排序判定。
type Event struct {
	Node      NodeID
	Epoch     uint64
	Seq       uint64
	Prev      Hash   // 同节点同代次上一事件的摘要；Seq==1 时必须是 ZeroHash
	Refs      []Hash // 本事件发生时已观察到的（跨节点/跨代次）事件
	Kind      Kind
	Payload   []byte
	WallClock int64 // 节点自报墙钟，仅显示用
	Sig       []byte
}

// encode 产出事件（不含签名）的规范二进制编码。
func (e *Event) encode() []byte {
	var buf []byte
	buf = appendString(buf, string(e.Node))
	buf = binary.AppendUvarint(buf, e.Epoch)
	buf = binary.AppendUvarint(buf, e.Seq)
	buf = append(buf, e.Prev[:]...)
	buf = binary.AppendUvarint(buf, uint64(len(e.Refs)))
	for _, r := range e.Refs {
		buf = append(buf, r[:]...)
	}
	buf = append(buf, byte(e.Kind))
	buf = appendBytes(buf, e.Payload)
	var clk [8]byte
	binary.BigEndian.PutUint64(clk[:], uint64(e.WallClock))
	buf = append(buf, clk[:]...)
	return buf
}

func appendString(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func appendBytes(b, p []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// Hash 返回事件内容摘要，即事件 ID。
func (e *Event) Hash() Hash {
	return sha256.Sum256(e.encode())
}

// Sign 用节点私钥对事件摘要签名。
func (e *Event) Sign(priv ed25519.PrivateKey) {
	h := e.Hash()
	e.Sig = ed25519.Sign(priv, h[:])
}

// VerifySignature 用给定公钥校验事件签名。
func (e *Event) VerifySignature(pub ed25519.PublicKey) bool {
	h := e.Hash()
	return ed25519.Verify(pub, h[:], e.Sig)
}

// deps 返回事件的全部因果依赖（前事件 + 引用）。
func (e *Event) deps() []Hash {
	out := make([]Hash, 0, len(e.Refs)+1)
	if e.Prev != ZeroHash {
		out = append(out, e.Prev)
	}
	out = append(out, e.Refs...)
	return out
}

// KeyRegistry 是宿主持有的可信密钥表：每个节点每个代次一把公钥。
// 代次滚动即换钥，旧代次密钥不能为新代次事件签名。
type KeyRegistry struct {
	keys map[NodeID]map[uint64]ed25519.PublicKey
}

func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{keys: make(map[NodeID]map[uint64]ed25519.PublicKey)}
}

func (r *KeyRegistry) Register(node NodeID, epoch uint64, pub ed25519.PublicKey) {
	m, ok := r.keys[node]
	if !ok {
		m = make(map[uint64]ed25519.PublicKey)
		r.keys[node] = m
	}
	m[epoch] = pub
}

func (r *KeyRegistry) lookup(node NodeID, epoch uint64) (ed25519.PublicKey, bool) {
	m, ok := r.keys[node]
	if !ok {
		return nil, false
	}
	k, ok := m[epoch]
	return k, ok
}

var (
	ErrUnknownKey   = errors.New("causal: no registered key for node/epoch")
	ErrBadSignature = errors.New("causal: invalid event signature")
	ErrBadPrev      = errors.New("causal: prev hash does not match strict sequence")
	ErrBadSeq       = errors.New("causal: seq 1 event must have zero prev")
	ErrCycle        = errors.New("causal: dependency cycle detected")
	ErrUnknownEvent = errors.New("causal: unknown event hash")
)

// DoubleSignEvidence 是双签证据：同一节点同一代次同一序号下，
// 两份内容不同但签名均有效的事件。
type DoubleSignEvidence struct {
	Node  NodeID
	Epoch uint64
	Seq   uint64
	A     *Event
	B     *Event
}

func (d *DoubleSignEvidence) Error() string {
	return fmt.Sprintf("causal: double-sign by %s at epoch %d seq %d", d.Node, d.Epoch, d.Seq)
}
