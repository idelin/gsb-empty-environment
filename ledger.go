// Package auditledger 实现单机离散时间的不可变只追加事务审计账簿。
//
// 核心性质：
//   - 每条事务构成一个区块，包含前序区块哈希（哈希链）、业务有效载荷和签名回执；
//   - 局部撤销通过追加逆向冲正条目完成，历史区块永不被物理覆写；
//   - 任意历史版本（前 n 个区块）均可生成默克尔路径证明。
package auditledger

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// TxType 标识事务类型。
type TxType string

const (
	// TxTransfer 转账事务。
	TxTransfer TxType = "transfer"
	// TxReversal 冲正事务，逆向抵消一笔历史转账。
	TxReversal TxType = "reversal"
)

// Payload 是事务的业务有效载荷。
type Payload struct {
	Type     TxType `json:"type"`
	From     string `json:"from"`
	To       string `json:"to"`
	Amount   int64  `json:"amount"`
	Memo     string `json:"memo,omitempty"`
	Reverses uint64 `json:"reverses,omitempty"` // 冲正目标区块序号（仅 reversal 使用）
}

// Bytes 返回有效载荷的确定性序列化结果。
func (p Payload) Bytes() []byte {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err) // Payload 字段均可序列化，不会发生
	}
	return b
}

// Block 是账簿中的一条事务记录（区块）。
type Block struct {
	Index     uint64   `json:"index"`     // 离散逻辑序号，从 0 开始
	Time      uint64   `json:"time"`      // 离散逻辑时间戳，等于 Index
	PrevHash  [32]byte `json:"prev_hash"` // 前序区块哈希，形成哈希链
	Payload   Payload  `json:"payload"`   // 业务有效载荷
	Signature []byte   `json:"signature"` // 签名回执（ed25519）
	Hash      [32]byte `json:"hash"`      // 本区块哈希
}

// signingMessage 构造待签名消息：index || time || prevHash || payload。
func signingMessage(index, time uint64, prevHash [32]byte, payload []byte) []byte {
	buf := make([]byte, 0, 8+8+32+len(payload))
	buf = binary.BigEndian.AppendUint64(buf, index)
	buf = binary.BigEndian.AppendUint64(buf, time)
	buf = append(buf, prevHash[:]...)
	buf = append(buf, payload...)
	return buf
}

// computeHash 计算区块哈希：SHA-256("BLOCK" || 签名消息 || 签名)。
func computeHash(b Block) [32]byte {
	msg := signingMessage(b.Index, b.Time, b.PrevHash, b.Payload.Bytes())
	h := sha256.New()
	h.Write([]byte("BLOCK"))
	h.Write(msg)
	h.Write(b.Signature)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// VerifySignature 用给定公钥校验区块的签名回执。
func VerifySignature(pub ed25519.PublicKey, b Block) bool {
	msg := signingMessage(b.Index, b.Time, b.PrevHash, b.Payload.Bytes())
	return ed25519.Verify(pub, msg, b.Signature)
}

// Ledger 是不可变只追加的审计账簿。所有写操作只能追加，不能修改历史。
type Ledger struct {
	mu       sync.RWMutex
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	blocks   []Block
	balances map[string]int64
	reversed map[uint64]uint64 // 被冲正区块序号 -> 冲正区块序号
	roots    [][32]byte        // roots[i] = 前 i+1 个区块的默克尔根
}

// NewLedger 创建账簿。genesis 为账户初始余额（创世状态，不计入区块）。
func NewLedger(priv ed25519.PrivateKey, genesis map[string]int64) (*Ledger, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("auditledger: invalid private key")
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("auditledger: cannot derive public key")
	}
	balances := make(map[string]int64, len(genesis))
	for acc, amt := range genesis {
		balances[acc] = amt
	}
	return &Ledger{
		priv:     priv,
		pub:      pub,
		balances: balances,
		reversed: make(map[uint64]uint64),
	}, nil
}

// PublicKey 返回账簿签名公钥，供审计方校验签名回执。
func (l *Ledger) PublicKey() ed25519.PublicKey {
	return l.pub
}

// Len 返回当前区块总数。
func (l *Ledger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.blocks)
}

// GetBlock 返回第 index 个区块的副本（只读，无法借此修改账簿）。
func (l *Ledger) GetBlock(index uint64) (Block, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if index >= uint64(len(l.blocks)) {
		return Block{}, fmt.Errorf("auditledger: block %d out of range", index)
	}
	return l.blocks[index], nil
}

// Balance 返回账户当前余额。
func (l *Ledger) Balance(account string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[account]
}

// AppendTransfer 追加一笔转账事务。
func (l *Ledger) AppendTransfer(from, to string, amount int64, memo string) (Block, error) {
	return l.append(Payload{
		Type:   TxTransfer,
		From:   from,
		To:     to,
		Amount: amount,
		Memo:   memo,
	})
}

// AppendReversal 对历史区块 index 发起冲正，追加一条逆向冲正条目。
// 原区块保持原样，账簿只新增记录。
func (l *Ledger) AppendReversal(index uint64, memo string) (Block, error) {
	return l.append(Payload{
		Type:     TxReversal,
		Memo:     memo,
		Reverses: index,
	})
}

// validate 在追加前校验事务的业务合法性。调用方须持有写锁。
func (l *Ledger) validate(p Payload) error {
	switch p.Type {
	case TxTransfer:
		if p.Amount <= 0 {
			return fmt.Errorf("auditledger: transfer amount must be positive, got %d", p.Amount)
		}
		if p.From == p.To {
			return errors.New("auditledger: transfer from and to must differ")
		}
		if l.balances[p.From] < p.Amount {
			return fmt.Errorf("auditledger: insufficient funds in %q: have %d, need %d",
				p.From, l.balances[p.From], p.Amount)
		}
	case TxReversal:
		if p.Reverses >= uint64(len(l.blocks)) {
			return fmt.Errorf("auditledger: reversal target %d out of range", p.Reverses)
		}
		target := l.blocks[p.Reverses]
		if target.Payload.Type != TxTransfer {
			return fmt.Errorf("auditledger: block %d is %s, only transfer can be reversed",
				p.Reverses, target.Payload.Type)
		}
		if _, ok := l.reversed[p.Reverses]; ok {
			return fmt.Errorf("auditledger: block %d already reversed", p.Reverses)
		}
	default:
		return fmt.Errorf("auditledger: unknown tx type %q", p.Type)
	}
	return nil
}

// apply 将事务效果应用到总账余额。调用方须持有写锁。
func (l *Ledger) apply(p Payload) {
	switch p.Type {
	case TxTransfer:
		l.balances[p.From] -= p.Amount
		l.balances[p.To] += p.Amount
	case TxReversal:
		orig := l.blocks[p.Reverses].Payload
		l.balances[orig.From] += orig.Amount
		l.balances[orig.To] -= orig.Amount
		l.reversed[p.Reverses] = uint64(len(l.blocks)) // 新区块的序号
	}
}

// append 校验、签名、链接并追加一个新区块，同时更新总账与默克尔根。
func (l *Ledger) append(p Payload) (Block, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.validate(p); err != nil {
		return Block{}, err
	}

	index := uint64(len(l.blocks))
	var prevHash [32]byte
	if index > 0 {
		prevHash = l.blocks[index-1].Hash
	}

	sig := ed25519.Sign(l.priv, signingMessage(index, index, prevHash, p.Bytes()))
	blk := Block{
		Index:     index,
		Time:      index, // 离散逻辑时间
		PrevHash:  prevHash,
		Payload:   p,
		Signature: sig,
	}
	blk.Hash = computeHash(blk)

	l.blocks = append(l.blocks, blk)
	l.apply(p)

	leaves := make([][32]byte, len(l.blocks))
	for i, b := range l.blocks {
		leaves[i] = b.Hash
	}
	l.roots = append(l.roots, MerkleRoot(leaves))
	return blk, nil
}

// RootAt 返回历史版本 version（即前 version 个区块）的默克尔根。
// version 取值范围 [1, Len()]。
func (l *Ledger) RootAt(version int) ([32]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if version < 1 || version > len(l.roots) {
		return [32]byte{}, fmt.Errorf("auditledger: version %d out of range [1, %d]", version, len(l.roots))
	}
	return l.roots[version-1], nil
}

// Prove 为区块 index 生成当前最新版本下的默克尔路径证明。
func (l *Ledger) Prove(index uint64) (Proof, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.proveLocked(index, len(l.blocks))
}

// ProveAt 为区块 index 生成历史版本 version（前 version 个区块）下的默克尔路径证明。
func (l *Ledger) ProveAt(index uint64, version int) (Proof, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if version < 1 || version > len(l.blocks) {
		return Proof{}, fmt.Errorf("auditledger: version %d out of range [1, %d]", version, len(l.blocks))
	}
	return l.proveLocked(index, version)
}

func (l *Ledger) proveLocked(index uint64, version int) (Proof, error) {
	if index >= uint64(version) {
		return Proof{}, fmt.Errorf("auditledger: block %d not in version %d", index, version)
	}
	leaves := make([][32]byte, version)
	for i := 0; i < version; i++ {
		leaves[i] = l.blocks[i].Hash
	}
	return BuildProof(leaves, index)
}

// VerifyChain 从头重放整个账簿：校验哈希链、每个区块的哈希与签名回执。
func (l *Ledger) VerifyChain() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var prev [32]byte
	for i, b := range l.blocks {
		if b.Index != uint64(i) || b.Time != uint64(i) {
			return fmt.Errorf("auditledger: block %d has bad index/time (%d/%d)", i, b.Index, b.Time)
		}
		if b.PrevHash != prev {
			return fmt.Errorf("auditledger: block %d breaks hash chain", i)
		}
		if computeHash(b) != b.Hash {
			return fmt.Errorf("auditledger: block %d hash mismatch", i)
		}
		if !VerifySignature(l.pub, b) {
			return fmt.Errorf("auditledger: block %d bad signature", i)
		}
		prev = b.Hash
	}
	return nil
}

// Equal 判断两个区块是否逐字节相同，用于审计历史是否被改动。
func (b Block) Equal(o Block) bool {
	return b.Index == o.Index &&
		b.Time == o.Time &&
		b.PrevHash == o.PrevHash &&
		b.Payload == o.Payload &&
		bytes.Equal(b.Signature, o.Signature) &&
		b.Hash == o.Hash
}
