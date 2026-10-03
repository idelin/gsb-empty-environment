// Package ledger 实现单机离散时间的不可变只追加事务审计账簿。
//
// 特性：
//   - 哈希链：每个区块引用前序区块哈希，任何篡改都会断链；
//   - 只追加：历史区块物理上不可覆写，撤销通过追加逆向冲正条目完成；
//   - 默克尔证明：每个版本（每次追加后）的根哈希被保存，
//     可为任意历史版本中的任意区块生成路径证明。
package ledger

import (
	"bytes"
	"errors"
	"fmt"
)

// Ledger 只追加审计账簿。
type Ledger struct {
	key      []byte           // 签名密钥（HMAC）
	blocks   []*Block         // 只追加的区块序列
	roots    [][32]byte       // roots[i] 为前 i+1 个区块的默克尔根
	balances map[string]int64 // 总账状态
	reversed map[int]bool     // 已被冲正的区块高度
}

// New 创建空账簿。key 用于生成签名回执。
func New(key []byte) *Ledger {
	return &Ledger{
		key:      key,
		balances: make(map[string]int64),
		reversed: make(map[int]bool),
	}
}

// Height 返回账簿当前高度（区块数量）。
func (l *Ledger) Height() int { return len(l.blocks) }

// Block 返回指定高度的区块（只读副本指针，账簿内部绝不修改历史区块）。
func (l *Ledger) Block(index int) (*Block, error) {
	if index < 0 || index >= len(l.blocks) {
		return nil, fmt.Errorf("ledger: block %d out of range", index)
	}
	cp := *l.blocks[index]
	return &cp, nil
}

// Balance 返回账户当前余额。
func (l *Ledger) Balance(account string) int64 { return l.balances[account] }

// Root 返回指定版本（前 version 个区块）的默克尔根。
func (l *Ledger) Root(version int) ([32]byte, error) {
	if version < 1 || version > len(l.blocks) {
		return [32]byte{}, fmt.Errorf("ledger: version %d out of range", version)
	}
	return l.roots[version-1], nil
}

// append 是唯一的写入入口：构造新区块并链接到链尾。
func (l *Ledger) append(tx Tx) *Block {
	var prev [32]byte
	if len(l.blocks) > 0 {
		prev = l.blocks[len(l.blocks)-1].Hash
	}
	sig := sign(l.key, tx)
	b := &Block{
		Index:     len(l.blocks),
		PrevHash:  prev,
		Payload:   tx,
		Signature: sig,
		Hash:      computeHash(len(l.blocks), prev, tx, sig),
	}
	l.blocks = append(l.blocks, b)
	// 记录新版本的默克尔根（历史根全部保留，可回溯任意版本）。
	leaves := make([][32]byte, len(l.blocks))
	for i, blk := range l.blocks {
		leaves[i] = blk.Hash
	}
	l.roots = append(l.roots, merkleRoot(leaves))
	return b
}

// Transfer 追加一笔转账事务并更新总账。
func (l *Ledger) Transfer(from, to string, amount int64) (*Block, error) {
	if amount <= 0 {
		return nil, errors.New("ledger: amount must be positive")
	}
	tx := Tx{Type: TxTransfer, From: from, To: to, Amount: amount, Ref: -1}
	b := l.append(tx)
	l.balances[from] -= amount
	l.balances[to] += amount
	return b, nil
}

// Reverse 对指定高度的转账事务发起局部撤销：
// 追加一条逆向冲正条目（金额为原值取负），原区块保持原样。
func (l *Ledger) Reverse(index int) (*Block, error) {
	if index < 0 || index >= len(l.blocks) {
		return nil, fmt.Errorf("ledger: block %d out of range", index)
	}
	orig := l.blocks[index]
	if orig.Payload.Type != TxTransfer {
		return nil, fmt.Errorf("ledger: block %d is not a transfer", index)
	}
	if l.reversed[index] {
		return nil, fmt.Errorf("ledger: block %d already reversed", index)
	}
	tx := Tx{
		Type:   TxReversal,
		From:   orig.Payload.To, // 逆向：原收款方退回
		To:     orig.Payload.From,
		Amount: -orig.Payload.Amount,
		Ref:    index,
	}
	b := l.append(tx)
	l.reversed[index] = true
	// 冲正更新总账：等效于撤销原转账。
	l.balances[orig.Payload.From] += orig.Payload.Amount
	l.balances[orig.Payload.To] -= orig.Payload.Amount
	return b, nil
}

// Prove 为指定区块生成针对某一历史版本（前 version 个区块）的默克尔路径证明。
// version 为 0 时表示当前最新版本。
func (l *Ledger) Prove(index, version int) (Proof, error) {
	if version == 0 {
		version = len(l.blocks)
	}
	if index < 0 || index >= version || version > len(l.blocks) {
		return Proof{}, fmt.Errorf("ledger: cannot prove block %d at version %d", index, version)
	}
	leaves := make([][32]byte, version)
	for i := 0; i < version; i++ {
		leaves[i] = l.blocks[i].Hash
	}
	return Proof{Index: index, Version: version, Path: buildProof(leaves, index)}, nil
}

// VerifyBlock 校验指定高度的区块在当前链中的完整性：
// 重算哈希、校验前序链接与签名回执。
func (l *Ledger) VerifyBlock(index int) bool {
	if index < 0 || index >= len(l.blocks) {
		return false
	}
	b := l.blocks[index]
	var prev [32]byte
	if index > 0 {
		prev = l.blocks[index-1].Hash
	}
	if b.PrevHash != prev {
		return false
	}
	if !bytes.Equal(b.Signature, sign(l.key, b.Payload)) {
		return false
	}
	return b.Hash == computeHash(b.Index, prev, b.Payload, b.Signature)
}

// VerifyChain 全量校验哈希链与每个版本的默克尔根。
func (l *Ledger) VerifyChain() bool {
	for i := range l.blocks {
		if !l.VerifyBlock(i) {
			return false
		}
	}
	leaves := make([][32]byte, 0, len(l.blocks))
	for i, b := range l.blocks {
		leaves = append(leaves, b.Hash)
		if merkleRoot(leaves) != l.roots[i] {
			return false
		}
	}
	return true
}
