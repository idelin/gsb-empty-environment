package recov

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// testHarness 用真实密码学原语构造一个可全量回放的小目录。
type testHarness struct {
	t      *testing.T
	dirKey *DirectoryKey
	witKey *WitnessKey
	events []*Event
	dirCPs []SignedCheckpoint

	// 每个实例保存一棵 OPK 树的私钥与承诺。
	opkPriv    map[InstanceID][]*ecdh.PrivateKey
	opkBlk     map[InstanceID]*OPKBlock
	opkHistory map[InstanceID][]*ecdh.PrivateKey // 历次发布树的私钥快照

	receipts []*Receipt
	witCPs   []SignedCheckpoint
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	dirKey, err := GenerateDirectoryKey()
	if err != nil {
		t.Fatal(err)
	}
	witKey, err := GenerateWitnessKey()
	if err != nil {
		t.Fatal(err)
	}
	return &testHarness{
		t:          t,
		dirKey:     dirKey,
		witKey:     witKey,
		opkPriv:    map[InstanceID][]*ecdh.PrivateKey{},
		opkBlk:     map[InstanceID]*OPKBlock{},
		opkHistory: map[InstanceID][]*ecdh.PrivateKey{},
	}
}

func (h *testHarness) seq() uint64 { return uint64(len(h.events)) }

// appendEvent 追加事件并立即签发覆盖当前日志的目录检查点。
func (h *testHarness) appendEvent(ev *Event) {
	ev.Seq = h.seq()
	h.events = append(h.events, ev)
	h.dirCPs = append(h.dirCPs, h.signDir())
}

func (h *testHarness) signDir() SignedCheckpoint {
	leaves := make([][]byte, len(h.events))
	for i, e := range h.events {
		leaves[i] = EventHash(e)
	}
	cp := &Checkpoint{LogID: DirectoryLogID, TreeSize: h.seq(), TreeRoot: MTH(leaves)}
	sc, err := h.dirKey.SignCheckpoint(cp)
	if err != nil {
		h.t.Fatal(err)
	}
	return sc
}

func (h *testHarness) register(acct AccountID, name DeviceName, inst InstanceID) *ecdh.PrivateKey {
	idPriv, err := GenerateECDHKey()
	if err != nil {
		h.t.Fatal(err)
	}
	h.appendEvent(&Event{
		Kind: EvRegister, Account: acct, Device: name, Instance: inst,
		IdentityKey: idPriv.PublicKey().Bytes(),
	})
	return idPriv
}

func (h *testHarness) rotate(acct AccountID, inst InstanceID) *ecdh.PrivateKey {
	idPriv, err := GenerateECDHKey()
	if err != nil {
		h.t.Fatal(err)
	}
	h.appendEvent(&Event{Kind: EvRotateIDKey, Account: acct, Instance: inst,
		IdentityKey: idPriv.PublicKey().Bytes()})
	return idPriv
}

func (h *testHarness) remove(acct AccountID, inst InstanceID) {
	h.appendEvent(&Event{Kind: EvRemove, Account: acct, Instance: inst})
}

// publishOPK 为实例生成 count 把一次性密钥，发布承诺树并挂载到目录。
func (h *testHarness) publishOPK(acct AccountID, inst InstanceID, count int) *OPKBlock {
	privs := make([]*ecdh.PrivateKey, count)
	keys := make([][]byte, count)
	leafData := make([][]byte, count)
	for i := 0; i < count; i++ {
		priv, err := GenerateECDHKey()
		if err != nil {
			h.t.Fatal(err)
		}
		privs[i] = priv
		keys[i] = priv.PublicKey().Bytes()
		leafData[i] = OPKCommitment(priv.PublicKey(), uint32(i), inst, acct)
	}
	block := &OPKBlock{
		Account: acct, Instance: inst, Root: MTH(leafData),
		LeafCount: uint32(count), LeafKeys: keys,
	}
	h.opkPriv[inst] = privs
	h.opkBlk[inst] = block
	h.opkHistory[inst] = append(h.opkHistory[inst], privs...)
	h.appendEvent(&Event{
		Kind: EvOPKBlock, Account: acct, Instance: inst,
		OPKRoot: block.Root, OPKCount: uint32(count),
	})
	return block
}

func (h *testHarness) revoke(acct AccountID, inst InstanceID, idx uint32) {
	h.appendEvent(&Event{Kind: EvOPKRevoke, Account: acct, Instance: inst, OPKIndex: idx})
}

// envelopeAt 用指定检查点时刻的状态构造一个消费某 OPK 叶子的会话信封。
func (h *testHarness) envelopeAt(acct AccountID, inst InstanceID, idx uint32,
	sendCP SignedCheckpoint, recoveryKey []byte) *SessionEnvelope {
	block := h.opkBlk[inst]
	opkPriv := h.opkPriv[inst][idx]
	commitment := OPKCommitment(opkPriv.PublicKey(), idx, inst, acct)
	proof := InclusionProof(opkLeafData(block, inst, acct), uint64(idx))

	ephPriv, err := GenerateECDHKey()
	if err != nil {
		h.t.Fatal(err)
	}
	handshake := make([]byte, 32)
	if _, err := rand.Read(handshake); err != nil {
		h.t.Fatal(err)
	}
	sid := DeriveSessionID(handshake, commitment, inst, acct)
	wrapped, nonce, err := WrapRecoveryKey(opkPriv.PublicKey(), ephPriv, sid, recoveryKey)
	if err != nil {
		h.t.Fatal(err)
	}
	return &SessionEnvelope{
		Account: acct, Instance: inst, OPKIndex: idx, OPKCommitment: commitment,
		OPKInclusion:    proof,
		SendCheckpoint:  sendCP,
		EphemeralPub:    ephPriv.PublicKey().Bytes(),
		HandshakeDigest: handshake,
		WrappedKey:      wrapped, Nonce: nonce,
	}
}

func opkLeafData(block *OPKBlock, inst InstanceID, acct AccountID) [][]byte {
	out := make([][]byte, block.LeafCount)
	for i, keyPt := range block.LeafKeys {
		pub, err := UnmarshalECDHPublic(keyPt)
		if err != nil {
			panic(err)
		}
		out[i] = OPKCommitment(pub, uint32(i), inst, acct)
	}
	return out
}

// receiptFor 为信封追加一条回执并签发回执检查点。
func (h *testHarness) receiptFor(env *SessionEnvelope) (*Receipt, SignedCheckpoint) {
	r := &Receipt{
		Seq:        uint64(len(h.receipts)),
		Account:    env.Account,
		Instance:   env.Instance,
		OPKIndex:   env.OPKIndex,
		Commitment: clone(env.OPKCommitment),
		SessionID: DeriveSessionID(env.HandshakeDigest, env.OPKCommitment,
			env.Instance, env.Account),
	}
	h.receipts = append(h.receipts, r)
	cp := h.signReceipts()
	h.witCPs = append(h.witCPs, cp)
	return r, cp
}

func (h *testHarness) signReceipts() SignedCheckpoint {
	leaves := make([][]byte, len(h.receipts))
	for i, r := range h.receipts {
		leaves[i] = ReceiptHash(r)
	}
	cp := &Checkpoint{
		LogID: ReceiptLogID, TreeSize: uint64(len(h.receipts)), TreeRoot: MTH(leaves),
	}
	sc, err := h.witKey.SignReceiptCheckpoint(cp)
	if err != nil {
		h.t.Fatal(err)
	}
	return sc
}

// feedDirectory 把目录事件与指定检查点灌入一个全新导入器。
func (h *testHarness) feedDirectory(im *Importer, cp SignedCheckpoint) {
	h.t.Helper()
	for i := uint64(0); i < cp.Checkpoint.TreeSize; i++ {
		if err := im.ImportEvent(h.events[i]); err != nil {
			h.t.Fatalf("import event %d: %v", i, err)
		}
	}
	if err := im.ImportDirectoryCheckpoint(cp); err != nil {
		h.t.Fatalf("import dir checkpoint: %v", err)
	}
}

func (h *testHarness) feedAllReceipts(im *Importer) {
	h.t.Helper()
	for _, r := range h.receipts {
		if err := im.ImportReceipt(r); err != nil {
			h.t.Fatalf("import receipt: %v", err)
		}
	}
	for _, cp := range h.witCPs {
		if err := im.ImportWitnessCheckpoint(cp); err != nil {
			h.t.Fatalf("import witness checkpoint: %v", err)
		}
	}
}

func dirPub(k *DirectoryKey) ed25519.PublicKey { return k.Public }
func witPub(k *WitnessKey) ed25519.PublicKey   { return k.Public }
