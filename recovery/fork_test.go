package recovery

import "testing"

// 场景四：目录冲突后缀；两个分支后来各有不同消费回执时判定双重消费。
func TestReplayForkBranches(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})

	common := newBranch(dirPriv)
	common.appendEvent(dev.addEvent(EventAdd))
	send := common.head()
	commitment := dev.commitment(0, 0)

	forkOne := common.forkFrom(1)
	forkOne.appendEvent(dev.rotateEvent(t, 1, []Hash{domainHash("fork1-c")}))
	forkTwo := common.forkFrom(1)
	other := makeDevice(t, "acct-1", "device-A2", "Second Alice",
		[]Hash{domainHash("d0")})
	forkTwo.appendEvent(other.addEvent(EventAdd))

	a := NewAcceptor(anchor, nil)
	common.ingestChain(a)
	forkOne.ingestChain(a)
	forkTwo.ingestChain(a)
	ingestDeviceChainAt(t, a, common, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 0)

	req := buildRequest(dev, commitment, send, []byte("fork-safe"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	d := a.Evaluate(req)
	if d.Status != StatusAccepted || d.ViableBranches != 2 {
		t.Fatalf("identity identical before fork must accept across 2 branches, got %s/%s branches=%d",
			d.Status, d.Reason, d.ViableBranches)
	}

	// 两个分支后来各带一条内容不同的回执：同一会话摘要被消费两次。
	r1 := makeReceipt(t, dev, req)
	r2 := makeReceipt(t, dev, req)
	r2.SendTreeSize = send.TreeSize + 1
	r2 = signReceipt(dev.priv, r2)

	double := NewAcceptor(anchor, nil)
	common.ingestChain(double)
	forkOne.ingestChain(double)
	forkTwo.ingestChain(double)
	ingestDeviceChainAt(t, double, common, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, double, dev, 0, 0)

	wd := newWitness(witPriv)
	wd.addReceipt(r1)
	wd.addReceipt(r2)
	cp := wd.checkpoint(t)
	sp := wd.proof(req.SessionDigest)
	mustIngest(t, double, Record{Kind: RecReceiptCheckpoint, ReceiptCheckpoint: &cp})
	mustIngest(t, double, Record{Kind: RecSparseProof, SparseProof: &sp})
	mustIngest(t, double, Record{Kind: RecReceipt, Receipt: &r1})
	mustIngest(t, double, Record{Kind: RecReceipt, Receipt: &r2})

	dd := double.Evaluate(req)
	if dd.Status != StatusRejected || dd.Reason != ReasonDoubleSpend {
		t.Fatalf("different receipts across branches must be double-spend, got %s/%s", dd.Status, dd.Reason)
	}
}

// 分叉但发送时身份一致时，导出的最小 Bundle 仍可独立验证且不可再删。
func TestForkedBundleIsMinimal(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone", []Hash{domainHash("c0")})
	common := newBranch(dirPriv)
	common.appendEvent(dev.addEvent(EventAdd))
	send := common.head()
	forkOne := common.forkFrom(1)
	forkOne.appendEvent(dev.rotateEvent(t, 1, []Hash{domainHash("x")}))
	forkTwo := common.forkFrom(1)
	other := makeDevice(t, "acct-1", "device-A2", "Second", []Hash{domainHash("d")})
	forkTwo.appendEvent(other.addEvent(EventAdd))

	a := NewAcceptor(anchor, nil)
	common.ingestChain(a)
	forkOne.ingestChain(a)
	forkTwo.ingestChain(a)
	ingestDeviceChainAt(t, a, common, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 0)
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("min-fork"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	decision := a.Evaluate(req)
	if decision.Status != StatusAccepted || decision.ViableBranches != 2 {
		t.Fatalf("setup: %s/%s branches=%d", decision.Status, decision.Reason, decision.ViableBranches)
	}
	bundle, err := a.ExportBundle(req, decision)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	independent, err := VerifyBundle(bundle)
	if err != nil || independent.Status != StatusAccepted {
		t.Fatalf("independent verify: %v %+v", err, independent)
	}
	for i := range bundle.Records {
		stripped := Bundle{Trust: bundle.Trust, Request: bundle.Request}
		for j, r := range bundle.Records {
			if i != j {
				stripped.Records = append(stripped.Records, r)
			}
		}
		again, err := VerifyBundle(stripped)
		if err == nil && again.Status == StatusAccepted {
			t.Fatalf("record %d (%s) removable; not minimal", i, bundle.Records[i].Kind)
		}
	}
}

// 缺少包含证明保持 pending，与确有双重消费的 rejected 明确分开。
func TestMissingProofVsDoubleSpend(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("k"))

	missing := NewAcceptor(anchor, nil)
	b.ingestChain(missing)
	ingestDeviceChainAt(t, missing, b, dev.device, int(send.TreeSize))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, missing, w, req, makeReceipt(t, dev, req))
	d := missing.Evaluate(req)
	if d.Status != StatusPending || d.Reason != ReasonMissingPrekeyProof {
		t.Fatalf("missing prekey proof must be pending, got %s/%s", d.Status, d.Reason)
	}

	ingestPrekeyProof(t, missing, dev, 0, 0)
	if d = missing.Evaluate(req); d.Status != StatusAccepted {
		t.Fatalf("completion should accept, got %s/%s", d.Status, d.Reason)
	}
}

// 回执先于目录前缀到达：先 pending，补齐目录记录后自动可验收。
func TestReceiptArrivesBeforeDirectoryPrefix(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("late-prefix"))

	a := NewAcceptor(anchor, nil)
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	if d := a.Evaluate(req); d.Status != StatusPending || d.Reason != ReasonMissingCheckpoint {
		t.Fatalf("receipt first must wait for directory prefix, got %s/%s", d.Status, d.Reason)
	}

	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(send.TreeSize))
	if d := a.Evaluate(req); d.Status != StatusPending || d.Reason != ReasonMissingPrekeyProof {
		t.Fatalf("still missing prekey proof, got %s/%s", d.Status, d.Reason)
	}
	ingestPrekeyProof(t, a, dev, 0, 0)
	if d := a.Evaluate(req); d.Status != StatusAccepted {
		t.Fatalf("after prefix arrives should accept, got %s/%s", d.Status, d.Reason)
	}
}

// 重新注册同名设备必须得到新实例标识，且不能继承旧实例的密钥授权。
func TestReregisterSameNameDoesNotInheritKeys(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	old := makeDevice(t, "acct-1", "device-old", "Alice Phone",
		[]Hash{domainHash("old-c")})
	b := newBranch(dirPriv)
	b.appendEvent(old.addEvent(EventAdd))
	b.appendEvent(old.addEvent(EventRemove))

	fresh := makeDevice(t, "acct-1", "device-new", "Alice Phone",
		[]Hash{domainHash("new-c")})
	if equalHash(Hash(old.identityPK()), Hash(fresh.identityPK())) {
		t.Fatalf("fresh instance must not inherit old identity key")
	}
	b.appendEvent(fresh.addEvent(EventAdd))
	send := b.head()

	// 旧实例承诺不能借新实例的注册恢复授权。
	oldReq := buildRequest(old, old.commitment(0, 0), send, []byte("old"))
	oldAcceptor := NewAcceptor(anchor, nil)
	b.ingestChain(oldAcceptor)
	ingestDeviceChainAt(t, oldAcceptor, b, old.device, int(send.TreeSize))
	ingestPrekeyProof(t, oldAcceptor, old, 0, 0)
	wOld := newWitness(witPriv)
	ingestReceiptBundle(t, oldAcceptor, wOld, oldReq, makeReceipt(t, old, oldReq))
	if d := oldAcceptor.Evaluate(oldReq); d.Status != StatusRejected || d.Reason != ReasonDeviceRemoved {
		t.Fatalf("old instance must stay removed, got %s/%s", d.Status, d.Reason)
	}

	// 新实例独立授权。
	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, fresh.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, fresh, 0, 0)
	req := buildRequest(fresh, fresh.commitment(0, 0), send, []byte("fresh"))
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, fresh, req))
	if d := a.Evaluate(req); d.Status != StatusAccepted {
		t.Fatalf("fresh instance should be authorized, got %s/%s", d.Status, d.Reason)
	}
}

// 导出的 Bundle 必须能独立验证，且去掉任何一条记录都会失去某项证明。
func TestBundleIsIndependentlyVerifiableAndMinimal(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 1), send, []byte("exported"))

	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 1)
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	decision := a.Evaluate(req)
	if decision.Status != StatusAccepted {
		t.Fatalf("setup: %s/%s", decision.Status, decision.Reason)
	}

	bundle, err := a.ExportBundle(req, decision)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(bundle.Records) == 0 {
		t.Fatalf("bundle must contain records")
	}

	// 独立验证：不传入原 Acceptor 的任何内存状态。
	independent, err := VerifyBundle(bundle)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	if independent.Status != StatusAccepted {
		t.Fatalf("independent verification: %s/%s", independent.Status, independent.Reason)
	}

	// 最小性：逐条删除，任何一条被删都不得再 accepted。
	for i := range bundle.Records {
		stripped := Bundle{Trust: bundle.Trust, Request: bundle.Request}
		for j, r := range bundle.Records {
			if i != j {
				stripped.Records = append(stripped.Records, r)
			}
		}
		again, err := VerifyBundle(stripped)
		if err == nil && again.Status == StatusAccepted {
			t.Fatalf("record %d (%s) is removable; bundle not minimal", i, bundle.Records[i].Kind)
		}
	}
}
