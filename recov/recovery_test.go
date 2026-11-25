package recov

import (
	"bytes"
	"errors"
	"testing"
)

const (
	acct   AccountID  = "acct-1"
	phone  DeviceName = "phone"
	laptop DeviceName = "laptop"
	instA  InstanceID = "inst-phone-1"
	instB  InstanceID = "inst-laptop-1"
	instA2 InstanceID = "inst-phone-2"
)

// 场景一：跨设备使用相同 OPK 序号，互不串扰，两条会话都能恢复。
func TestCrossDeviceSameIndex(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	h.register(acct, laptop, instB)
	blkA := h.publishOPK(acct, instA, 4)
	blkB := h.publishOPK(acct, instB, 4)
	cp := h.dirCPs[len(h.dirCPs)-1]

	keyA := []byte("recovery-key-A!!")
	keyB := []byte("recovery-key-B!!")
	envA := h.envelopeAt(acct, instA, 0, cp, keyA)
	envB := h.envelopeAt(acct, instB, 0, cp, keyB)
	h.receiptFor(envA)
	h.receiptFor(envB)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(blkA); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportOPKBlock(blkB); err != nil {
		t.Fatal(err)
	}

	recovered := map[InstanceID][]byte{}
	cb := func(_ []byte, _ AccountID, inst InstanceID, rk []byte) error {
		recovered[inst] = clone(rk)
		return nil
	}
	if err := im.RecoverSession(envA, h.opkPriv[instA][0], cb); err != nil {
		t.Fatalf("recover A: %v", err)
	}
	if err := im.RecoverSession(envB, h.opkPriv[instB][0], cb); err != nil {
		t.Fatalf("recover B: %v", err)
	}
	if !bytes.Equal(recovered[instA], keyA) || !bytes.Equal(recovered[instB], keyB) {
		t.Fatal("cross-device recovery key mismatch")
	}
}

// 场景二：并发换钥。锚定换钥前检查点的会话即使在换钥后导入也应接受。
func TestConcurrentKeyRotation(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 3)
	cpBefore := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 1, cpBefore, []byte("rotate-key!!16"))

	h.rotate(acct, instA)
	cpAfter := h.dirCPs[len(h.dirCPs)-1]
	h.receiptFor(env)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cpAfter)
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}

	var got []byte
	err := im.RecoverSession(env, h.opkPriv[instA][1],
		func(_ []byte, _ AccountID, _ InstanceID, rk []byte) error {
			got = clone(rk)
			return nil
		})
	if err != nil {
		t.Fatalf("session anchored before rotation must verify: %v", err)
	}
	if string(got) != "rotate-key!!16" {
		t.Fatal("recovery key mismatch")
	}

	st, err := foldEvents(h.events[:cpAfter.Checkpoint.TreeSize])
	if err != nil {
		t.Fatal(err)
	}
	ident, err := st.identityFor(&SessionEnvelope{Account: acct, Instance: instA, OPKIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ident.idKey, h.events[0].IdentityKey) {
		t.Fatal("identity key must change after rotation")
	}
}

// 场景三：延迟到达的撤销。撤销晚于会话则有效；早于会话则拒绝。
func TestDelayedRevocation(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 4)
	cpBefore := h.dirCPs[len(h.dirCPs)-1]

	env := h.envelopeAt(acct, instA, 2, cpBefore, []byte("delayed-rk!!16"))
	h.receiptFor(env)

	h.revoke(acct, instA, 2)
	cpAfter := h.dirCPs[len(h.dirCPs)-1]

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cpAfter)
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}
	if err := im.RecoverSession(env, h.opkPriv[instA][2],
		func(_ []byte, _ AccountID, _ InstanceID, _ []byte) error { return nil }); err != nil {
		t.Fatalf("revocation after session must not invalidate it: %v", err)
	}

	envLate := h.envelopeAt(acct, instA, 2, cpAfter, []byte("delayed-rk!!16"))
	_, err := im.Evaluate(envLate)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("expected ErrNotAuthorized, got %v", err)
	}
}

// 场景四：同名设备重新注册，新实例绝不继承旧实例的身份钥或 OPK。
func TestReregistrationNoInheritance(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	oldBlock := h.publishOPK(acct, instA, 2)
	oldID := h.events[0].IdentityKey
	h.remove(acct, instA)

	// 实例标识复用时折叠必须拒绝。
	bad := newHarness(t)
	bad.register(acct, phone, instA)
	bad.appendEvent(&Event{Kind: EvRegister, Account: acct, Device: phone, Instance: instA,
		IdentityKey: oldID})
	if _, err := foldEvents(bad.events); !errors.Is(err, ErrReRegistration) {
		t.Fatalf("expected ErrReRegistration, got %v", err)
	}

	// 注销后用新实例标识重注册是合法的，但钥完全不同。
	h.register(acct, phone, instA2)
	newBlock := h.publishOPK(acct, instA2, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]

	st, err := foldEvents(h.events[:cp.Checkpoint.TreeSize])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(st.instances[instA2].idKey, oldID) {
		t.Fatal("re-registered instance inherited old identity key")
	}
	if bytes.Equal(st.instances[instA2].opkRoot, oldBlock.Root) {
		t.Fatal("re-registered instance inherited old OPK tree")
	}
	if st.instances[instA].active {
		t.Fatal("old instance should be inactive")
	}
	if st.nameToInst[phone] != instA2 {
		t.Fatal("device name must resolve to the new instance")
	}

	// 旧 OPK 块不能证明新实例的承诺，必须报缺少包含证明。
	env := h.envelopeAt(acct, instA2, 0, cp, []byte("new-inst-rk!!1"))
	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	if err := im.ImportOPKBlock(oldBlock); err != nil {
		t.Fatal(err)
	}
	if _, err := im.findOPKBlock(env); !errors.Is(err, ErrMissingInclusion) {
		t.Fatalf("old OPK block must not prove new instance, got %v", err)
	}
	if err := im.ImportOPKBlock(newBlock); err != nil {
		t.Fatal(err)
	}
	if _, err := im.findOPKBlock(env); err != nil {
		t.Fatalf("new OPK block must prove the commitment: %v", err)
	}
}

// 场景五：回执先到、目录前缀后到。先 Pending，补齐后接受。
func TestOutOfOrderReceiptFirst(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 0, cp, []byte("late-dir-rk!!1"))
	h.receiptFor(env)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}
	if _, err := im.Evaluate(env); !errors.Is(err, ErrPending) {
		t.Fatalf("expected ErrPending before directory arrives, got %v", err)
	}

	h.feedDirectory(im, cp)
	if err := im.RecoverSession(env, h.opkPriv[instA][0],
		func(_ []byte, _ AccountID, _ InstanceID, _ []byte) error { return nil }); err != nil {
		t.Fatalf("recovery after late directory must succeed: %v", err)
	}
}

// 场景六：缺少包含证明与确有双重消费必须分成两类错误。
func TestMissingInclusionVsDoubleSpend(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 3)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env1 := h.envelopeAt(acct, instA, 0, cp, []byte("spend-one!!16"))
	h.receiptFor(env1)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	if _, err := im.Evaluate(env1); !errors.Is(err, ErrMissingInclusion) {
		t.Fatalf("expected ErrMissingInclusion without OPK block, got %v", err)
	}

	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}
	// 第二个不同会话摘要消费同一承诺 -> 双花。
	env2 := h.envelopeAt(acct, instA, 0, cp, []byte("spend-two!!16"))
	env2.HandshakeDigest = bytes.Repeat([]byte{9}, 32)
	h.receiptFor(env2)
	if err := im.ImportReceipt(h.receipts[1]); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportWitnessCheckpoint(h.witCPs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := im.Evaluate(env1); !errors.Is(err, ErrDoubleConsumption) {
		t.Fatalf("expected ErrDoubleConsumption, got %v", err)
	}

	// 同一会话的重复回执不是双花。
	h3 := newHarness(t)
	h3.register(acct, phone, instA)
	b3 := h3.publishOPK(acct, instA, 2)
	c3 := h3.dirCPs[len(h3.dirCPs)-1]
	e3 := h3.envelopeAt(acct, instA, 0, c3, []byte("dup-rk!!16"))
	h3.receiptFor(e3)
	h3.receipts = append(h3.receipts, cloneReceipt(h3.receipts[0]))
	h3.receipts[1].Seq = 1
	h3.witCPs = append(h3.witCPs, h3.signReceipts())
	im3 := NewImporter(dirPub(h3.dirKey), witPub(h3.witKey))
	h3.feedDirectory(im3, c3)
	h3.feedAllReceipts(im3)
	if err := im3.ImportOPKBlock(b3); err != nil {
		t.Fatal(err)
	}
	dec, err := im3.Evaluate(e3)
	if err != nil {
		t.Fatalf("duplicate receipt of the same session is not double spend: %v", err)
	}
	if dec.ReceiptCount != 2 {
		t.Fatalf("expected 2 self receipts, got %d", dec.ReceiptCount)
	}
}

// 场景七：目录冲突后缀。
//   - 分叉点之后两分支对同一片 OPK 给出不同授权状态 -> 会话身份不一致 -> 拒绝；
//   - 会话身份在两分支完全相同 -> 即使有分叉也接受。
func TestConflictingSuffixesDirectory(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 3)
	commonSize := uint64(len(h.events))
	commonEvents := append([]*Event(nil), h.events...)

	// 共同前缀之后，同序号放两条冲突事件，各自签名成同规模不同根的检查点。
	revokeA := &Event{Seq: commonSize, Kind: EvOPKRevoke, Account: acct, Instance: instA, OPKIndex: 0}
	revokeB := &Event{Seq: commonSize, Kind: EvOPKRevoke, Account: acct, Instance: instA, OPKIndex: 1}

	cpA := signSyntheticDir(t, h.dirKey, append(append([]*Event(nil), commonEvents...), revokeA))
	cpB := signSyntheticDir(t, h.dirKey, append(append([]*Event(nil), commonEvents...), revokeB))

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	for _, ev := range commonEvents {
		if err := im.ImportEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := im.ImportEvent(revokeA); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportEvent(revokeB); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportDirectoryCheckpoint(cpA); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportDirectoryCheckpoint(cpB); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}

	// 引用 0 号 OPK：分支 A 撤销了它，分支 B 没有 -> 身份不一致 -> 拒绝。
	env0 := h.envelopeAt(acct, instA, 0, cpA, []byte("fork-rk!!16"))
	_, err := im.Evaluate(env0)
	if !errors.Is(err, ErrForkAmbiguity) {
		t.Fatalf("expected ErrForkAmbiguity for differing authorization, got %v", err)
	}

	// 引用 2 号 OPK：两个分支都没动它，会话身份在两分支相同 -> 接受（补回执后）。
	env2 := h.envelopeAt(acct, instA, 2, cpA, []byte("fork-rk!!16"))
	h.receiptFor(env2)
	h.feedAllReceipts(im)
	dec, err := im.Evaluate(env2)
	if err != nil {
		t.Fatalf("identical identity across branches must verify: %v", err)
	}
	if dec.BranchCount != 2 {
		t.Fatalf("expected 2 plausible branches, got %d", dec.BranchCount)
	}
}

// 场景八：两个回执分支后来各自携带不同的消费回执 -> 跨分支双花。
func TestTwoReceiptBranchesDifferentConsumption(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 0, cp, []byte("rcpt-fork!!16"))

	// 回执分叉：共同的第 0 条之后，两个分支在序号 1 放不同会话的消费回执。
	base := &Receipt{
		Seq: 0, Account: acct, Instance: instA, OPKIndex: 0,
		Commitment: clone(env.OPKCommitment),
		SessionID:  DeriveSessionID(env.HandshakeDigest, env.OPKCommitment, instA, acct),
	}
	otherHandshake := bytes.Repeat([]byte{7}, 32)
	otherSID := DeriveSessionID(otherHandshake, env.OPKCommitment, instA, acct)
	consumeSelf := &Receipt{Seq: 1, Account: acct, Instance: instA, OPKIndex: 0,
		Commitment: clone(env.OPKCommitment), SessionID: clone(base.SessionID)}
	consumeOther := &Receipt{Seq: 1, Account: acct, Instance: instA, OPKIndex: 0,
		Commitment: clone(env.OPKCommitment), SessionID: otherSID}

	wcpA := signSyntheticReceipts(t, h.witKey, []*Receipt{base, consumeSelf})
	wcpB := signSyntheticReceipts(t, h.witKey, []*Receipt{base, consumeOther})

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportReceipt(base); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportReceipt(consumeSelf); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportReceipt(consumeOther); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportWitnessCheckpoint(wcpA); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportWitnessCheckpoint(wcpB); err != nil {
		t.Fatal(err)
	}

	// 只有分支 A 时，本会话是唯一消费者，应接受。
	imA := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(imA, cp)
	_ = imA.ImportOPKBlock(blk)
	_ = imA.ImportReceipt(base)
	_ = imA.ImportReceipt(consumeSelf)
	_ = imA.ImportWitnessCheckpoint(wcpA)
	if _, err := imA.Evaluate(env); err != nil {
		t.Fatalf("single branch should verify: %v", err)
	}

	// 两个分支都纳入后，承诺被两个不同会话摘要消费 -> 双花。
	if _, err := im.Evaluate(env); !errors.Is(err, ErrDoubleConsumption) {
		t.Fatalf("expected ErrDoubleConsumption across receipt branches, got %v", err)
	}
}

func signSyntheticDir(t *testing.T, k *DirectoryKey, events []*Event) SignedCheckpoint {
	t.Helper()
	leaves := make([][]byte, len(events))
	for i, e := range events {
		e.Seq = uint64(i)
		leaves[i] = EventHash(e)
	}
	sc, err := k.SignCheckpoint(&Checkpoint{
		LogID: DirectoryLogID, TreeSize: uint64(len(events)), TreeRoot: MTH(leaves),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func signSyntheticReceipts(t *testing.T, k *WitnessKey, receipts []*Receipt) SignedCheckpoint {
	t.Helper()
	leaves := make([][]byte, len(receipts))
	for i, r := range receipts {
		r.Seq = uint64(i)
		leaves[i] = ReceiptHash(r)
	}
	sc, err := k.SignReceiptCheckpoint(&Checkpoint{
		LogID: ReceiptLogID, TreeSize: uint64(len(receipts)), TreeRoot: MTH(leaves),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}
