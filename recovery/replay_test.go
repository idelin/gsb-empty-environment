package recovery

import (
	"crypto/ed25519"
	"testing"
)

type deviceState struct {
	account AccountID
	device  DeviceID
	name    DeviceName
	priv    ed25519.PrivateKey
	trees   map[uint64]prekeyTree
	epoch   uint64
	seq     uint64
}

func mustDeviceKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	return mustKey(t)
}

func makeDevice(t *testing.T, account AccountID, device DeviceID, name DeviceName, commitments []Hash) *deviceState {
	t.Helper()
	d := &deviceState{
		account: account,
		device:  device,
		name:    name,
		priv:    mustDeviceKey(t),
		trees:   make(map[uint64]prekeyTree),
	}
	d.rotate(t, 0, commitments)
	return d
}

func (d *deviceState) rotate(t *testing.T, epoch uint64, commitments []Hash) {
	t.Helper()
	d.epoch = epoch
	d.trees[epoch] = buildPrekeyTree(d.device, epoch, commitments)
}

func (d *deviceState) identityPK() []byte {
	return []byte(d.priv.Public().(ed25519.PublicKey))
}

func (d *deviceState) addEvent(kind EventKind) Event {
	e := Event{
		Seq:        d.seq,
		Account:    d.account,
		Device:     d.device,
		Name:       d.name,
		Kind:       kind,
		IdentityPK: d.identityPK(),
		PrekeyRoot: d.trees[d.epoch].root(),
		Epoch:      d.epoch,
	}
	d.seq++
	return e
}

func (d *deviceState) rotateEvent(t *testing.T, epoch uint64, commitments []Hash) Event {
	t.Helper()
	d.rotate(t, epoch, commitments)
	return d.addEvent(EventRotate)
}

func (d *deviceState) commitment(epoch uint64, index uint64) Hash {
	return d.trees[epoch].leafCommitment(index)
}

func (pt prekeyTree) leafCommitment(index uint64) Hash {
	// 叶子由承诺派生，测试侧直接按同样规则反推承诺：这里保存原始承诺更稳妥。
	return pt.commitments[index]
}

func newTestTrust(t *testing.T) (TrustAnchor, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	dirPriv := mustKey(t)
	witPriv := mustKey(t)
	return TrustAnchor{
		DirectoryKey: []byte(dirPriv.Public().(ed25519.PublicKey)),
		WitnessKey:   []byte(witPriv.Public().(ed25519.PublicKey)),
	}, dirPriv, witPriv
}

func buildRequest(d *deviceState, commitment Hash, send SignedCheckpoint, wrapped []byte) RecoveryRequest {
	return RecoveryRequest{
		SessionDigest:      domainHash("session", []byte(d.device), commitment),
		Account:            d.account,
		Device:             d.device,
		Commitment:         commitment,
		SendCheckpoint:     send,
		WrappedRecoveryKey: wrapped,
	}
}

func makeReceipt(t *testing.T, d *deviceState, req RecoveryRequest) Receipt {
	t.Helper()
	r := Receipt{
		SessionDigest: req.SessionDigest,
		Device:        d.device,
		IdentityPK:    d.identityPK(),
		Commitment:    req.Commitment,
		SendTreeSize:  req.SendCheckpoint.TreeSize,
	}
	return signReceipt(d.priv, r)
}

// ingestDeviceChainAt 只导入目标设备的连续事件，且全部锚定到发送检查点 size。
func ingestDeviceChainAt(t *testing.T, a *Acceptor, b *branch, device DeviceID, size int) {
	t.Helper()
	for i := 0; i < size; i++ {
		if b.events[i].Device != device {
			continue
		}
		er := b.eventRecordAt(i, size)
		if err := a.Ingest(Record{Kind: RecEvent, Event: &er}); err != nil {
			t.Fatalf("ingest chain event %d: %v", i, err)
		}
	}
}

func ingestReceiptBundle(t *testing.T, a *Acceptor, w *witness, req RecoveryRequest, r Receipt) {
	t.Helper()
	w.addReceipt(r)
	cp := w.checkpoint(t)
	if err := a.Ingest(Record{Kind: RecReceiptCheckpoint, ReceiptCheckpoint: &cp}); err != nil {
		t.Fatalf("ingest receipt checkpoint: %v", err)
	}
	sp := w.proof(req.SessionDigest)
	if err := a.Ingest(Record{Kind: RecSparseProof, SparseProof: &sp}); err != nil {
		t.Fatalf("ingest sparse proof: %v", err)
	}
	if err := a.Ingest(Record{Kind: RecReceipt, Receipt: &r}); err != nil {
		t.Fatalf("ingest receipt: %v", err)
	}
}
