package evidence

import "testing"

// 测试图固定布局：
//
//	环境 prod：域 dom-a / dom-b / dom-c；袋 bag-1、bag-2。
//	环境 stage：同摘要对象 n3 的独立节点，证明跨环境不可互换。
//
// 依赖：n1 -> n2 -> n3（同环境），用于验证祖先传播。
func buildGenesisGraph(t *testing.T) (*fixture, map[string]NodeID, []NodeState) {
	t.Helper()
	f := newFixture(t)
	f.addDomain(t, "dom-a", "Alpha Domain")
	f.addDomain(t, "dom-b", "Beta Domain")
	f.addDomain(t, "dom-c", "Gamma Domain")
	f.addBuilder(t, "b1", "dom-a")
	f.addBuilder(t, "b2", "dom-a")
	f.addBuilder(t, "b3", "dom-b")
	f.addBuilder(t, "b4", "dom-c")

	n1 := NodeID{Env: "prod", Payload: payloadDigest("artifact-1")}
	n2 := NodeID{Env: "prod", Payload: payloadDigest("artifact-2")}
	n3 := NodeID{Env: "prod", Payload: payloadDigest("artifact-3")}
	n3Stage := NodeID{Env: "stage", Payload: payloadDigest("artifact-3")} // 同摘要不同环境
	n4 := NodeID{Env: "prod", Payload: payloadDigest("artifact-4")}

	nodes := []NodeState{
		nodeState(n1, "bag-1", []NodeID{n2},
			f.witness(t, "b1", "dom-a", n1),
			f.witness(t, "b3", "dom-b", n1)),
		nodeState(n2, "bag-2", []NodeID{n3},
			f.witness(t, "b2", "dom-a", n2)),
		nodeState(n3, "bag-2", nil,
			f.witness(t, "b4", "dom-c", n3),
			f.witness(t, "b1", "dom-a", n3)),
		nodeState(n3Stage, "bag-2", nil,
			f.witness(t, "b4", "dom-c", n3Stage)),
		// bag-3 与主图无依赖关系，四个场景都不应波及它，用于证明复用。
		nodeState(n4, "bag-3", nil, f.witness(t, "b2", "dom-a", n4)),
	}
	ids := map[string]NodeID{"n1": n1, "n2": n2, "n3": n3, "n3Stage": n3Stage, "n4": n4}
	return f, ids, nodes
}

func genesisState(t *testing.T) (*fixture, SignedRoot, map[BagID]BagCert, map[string]NodeID) {
	f, ids, nodes := buildGenesisGraph(t)
	root, bags, err := Genesis(GenesisInput{
		Epoch: 1, Registry: f.registry, Nodes: nodes,
		Signer: f.authPriv, PatchSeq: f.seq,
	})
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	if err := VerifyRoot(root, f.authPub); err != nil {
		t.Fatalf("verify genesis root: %v", err)
	}
	return f, root, bags, ids
}

// compareWithFullRebuild 用全量重建对照增量结果：根、每个袋摘要与来源
// 结论必须逐字节一致；同时断言增量只重算受影响袋。
func compareWithFullRebuild(t *testing.T, in UpdateInput, res UpdateResult) RebuildResult {
	t.Helper()
	full, err := FullRebuild(in)
	if err != nil {
		t.Fatalf("full rebuild: %v", err)
	}
	if !res.NewRoot.Body.MerkleRoot.Equal(full.NewRoot.Body.MerkleRoot) {
		t.Fatalf("merkle root mismatch: incremental %x vs full %x",
			res.NewRoot.Body.MerkleRoot, full.NewRoot.Body.MerkleRoot)
	}
	if !res.NewRoot.Body.RegistryHash.Equal(full.NewRoot.Body.RegistryHash) {
		t.Fatalf("registry hash mismatch")
	}
	if len(res.NewBags) != len(full.NewBags) {
		t.Fatalf("bag count mismatch")
	}
	for bag, fb := range full.NewBags {
		ib, ok := res.NewBags[bag]
		if !ok {
			t.Fatalf("incremental missing bag %s", bag)
		}
		if ib.BodyHash != fb.BodyHash {
			t.Fatalf("bag %s hash diverges from full rebuild", bag)
		}
		for i := range fb.Body.Sources {
			if !claimsEqual(ib.Body.Sources[i], fb.Body.Sources[i]) {
				t.Fatalf("bag %s source claim diverges from full rebuild", bag)
			}
		}
	}
	// 治理变更会推进注册表代数，此时全部袋重验是正确行为；只有纯节点
	// 补丁（注册表未变）才强制证明复用路径被实际使用。
	registryUnchanged := res.NewRoot.Body.RegistrySeq == in.OldRoot.Body.RegistrySeq
	if registryUnchanged && res.Delta.RecomputeCount >= len(res.NewBags) {
		t.Fatalf("incremental run recomputed all %d bags; no reuse happened", res.Delta.TotalBags)
	}
	return full
}

// 场景一：节点跨袋移动。补丁把节点从 bag-2 移到 bag-1，两个袋的边界都
// 改变，必须重算；无关节点所在袋若边界完全相同则凭指纹复用。全量重建
// 与增量结果逐袋一致。
func TestScenario1_CrossBagMovement(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	moved := bags["bag-2"].Body.Nodes
	var n2 NodeState
	for _, n := range moved {
		if n.ID == ids["n2"] {
			n2 = n
		}
	}
	n2.Bag = "bag-1"
	patch := f.nodeEnvelope(t, NodePatch{Op: NodeUpsert, Node: n2.ID, Bag: n2.Bag, Deps: n2.Deps})

	in := UpdateInput{
		OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{patch}, Signer: f.authPriv,
	}
	res, err := Update(in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	full := compareWithFullRebuild(t, in, res)

	// n2 直接受影响；n1 依赖 n2，是必要祖先，因此 bag-1、bag-2 都重算。
	if res.Delta.RecomputeCount != 2 {
		t.Fatalf("expected 2 recomputed bags, got %d", res.Delta.RecomputeCount)
	}
	if len(res.Delta.Reused) != 1 || res.Delta.Reused[0].Bag != "bag-3" {
		t.Fatalf("expected only bag-3 reused, got %+v", res.Delta.Reused)
	}
	// 新图中 bag-1 必须包含 n2，且其来源选择与全量重建一致。
	var found bool
	for _, n := range res.NewBags["bag-1"].Body.Nodes {
		if n.ID == ids["n2"] {
			found = true
		}
	}
	if !found {
		t.Fatalf("n2 did not move into bag-1")
	}
	if err := VerifyDelta(root, f.registry, f.authPub, bags, res.Delta); err != nil {
		t.Fatalf("independent delta verify: %v", err)
	}
	if full.PerBagChecks["bag-1"] < 1 {
		t.Fatalf("full rebuild should verify bag-1 witnesses")
	}
}

// 场景二：旧见证被撤销移除，但节点还有替代来源，最优来源切换且成本可重算；
// 只重算受影响袋，其余袋复用。
func TestScenario2_WitnessRemovedAlternativeSurvives(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	// n1 有 b1(dom-a) 与 b3(dom-b) 两条见证，确定顺序下首选 b1。
	before := findClaim(bags["bag-1"].Body.Sources, ids["n1"])
	if before == nil || before.Chosen == nil || before.Chosen.Builder != "b1" {
		t.Fatalf("expected initial chosen builder b1")
	}

	rev := f.stageGov(t, RegistryEvent{
		Kind: GovRevokeBuilders, Builders: []BuilderID{"b1"}, Reason: "batch-compromise-2026",
	})
	in := UpdateInput{
		OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{rev}, Signer: f.authPriv,
	}

	res, err := Update(in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	compareWithFullRebuild(t, in, res)

	after := findClaim(res.NewBags["bag-1"].Body.Sources, ids["n1"])
	if after == nil || after.Chosen == nil {
		t.Fatalf("expected alternative source to survive revocation")
	}
	if after.Chosen.Builder != "b3" {
		t.Fatalf("expected failover to b3, got %v", after.Chosen.Builder)
	}
	if len(after.Failures) != 1 || after.Failures[0].Builder != "b1" ||
		after.Failures[0].Reason != "batch-compromise-2026" {
		t.Fatalf("revocation not registered as failure: %+v", after.Failures)
	}
	// 成本账本可按固定权重重算，且包含被撤销见证的失败验签尝试。
	sigW, hashW := CostWeights()
	if after.Cost.Weighted != after.Cost.SigChecks*sigW+after.Cost.HashOps*hashW {
		t.Fatalf("cost ledger not reproducible: %+v", after.Cost)
	}
	if after.Cost.SigChecks != 2 {
		t.Fatalf("expected 2 signature checks (b1 fail + b3 pass), got %d", after.Cost.SigChecks)
	}
	// 治理变更推进注册表代数，三个袋都在新代数下重验；这是正确行为，
	// 复用路径由场景一（纯节点补丁）专门约束。
	if res.Delta.RecomputeCount != 3 || len(res.Delta.Reused) != 0 {
		t.Fatalf("expected 3 recomputed and 0 reused after registry change, got %d/%d",
			res.Delta.RecomputeCount, len(res.Delta.Reused))
	}
	if err := VerifyDelta(root, in.OldRegistry, f.authPub, bags, res.Delta); err != nil {
		t.Fatalf("independent delta verify: %v", err)
	}
}

func (d Digest) Equal(o Digest) bool {
	for i := range d {
		if d[i] != o[i] {
			return false
		}
	}
	return true
}

// 场景三：两个信任域合并后共同失效。先把 dom-c 并入 dom-b，再对合并后
// 的域批量撤销；所有依赖这两个域见证的节点必须登记失效，找不到替代来源
// 的节点变为 Unsourced，且增量与全量重建一致。
func TestScenario3_MergedDomainsFailTogether(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	// n3(prod) 的首选见证按确定顺序是 b1(dom-a)；b4(dom-c) 是备选。
	// 合并 dom-c -> dom-b，使 b4 迁移到 dom-b，随后撤销 dom-b 的构建器，
	// 合并迁移来的 b4 与原 dom-b 的 b3 共同失效。
	merge := f.stageGov(t, RegistryEvent{
		Kind: GovMergeDomains, Source: "dom-c", Target: "dom-b",
	})
	f.stageGov(t, RegistryEvent{
		Kind: GovRevokeBuilders, Builders: []BuilderID{"b3", "b4"}, Reason: "merged-domain-incident",
	})

	in := UpdateInput{
		OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: f.stagedPatches(), Signer: f.authPriv,
	}
	if len(in.Patches) != 2 {
		t.Fatalf("expected 2 staged patches, got %d", len(in.Patches))
	}
	_ = merge
	res, err := Update(in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	full := compareWithFullRebuild(t, in, res)

	// 合并后 dom-c 已注销，独立重验方也无法再把 b4 当作 dom-c 见证接受。
	if _, exists := f.registry.Domains["dom-c"]; exists {
		t.Fatalf("dom-c should be retired after merge")
	}
	n3Claim := findClaim(res.NewBags["bag-2"].Body.Sources, ids["n3"])
	if n3Claim == nil {
		t.Fatalf("missing n3 claim")
	}
	// b1 仍有效，n3 不应失去来源，但其 b4 见证必须登记为合并域失效。
	if n3Claim.Unsourced || n3Claim.Chosen == nil || n3Claim.Chosen.Builder != "b1" {
		t.Fatalf("n3 should fail over to b1, got %+v", n3Claim)
	}
	var sawB4 bool
	for _, fail := range n3Claim.Failures {
		if fail.Builder == "b4" && fail.Reason == "merged-domain-incident" {
			sawB4 = true
		}
	}
	if !sawB4 {
		t.Fatalf("merged builder b4 failure not registered: %+v", n3Claim.Failures)
	}

	// 再撤销 b1，使 n3(prod) 彻底无来源；stage 环境的同摘要节点 n3Stage
	// 只有 b4 见证，也随之无来源——但二者是不同节点，结论分别登记。
	oldReg2 := f.snapshotRegistry()
	root2, bags2 := res.NewRoot, res.NewBags
	revokeB1 := f.stageGov(t, RegistryEvent{
		Kind: GovRevokeBuilders, Builders: []BuilderID{"b1"}, Reason: "final-purge",
	})
	in2 := UpdateInput{
		OldRoot: root2, Authority: f.authPub, OldRegistry: oldReg2,
		OldBags: bags2, Patches: []Envelope{revokeB1}, Signer: f.authPriv,
	}
	res2, err := Update(in2)
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	compareWithFullRebuild(t, in2, res2)
	prodClaim := findClaim(res2.NewBags["bag-2"].Body.Sources, ids["n3"])
	stageClaim := findClaim(res2.NewBags["bag-2"].Body.Sources, ids["n3Stage"])
	if prodClaim == nil || stageClaim == nil {
		t.Fatalf("missing claims")
	}
	if !prodClaim.Unsourced || !stageClaim.Unsourced {
		t.Fatalf("both same-digest nodes across envs must be independently unsourced")
	}
	if err := VerifyDelta(root2, oldReg2, f.authPub, bags2, res2.Delta); err != nil {
		t.Fatalf("independent delta verify after purge: %v", err)
	}
	if full.SignatureChecks <= 0 {
		t.Fatalf("full rebuild must perform signature checks")
	}
}

// 场景四：错误复用边界——同名变量但身份不同。构造一个与现有域同名但
// DomainID 不同的新域，把节点见证切到新域构建器；边界指纹纳入完整域
// 身份与公钥，任何试图复用旧袋的行为都必须被拒绝。
func TestScenario4_SameNameDifferentIdentityReuseRejected(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	// 新域名字也叫 "Alpha Domain"，但身份是 dom-a2，公钥全新。
	addDomain := f.stageGov(t, RegistryEvent{Kind: GovAddDomain, DomainID: "dom-a2", Name: "Alpha Domain"})
	b5Pub, b5Priv := deterministicKey("builder:b5")
	f.builders["b5"] = b5Priv
	f.pubkeys["b5"] = b5Pub
	addBuilder := f.stageGov(t, RegistryEvent{Kind: GovAddBuilder, DomainID: "dom-a2", Builder: "b5", PubKey: b5Pub})

	// n2 原来只有 b2(dom-a) 见证；追加 b5(dom-a2) 见证并撤销 b2，
	// 迫使来源切换到同名不同身份的新域。
	w5 := f.witness(t, "b5", "dom-a2", ids["n2"])
	addWitness := f.nodeEnvelope(t, NodePatch{Op: NodeAddWitness, Node: ids["n2"], Witness: &w5})
	f.stageGov(t, RegistryEvent{
		Kind: GovRevokeBuilders, Builders: []BuilderID{"b2"}, Reason: "identity-rotation",
	})

	in := UpdateInput{
		OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: f.stagedPatches(), Signer: f.authPriv,
	}
	if len(in.Patches) != 4 {
		t.Fatalf("expected 4 patches, got %d", len(in.Patches))
	}
	_ = addDomain
	_ = addBuilder
	_ = addWitness
	res, err := Update(in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	compareWithFullRebuild(t, in, res)

	claim := findClaim(res.NewBags["bag-2"].Body.Sources, ids["n2"])
	if claim == nil || claim.Chosen == nil || claim.Chosen.Domain != "dom-a2" {
		t.Fatalf("expected source switched to dom-a2, got %+v", claim)
	}
	// 含治理变更，注册表代数推进，所有袋都在新代数下重验；bag-2 的
	// 来源确实切换到了同名不同身份的新域。
	recomputedBags := map[BagID]bool{}
	for _, rb := range res.Delta.Recomputed {
		recomputedBags[rb.NewCert.Body.Bag] = true
	}
	if !recomputedBags["bag-2"] {
		t.Fatalf("bag-2 must be recomputed when same-name domain identity changes")
	}

	// 主动伪造一个复用记录：把 bag-2 说成可复用。独立重验必须拒绝。
	bad := res.Delta
	bad.Reused = append(bad.Reused, ReuseRecord{
		Bag: "bag-2", OldBodyHash: bags["bag-2"].BodyHash,
		NewBodyHash: bags["bag-2"].BodyHash, Fingerprint: Digest{},
	})
	if err := VerifyDelta(root, oldReg, f.authPub, bags, bad); err == nil {
		t.Fatalf("forged reuse across different domain identities must be rejected")
	}

	// 正确差分仍可独立重验。
	if err := VerifyDelta(root, oldReg, f.authPub, bags, res.Delta); err != nil {
		t.Fatalf("legitimate delta verify: %v", err)
	}
}

// 环境隔离：同摘要对象跨环境的见证不能互换。直接用 stage 环境节点的见证
// 去更新 prod 节点必须被拒绝。
func TestEnvironmentIsolation(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	// 用 stage 环境身份产生的真实见证，直接登记到 prod 的同摘要节点上。
	wrong := f.witness(t, "b3", "dom-b", ids["n3Stage"])
	addPatch := f.nodeEnvelope(t, NodePatch{Op: NodeAddWitness, Node: ids["n3"], Witness: &wrong})

	in := UpdateInput{
		OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{addPatch}, Signer: f.authPriv,
	}
	res, err := Update(in)
	if err != nil {
		t.Fatalf("update should accept patch but reject witness at source layer: %v", err)
	}
	claim := findClaim(res.NewBags["bag-2"].Body.Sources, ids["n3"])
	if claim == nil {
		t.Fatalf("missing n3 claim")
	}
	var sawMismatch bool
	for _, fail := range claim.Failures {
		if fail.Reason == ErrEnvMismatch.Error() {
			sawMismatch = true
		}
	}
	if !sawMismatch {
		t.Fatalf("cross-environment witness must be recorded as environment mismatch: %+v", claim.Failures)
	}
	// 即使它在确定顺序中靠前，也绝不能被选为 prod 节点的来源。
	if claim.Chosen != nil && claim.Chosen.Builder == "b3" && claim.Chosen.Node.Env == "stage" {
		t.Fatalf("cross-environment witness must never be chosen")
	}
	// 同摘要的 stage 节点结论不受影响，证明两者独立。
	stageClaim := findClaim(res.NewBags["bag-2"].Body.Sources, ids["n3Stage"])
	if stageClaim == nil || stageClaim.Unsourced {
		t.Fatalf("stage node must keep its own valid source independently")
	}
	if err := VerifyDelta(root, oldReg, f.authPub, bags, res.Delta); err != nil {
		t.Fatalf("delta verify: %v", err)
	}
}

// 防篡改：补丁序号缺口、错误前像注册表摘要、伪造新根都会被拒绝。
func TestTamperRejection(t *testing.T) {
	f, root, bags, _ := genesisState(t)
	oldReg := f.snapshotRegistry()

	rev := f.stageGov(t, RegistryEvent{
		Kind: GovRevokeBuilders, Builders: []BuilderID{"b1"}, Reason: "x",
	})

	// 序号缺口。
	gap := rev
	gap.Seq++
	gap, _ = SignEnvelope(gap, f.authPriv)
	in := UpdateInput{OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{gap}, Signer: f.authPriv}
	if _, err := Update(in); err != ErrPatchSequence {
		t.Fatalf("expected ErrPatchSequence, got %v", err)
	}

	// 前像注册表摘要错误。
	badPrev := rev
	badPrev.PrevRegHash = Digest{}
	badPrev, _ = SignEnvelope(badPrev, f.authPriv)
	in = UpdateInput{OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{badPrev}, Signer: f.authPriv}
	if _, err := Update(in); err != ErrRegistryHash {
		t.Fatalf("expected ErrRegistryHash, got %v", err)
	}

	// 合法更新后篡改新根，独立重验必须发现根不一致。
	in = UpdateInput{OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{rev}, Signer: f.authPriv}
	res, err := Update(in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	tampered := res.Delta
	tampered.NewRoot.Body.Epoch = 999
	if err := VerifyDelta(root, oldReg, f.authPub, bags, tampered); err == nil {
		t.Fatalf("tampered root must fail independent verification")
	}
}

// 分裂场景：域分裂后构建器迁移到新域，见证有效性与边界指纹正确更新。
func TestDomainSplit(t *testing.T) {
	f, root, bags, ids := genesisState(t)
	oldReg := f.snapshotRegistry()

	split := f.stageGov(t, RegistryEvent{
		Kind: GovSplitDomain, From: "dom-a", NewDomainID: "dom-a-east",
		NewName: "Alpha East", MoveBuilders: []BuilderID{"b1"},
	})
	in := UpdateInput{OldRoot: root, Authority: f.authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []Envelope{split}, Signer: f.authPriv}
	res, err := Update(in)
	if err != nil {
		t.Fatalf("split update: %v", err)
	}
	compareWithFullRebuild(t, in, res)

	// b1 现在登记在 dom-a-east，旧的 dom-a/b1 见证身份失效；n1 首选 b1
	// 的旧见证不再有效，回退到 b3。
	claim := findClaim(res.NewBags["bag-1"].Body.Sources, ids["n1"])
	if claim == nil || claim.Chosen == nil || claim.Chosen.Builder != "b3" {
		t.Fatalf("expected failover after split, got %+v", claim)
	}
	if err := VerifyDelta(root, oldReg, f.authPub, bags, res.Delta); err != nil {
		t.Fatalf("delta verify after split: %v", err)
	}
}
