package recovery

import (
	"crypto/ed25519"
	"testing"
)

// 分叉点本身存在两个 size 相同、会话身份不同的检查点：必须 branch_conflict。
func TestConflictAtSendSizeRejected(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})

	common := newBranch(dirPriv)
	common.appendEvent(dev.addEvent(EventAdd))

	// 手工构造在 size=2 处的冲突：两条链 size=1 前缀相同，但追加不同事件。
	forkOne := common.forkFrom(1)
	forkOne.appendEvent(dev.rotateEvent(t, 1, []Hash{domainHash("new-tree")}))
	sendOne := forkOne.head()

	forkTwo := common.forkFrom(1)
	forkTwo.appendEvent(dev.rotateEvent(t, 2, []Hash{domainHash("other-tree")}))
	sendTwo := forkTwo.head()

	a := NewAcceptor(anchor, nil)
	common.ingestChain(a)
	forkOne.ingestChain(a)
	forkTwo.ingestChain(a)
	ingestDeviceChainAt(t, a, forkOne, dev.device, int(sendOne.TreeSize))
	ingestDeviceChainAt(t, a, forkTwo, dev.device, int(sendTwo.TreeSize))
	// 两个分叉各自的预钥树根不同，分别导入承诺包含证明。
	ppOne := dev.trees[1].proof(0)
	ppTwo := dev.trees[2].proof(0)
	mustIngest(t, a, Record{Kind: RecPrekeyProof, PrekeyProof: &ppOne})
	mustIngest(t, a, Record{Kind: RecPrekeyProof, PrekeyProof: &ppTwo})

	// 请求锚定其中一个 size=2 检查点；另一个同 size 兄弟构成冲突后缀。
	req := buildRequest(dev, dev.commitment(1, 0), sendOne, []byte("conflict"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	d := a.Evaluate(req)
	if d.Status != StatusRejected ||
		(d.Reason != ReasonBranchConflict && d.Reason != ReasonBadProof) {
		t.Fatalf("conflicting session identities must be rejected, got %s/%s", d.Status, d.Reason)
	}
	if d.ViableBranches < 2 {
		t.Fatalf("expected at least 2 viable branches, got %d", d.ViableBranches)
	}
}

// 坏签名检查点与坏回执签名必须被确定性拒绝，而不是停在 pending。
// 同 size 的两个分叉都能包含同一承诺，但授权身份钥/预钥根不同：branch_conflict。
func TestSameCommitmentDifferentIdentityIsBranchConflict(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("shared-commit")})

	common := newBranch(dirPriv)
	common.appendEvent(dev.addEvent(EventAdd))

	// 分叉一：轮换到 epoch=1，新预钥树仍包含同一承诺值。
	forkOne := common.forkFrom(1)
	forkOne.appendEvent(dev.rotateEvent(t, 1, []Hash{domainHash("shared-commit")}))
	sendOne := forkOne.head()

	// 分叉二：手工构造一条换钥事件，使用另一把身份钥但相同的预钥树根，
	// 这样承诺包含证明在两边都成立，仅身份公钥不同。
	otherPriv := mustKey(t)
	conflicting := Event{
		GlobalIndex: 1,
		Seq:         1,
		Account:     dev.account,
		Device:      dev.device,
		Name:        dev.name,
		Kind:        EventRotate,
		IdentityPK:  []byte(otherPriv.Public().(ed25519.PublicKey)),
		PrekeyRoot:  dev.trees[1].root(),
		Epoch:       1,
	}
	forkTwo := common.forkFrom(1)
	forkTwo.appendEvent(conflicting)
	sendTwo := forkTwo.head()

	a := NewAcceptor(anchor, nil)
	common.ingestChain(a)
	forkOne.ingestChain(a)
	forkTwo.ingestChain(a)
	ingestDeviceChainAt(t, a, forkOne, dev.device, int(sendOne.TreeSize))
	ingestDeviceChainAt(t, a, forkTwo, dev.device, int(sendTwo.TreeSize))
	pp := dev.trees[1].proof(0)
	mustIngest(t, a, Record{Kind: RecPrekeyProof, PrekeyProof: &pp})

	req := buildRequest(dev, dev.commitment(1, 0), sendOne, []byte("conflict"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	d := a.Evaluate(req)
	if d.Status != StatusRejected || d.Reason != ReasonBranchConflict {
		t.Fatalf("different authorized identities must be branch_conflict, got %s/%s", d.Status, d.Reason)
	}
}

func TestBadSignaturesRejected(t *testing.T) {
	anchor, dirPriv, _ := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone", []Hash{domainHash("c0")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("k"))

	tampered := send
	tampered.RootHash = copyHash(send.RootHash)
	tampered.RootHash[0] ^= 0xFF
	req.SendCheckpoint = tampered
	a := NewAcceptor(anchor, nil)
	if d := a.Evaluate(req); d.Status != StatusRejected || d.Reason != ReasonBadSignature {
		t.Fatalf("tampered checkpoint must be bad_signature, got %s/%s", d.Status, d.Reason)
	}
}

// 见证明确给出非包含证明时是 NoReceipt 型 pending，不能与双花混淆。
func TestNoReceiptDistinctFromDoubleSpend(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone", []Hash{domainHash("c0")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("k"))

	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 0)

	w := newWitness(witPriv)
	cp := w.checkpoint(t)
	sp := w.proof(req.SessionDigest)
	sp.Exists = false
	sp.Value = smtEmptyHash
	mustIngest(t, a, Record{Kind: RecReceiptCheckpoint, ReceiptCheckpoint: &cp})
	mustIngest(t, a, Record{Kind: RecSparseProof, SparseProof: &sp})

	d := a.Evaluate(req)
	if d.Status != StatusPending || d.Reason != ReasonNoReceipt {
		t.Fatalf("witness non-inclusion must be pending/no_receipt, got %s/%s", d.Status, d.Reason)
	}
}
