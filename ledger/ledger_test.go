package ledger

import (
	"fmt"
	"testing"
)

var testKey = []byte("audit-ledger-secret-key")

// newLedgerWith100 写入 100 笔转账事务的账簿。
func newLedgerWith100(t *testing.T) *Ledger {
	t.Helper()
	l := New(testKey)
	for i := 0; i < 100; i++ {
		from := fmt.Sprintf("acct-%02d", i%10)
		to := fmt.Sprintf("acct-%02d", (i+1)%10)
		if _, err := l.Transfer(from, to, int64(100+i)); err != nil {
			t.Fatalf("transfer %d failed: %v", i, err)
		}
	}
	if l.Height() != 100 {
		t.Fatalf("height = %d, want 100", l.Height())
	}
	return l
}

// TestHashChain100 验收：100 笔事务形成完整哈希链。
func TestHashChain100(t *testing.T) {
	l := newLedgerWith100(t)

	// 创世区块前序哈希为零，其余区块链接前一区块哈希。
	var zero [32]byte
	b0, _ := l.Block(0)
	if b0.PrevHash != zero {
		t.Fatal("genesis PrevHash must be zero")
	}
	for i := 1; i < 100; i++ {
		prev, _ := l.Block(i - 1)
		cur, _ := l.Block(i)
		if cur.PrevHash != prev.Hash {
			t.Fatalf("chain broken at block %d", i)
		}
	}
	if !l.VerifyChain() {
		t.Fatal("VerifyChain failed on 100-block ledger")
	}
}

// TestReversalAcceptance 验收主场景：
// 对第 50 笔发起冲正 → 账簿新增第 101 笔冲正记录并更新总账，
// 原第 50 笔证据与路径证明依然有效。
func TestReversalAcceptance(t *testing.T) {
	l := newLedgerWith100(t)

	// 冲正前：为第 50 笔（高度 49）生成版本 100 的证明并留存证据快照。
	const target = 49
	origBefore, err := l.Block(target)
	if err != nil {
		t.Fatal(err)
	}
	rootV100, err := l.Root(100)
	if err != nil {
		t.Fatal(err)
	}
	proofV100, err := l.Prove(target, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(origBefore.Hash, proofV100, rootV100) {
		t.Fatal("proof for block 49 at version 100 invalid before reversal")
	}

	// 记录冲正前总账。
	orig := origBefore.Payload
	fromBefore := l.Balance(orig.From)
	toBefore := l.Balance(orig.To)

	// 发起冲正。
	rev, err := l.Reverse(target)
	if err != nil {
		t.Fatalf("reverse failed: %v", err)
	}

	// 1) 账簿新增第 101 笔（高度 100）冲正记录。
	if l.Height() != 101 {
		t.Fatalf("height = %d, want 101", l.Height())
	}
	if rev.Index != 100 {
		t.Fatalf("reversal index = %d, want 100", rev.Index)
	}
	if rev.Payload.Type != TxReversal || rev.Payload.Ref != target {
		t.Fatal("reversal payload malformed")
	}
	if rev.Payload.Amount != -orig.Amount {
		t.Fatalf("reversal amount = %d, want %d", rev.Payload.Amount, -orig.Amount)
	}

	// 2) 总账被更新：原转账被抵消。
	if got := l.Balance(orig.From); got != fromBefore+orig.Amount {
		t.Fatalf("balance(%s) = %d, want %d", orig.From, got, fromBefore+orig.Amount)
	}
	if got := l.Balance(orig.To); got != toBefore-orig.Amount {
		t.Fatalf("balance(%s) = %d, want %d", orig.To, got, toBefore-orig.Amount)
	}

	// 3) 原第 50 笔证据未被物理覆写。
	origAfter, _ := l.Block(target)
	if !origAfter.Equal(origBefore) {
		t.Fatal("original block 49 was physically modified")
	}

	// 4) 旧版本（100）的路径证明依然有效。
	if !VerifyProof(origAfter.Hash, proofV100, rootV100) {
		t.Fatal("historical proof for block 49 at version 100 invalid after reversal")
	}

	// 5) 新版本（101）下原区块证明同样可生成且有效。
	rootV101, err := l.Root(101)
	if err != nil {
		t.Fatal(err)
	}
	proofV101, err := l.Prove(target, 101)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(origAfter.Hash, proofV101, rootV101) {
		t.Fatal("proof for block 49 at version 101 invalid")
	}

	// 6) 冲正后整条链仍然完整。
	if !l.VerifyChain() {
		t.Fatal("VerifyChain failed after reversal")
	}
}

// TestDoubleReverseRejected 同一笔转账不允许重复冲正。
func TestDoubleReverseRejected(t *testing.T) {
	l := newLedgerWith100(t)
	if _, err := l.Reverse(49); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reverse(49); err == nil {
		t.Fatal("double reversal must be rejected")
	}
	if _, err := l.Reverse(100); err == nil {
		t.Fatal("reversing a reversal entry must be rejected")
	}
	if l.Height() != 101 {
		t.Fatalf("height = %d, want 101 (no extra entries)", l.Height())
	}
}

// TestTamperDetection 篡改任一历史区块都会破坏校验。
func TestTamperDetection(t *testing.T) {
	l := newLedgerWith100(t)
	victim := l.blocks[50]
	origAmount := victim.Payload.Amount
	victim.Payload.Amount = origAmount * 10 // 模拟物理篡改
	if l.VerifyBlock(50) {
		t.Fatal("tampered block passed verification")
	}
	victim.Payload.Amount = origAmount // 恢复
	if !l.VerifyChain() {
		t.Fatal("chain should verify after restore")
	}
}

// TestProofEveryBlockEveryVersion 任意历史版本的任意区块均可出证并验证。
func TestProofEveryBlockEveryVersion(t *testing.T) {
	l := New(testKey)
	for i := 0; i < 20; i++ {
		if _, err := l.Transfer("a", "b", int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	for v := 1; v <= l.Height(); v++ {
		root, err := l.Root(v)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < v; i++ {
			proof, err := l.Prove(i, v)
			if err != nil {
				t.Fatalf("prove(%d, %d): %v", i, v, err)
			}
			b, _ := l.Block(i)
			if !VerifyProof(b.Hash, proof, root) {
				t.Fatalf("proof invalid: block %d version %d", i, v)
			}
		}
	}
}

// TestProofRejectsWrongLeaf 证明对错误叶子不成立。
func TestProofRejectsWrongLeaf(t *testing.T) {
	l := newLedgerWith100(t)
	root, _ := l.Root(100)
	proof, _ := l.Prove(49, 100)
	other, _ := l.Block(50)
	if VerifyProof(other.Hash, proof, root) {
		t.Fatal("proof verified against wrong leaf")
	}
}
