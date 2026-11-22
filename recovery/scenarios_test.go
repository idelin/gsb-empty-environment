package recovery

import "testing"

func ingestPrekeyProof(t *testing.T, a *Acceptor, d *deviceState, epoch, index uint64) {
	t.Helper()
	tree := d.trees[epoch]
	pp := tree.proof(index)
	if err := a.Ingest(Record{Kind: RecPrekeyProof, PrekeyProof: &pp}); err != nil {
		t.Fatalf("ingest prekey proof: %v", err)
	}
}

func mustIngest(t *testing.T, a *Acceptor, r Record) {
	t.Helper()
	if err := a.Ingest(r); err != nil {
		t.Fatalf("ingest %s: %v", r.Kind, err)
	}
}

// 场景一：跨设备同序号。两个设备实例各自从 seq=0 编号，互不干扰。
func TestReplayCrossDeviceSameSequence(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	devA := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("ca0"), domainHash("ca1")})
	devB := makeDevice(t, "acct-1", "device-B", "Alice Laptop",
		[]Hash{domainHash("cb0"), domainHash("cb1")})

	b := newBranch(dirPriv)
	b.appendEvent(devA.addEvent(EventAdd))
	b.appendEvent(devB.addEvent(EventAdd))
	send := b.head()

	var delivered Decision
	a := NewAcceptor(anchor, func(d Decision) { delivered = d })
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, devA.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, devA, 0, 1)

	req := buildRequest(devA, devA.commitment(0, 1), send, []byte("recovery-key-A"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, devA, req))

	decision := a.Evaluate(req)
	if decision.Status != StatusAccepted {
		t.Fatalf("expected accepted, got %s/%s", decision.Status, decision.Reason)
	}
	if delivered.Status != StatusAccepted ||
		string(delivered.Request.WrappedRecoveryKey) != "recovery-key-A" {
		t.Fatalf("callback did not deliver recovery key: %+v", delivered)
	}

	ingestPrekeyProof(t, a, devB, 0, 0)
	reqB := buildRequest(devB, devB.commitment(0, 0), send, []byte("recovery-key-B"))
	a2 := NewAcceptor(anchor, nil)
	b.ingestChain(a2)
	ingestDeviceChainAt(t, a2, b, devB.device, int(send.TreeSize))
	ingestPrekeyProof(t, a2, devB, 0, 0)
	w2 := newWitness(witPriv)
	ingestReceiptBundle(t, a2, w2, reqB, makeReceipt(t, devB, reqB))
	if d := a2.Evaluate(reqB); d.Status != StatusAccepted {
		t.Fatalf("device B acceptance failed: %s/%s", d.Status, d.Reason)
	}
}

// 场景二：并发换钥。旧承诺在换钥后的检查点不再授权。
func TestReplayConcurrentKeyRotation(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("old-0"), domainHash("old-1")})

	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	oldSend := b.head()
	oldCommitment := dev.commitment(0, 0)

	b.appendEvent(dev.rotateEvent(t, 1,
		[]Hash{domainHash("new-0"), domainHash("new-1"), domainHash("new-2")}))
	newSend := b.head()

	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(oldSend.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 0)

	reqOld := buildRequest(dev, oldCommitment, oldSend, []byte("old-key"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, reqOld, makeReceipt(t, dev, reqOld))
	if d := a.Evaluate(reqOld); d.Status != StatusAccepted {
		t.Fatalf("old session at old checkpoint: %s/%s", d.Status, d.Reason)
	}

	// 旧承诺若声称在换钥后的检查点发起，预钥树无法再包含它。
	reqStale := buildRequest(dev, oldCommitment, newSend, []byte("stale"))
	stale := NewAcceptor(anchor, nil)
	b.ingestChain(stale)
	ingestDeviceChainAt(t, stale, b, dev.device, int(newSend.TreeSize))
	ingestPrekeyProof(t, stale, dev, 0, 0)
	wStale := newWitness(witPriv)
	ingestReceiptBundle(t, stale, wStale, reqStale, makeReceipt(t, dev, reqStale))
	d := stale.Evaluate(reqStale)
	if d.Status != StatusPending || d.Reason != ReasonMissingPrekeyProof {
		t.Fatalf("stale commitment must lose authorization, got %s/%s", d.Status, d.Reason)
	}

	ingestDeviceChainAt(t, a, b, dev.device, int(newSend.TreeSize))
	ingestPrekeyProof(t, a, dev, 1, 2)
	reqNew := buildRequest(dev, dev.commitment(1, 2), newSend, []byte("new-key"))
	wNew := newWitness(witPriv)
	ingestReceiptBundle(t, a, wNew, reqNew, makeReceipt(t, dev, reqNew))
	if d := a.Evaluate(reqNew); d.Status != StatusAccepted {
		t.Fatalf("new session at new checkpoint: %s/%s", d.Status, d.Reason)
	}
}

// 场景三：延迟到达的撤销。
func TestReplayDelayedRevocation(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})

	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	sendBeforeRemove := b.head()
	commitment := dev.commitment(0, 1)
	other := makeDevice(t, "acct-1", "device-other", "Other", []Hash{domainHash("o0")})
	b.appendEvent(other.addEvent(EventAdd))
	b.appendEvent(dev.addEvent(EventRemove))
	sendAfterRemove := b.head()

	// 撤销记录尚未到达：事件链断号，只能 pending。
	early := NewAcceptor(anchor, nil)
	b.ingestChain(early)
	ingestDeviceChainAt(t, early, b, dev.device, int(sendBeforeRemove.TreeSize))
	ingestPrekeyProof(t, early, dev, 0, 1)
	req := buildRequest(dev, commitment, sendAfterRemove, []byte("k"))
	wEarly := newWitness(witPriv)
	ingestReceiptBundle(t, early, wEarly, req, makeReceipt(t, dev, req))
	if d := early.Evaluate(req); d.Status != StatusPending {
		t.Fatalf("incomplete chain must stay pending, got %s/%s", d.Status, d.Reason)
	}

	// 撤销事件延迟到达并补齐后：发送检查点已包含删除，明确拒绝。
	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(sendAfterRemove.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 1)
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	if d := a.Evaluate(req); d.Status != StatusRejected || d.Reason != ReasonDeviceRemoved {
		t.Fatalf("complete chain with removal must reject: %s/%s", d.Status, d.Reason)
	}

	// 发送检查点已包含删除事件：明确拒绝。
	reqRemoved := buildRequest(dev, commitment, sendBeforeRemove, []byte("k"))
	removed := NewAcceptor(anchor, nil)
	b.ingestChain(removed)
	ingestDeviceChainAt(t, removed, b, dev.device, int(sendBeforeRemove.TreeSize))
	ingestPrekeyProof(t, removed, dev, 0, 1)
	wRemoved := newWitness(witPriv)
	ingestReceiptBundle(t, removed, wRemoved, reqRemoved, makeReceipt(t, dev, reqRemoved))
	d := removed.Evaluate(reqRemoved)
	if d.Status != StatusAccepted {
		t.Fatalf("session started before removal must survive, got %s/%s", d.Status, d.Reason)
	}
}
