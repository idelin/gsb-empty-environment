package ledger

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// TxType 事务类型：普通转账或逆向冲正。
type TxType int

const (
	TxTransfer TxType = iota
	TxReversal
)

// Tx 业务有效载荷。
type Tx struct {
	Type   TxType
	From   string
	To     string
	Amount int64 // 单位：分；冲正条目为原金额的相反数
	Ref    int   // 冲正条目指向的原区块高度；普通转账为 -1
}

// canonical 返回有效载荷的规范字节序列，用于签名与哈希。
func (t Tx) canonical() []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, byte(t.Type))
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(t.Amount))
	buf = append(buf, tmp[:]...)
	binary.BigEndian.PutUint64(tmp[:], uint64(int64(t.Ref)+1))
	buf = append(buf, tmp[:]...)
	buf = append(buf, t.From...)
	buf = append(buf, 0)
	buf = append(buf, t.To...)
	return buf
}

// Block 账簿区块：前序哈希 + 有效载荷 + 签名回执 + 自身哈希。
// 离散时间由逻辑时钟（区块高度）表示。
type Block struct {
	Index     int
	PrevHash  [32]byte
	Payload   Tx
	Signature []byte
	Hash      [32]byte
}

// sign 使用 HMAC-SHA256 生成签名回执。
func sign(key []byte, payload Tx) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload.canonical())
	return mac.Sum(nil)
}

// computeHash 计算区块哈希：SHA256(高度 ‖ 前序哈希 ‖ 载荷 ‖ 签名)。
func computeHash(index int, prev [32]byte, payload Tx, sig []byte) [32]byte {
	h := sha256.New()
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(index))
	h.Write(tmp[:])
	h.Write(prev[:])
	h.Write(payload.canonical())
	h.Write(sig)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func (b *Block) String() string {
	kind := "TRANSFER"
	if b.Payload.Type == TxReversal {
		kind = "REVERSAL"
	}
	return fmt.Sprintf("#%d %-8s %s -> %s  amount=%d ref=%d hash=%x",
		b.Index, kind, b.Payload.From, b.Payload.To,
		b.Payload.Amount, b.Payload.Ref, b.Hash[:8])
}

// Equal 判断两个区块内容是否完全一致（用于检测物理覆写）。
func (b *Block) Equal(o *Block) bool {
	if b == nil || o == nil {
		return b == o
	}
	return b.Index == o.Index &&
		b.PrevHash == o.PrevHash &&
		b.Payload == o.Payload &&
		bytes.Equal(b.Signature, o.Signature) &&
		b.Hash == o.Hash
}
