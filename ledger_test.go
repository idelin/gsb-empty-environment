package auditledger

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"
)

func newTestLedger(t *testing.T, genesis map[string]int64) *Ledger {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	l, err := NewLedger(priv, genesis)
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	return l
}

// TestAcceptance 验收场景：
//  1. 写入 100 笔转账事务并生成哈希链；
//  2. 对第 50 笔发起冲正，账簿新增第 101 笔冲正记录并更新总账；
//  3. 原第 50 笔证据（区块内容/哈希）与默克尔路径证明依然有效。
func TestAcceptance(t *testing.T) {
	const (
		alice   = "alice"
		bob     = "bob"
		amount  = int64(100)
		txCount = 100
		// “第 50 笔”按 1 基计数，对应区块序号 49。
		targetIndex = uint64(49)
	)

	l := newTestLedger(t, map[string]int64{alice: 1_000_000, bob: 0})

	// 步骤 1：写入 100 笔转账事务。
	for i := 0; i < txCount; i++ {
		blk, err := l.AppendTransfer(alice, bob, amount, fmt.Sprintf("transfer #%d", i+1))
		if err != nil {
			t.Fatalf("append transfer %d: %v", i+1, err)
		}
		if blk.Index != uint64(i) {
			t.Fatalf("block index = %d, want %d", blk.Index, i)
		}
	}
	if got := l.Len(); got != txCount {
		t.Fatalf("len = %d, want %d", got, txCount)
	}

	// 校验哈希链：每个区块的 PrevHash 等于前一区块的 Hash。
	prev, err := l.GetBlock(0)
	if err != nil {
		t.Fatal(err)
	}
	if prev.PrevHash != ([32]byte{}) {
		t.Fatal("genesis block PrevHash must be zero")
	}
	for i := 1; i < txCount; i++ {
		cur, err := l.GetBlock(uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		if cur.PrevHash != prev.Hash {
			t.Fatalf("hash chain broken at block %d", i)
		}
		prev = cur
	}
	if err := l.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}

	// 记录冲正前第 50 笔的证据与版本 100 下的默克尔路径证明。
	before, err := l.GetBlock(targetIndex)
	if err != nil {
		t.Fatal(err)
	}
	proofV100, err := l.ProveAt(targetIndex, txCount)
	if err != nil {
		t.Fatalf("ProveAt(49, 100): %v", err)
	}
	rootV100, err := l.RootAt(txCount)
	if err != nil {
		t.Fatalf("RootAt(100): %v", err)
	}
	if !VerifyProof(proofV100, rootV100) {
		t.Fatal("proof for block 50 invalid at version 100 (before reversal)")
	}

	aliceBefore := l.Balance(alice)
	bobBefore := l.Balance(bob)
	if aliceBefore != 1_000_000-txCount*amount || bobBefore != txCount*amount {
		t.Fatalf("balances before reversal: alice=%d bob=%d", aliceBefore, bobBefore)
	}

	// 步骤 2：对第 50 笔发起冲正。
	rev, err := l.AppendReversal(targetIndex, "reverse transfer #50")
	if err != nil {
		t.Fatalf("AppendReversal: %v", err)
	}
	if got := l.Len(); got != txCount+1 {
		t.Fatalf("len after reversal = %d, want %d", got, txCount+1)
	}
	if rev.Index != txCount {
		t.Fatalf("reversal block index = %d, want %d", rev.Index, txCount)
	}
	if rev.Payload.Type != TxReversal || rev.Payload.Reverses != targetIndex {
		t.Fatalf("reversal payload = %+v", rev.Payload)
	}
	if rev.PrevHash != prev.Hash {
		t.Fatal("reversal block not linked to previous head")
	}

	// 总账已更新：第 50 笔的 100 元被退回。
	if got := l.Balance(alice); got != aliceBefore+amount {
		t.Fatalf("alice balance = %d, want %d", got, aliceBefore+amount)
	}
	if got := l.Balance(bob); got != bobBefore-amount {
		t.Fatalf("bob balance = %d, want %d", got, bobBefore-amount)
	}

	// 步骤 3：原第 50 笔未被覆写，证据与路径证明依然有效。
	after, err := l.GetBlock(targetIndex)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatal("block 50 was modified after reversal (history overwritten!)")
	}
	if !VerifyProof(proofV100, rootV100) {
		t.Fatal("historical proof for block 50 invalid after reversal")
	}

	// 最新版本（101 个区块）下也能为第 50 笔生成并验证证明。
	proofV101, err := l.Prove(targetIndex)
	if err != nil {
		t.Fatalf("Prove(49): %v", err)
	}
	rootV101, err := l.RootAt(txCount + 1)
	if err != nil {
		t.Fatalf("RootAt(101): %v", err)
	}
	if !VerifyProof(proofV101, rootV101) {
		t.Fatal("proof for block 50 invalid at version 101")
	}
	if rootV101 == rootV100 {
		t.Fatal("root did not change after appending reversal")
	}

	if err := l.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain after reversal: %v", err)
	}
}

// TestReversalRules 冲正约束：不能重复冲正，不能冲正冲正条目，不能冲正不存在的区块。
func TestReversalRules(t *testing.T) {
	l := newTestLedger(t, map[string]int64{"alice": 1000})
	if _, err := l.AppendTransfer("alice", "bob", 100, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendReversal(0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendReversal(0, ""); err == nil {
		t.Fatal("double reversal should be rejected")
	}
	if _, err := l.AppendReversal(1, ""); err == nil {
		t.Fatal("reversing a reversal should be rejected")
	}
	if _, err := l.AppendReversal(99, ""); err == nil {
		t.Fatal("reversing a non-existent block should be rejected")
	}
	if got := l.Len(); got != 2 {
		t.Fatalf("len = %d, want 2 (failed reversals must not append)", got)
	}
}

// TestTransferRules 转账约束：金额必须为正、余额必须充足。
func TestTransferRules(t *testing.T) {
	l := newTestLedger(t, map[string]int64{"alice": 100})
	if _, err := l.AppendTransfer("alice", "bob", 0, ""); err == nil {
		t.Fatal("zero amount should be rejected")
	}
	if _, err := l.AppendTransfer("alice", "bob", -5, ""); err == nil {
		t.Fatal("negative amount should be rejected")
	}
	if _, err := l.AppendTransfer("alice", "bob", 101, ""); err == nil {
		t.Fatal("overspend should be rejected")
	}
	if _, err := l.AppendTransfer("alice", "alice", 1, ""); err == nil {
		t.Fatal("self transfer should be rejected")
	}
	if got := l.Len(); got != 0 {
		t.Fatalf("len = %d, want 0", got)
	}
}

// TestTamperDetection 篡改任一历史字段都会破坏哈希链或签名校验。
func TestTamperDetection(t *testing.T) {
	l := newTestLedger(t, map[string]int64{"alice": 1000})
	for i := 0; i < 5; i++ {
		if _, err := l.AppendTransfer("alice", "bob", 10, ""); err != nil {
			t.Fatal(err)
		}
	}
	blk, err := l.GetBlock(2)
	if err != nil {
		t.Fatal(err)
	}

	// 篡改金额：哈希与签名都应失效。
	tampered := blk
	tampered.Payload.Amount = 999
	if computeHash(tampered) == blk.Hash {
		t.Fatal("tampered block hash unchanged")
	}
	if VerifySignature(l.PublicKey(), tampered) {
		t.Fatal("tampered block signature still valid")
	}

	// 篡改默克尔证明：叶子哈希错误则验证失败。
	proof, err := l.Prove(2)
	if err != nil {
		t.Fatal(err)
	}
	root, err := l.RootAt(l.Len())
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(proof, root) {
		t.Fatal("valid proof rejected")
	}
	bad := proof
	bad.Leaf[0] ^= 0xff
	if VerifyProof(bad, root) {
		t.Fatal("tampered proof accepted")
	}
}

// TestMerkleProofAllSizes 对各种账簿规模，每个区块的证明都能通过验证。
func TestMerkleProofAllSizes(t *testing.T) {
	l := newTestLedger(t, map[string]int64{"alice": 1_000_000})
	for n := 1; n <= 64; n++ {
		if _, err := l.AppendTransfer("alice", "bob", 1, ""); err != nil {
			t.Fatal(err)
		}
		root, err := l.RootAt(n)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			p, err := l.ProveAt(uint64(i), n)
			if err != nil {
				t.Fatalf("n=%d i=%d: %v", n, i, err)
			}
			if !VerifyProof(p, root) {
				t.Fatalf("n=%d i=%d: proof rejected", n, i)
			}
		}
	}
}
