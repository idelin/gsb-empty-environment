package evidence

import (
	"crypto/ed25519"
	"sort"
)

// BoundaryFingerprint 是袋可复用性的唯一依据。它纳入：
//   - 每个节点的完整环境身份（同名摘要跨环境不互换）；
//   - 节点负载摘要、所属袋与依赖边；
//   - 全部见证的身份与公钥（不是仅摘要）；
//   - 当前注册表序列号与注册表摘要（域合并/分裂/撤销都改变代数）。
//
// 只有新旧边界指纹完全相同，袋才允许不重新验证而直接复用。
type BoundaryFingerprint struct {
	Tag          string      `json:"tag"`
	Bag          BagID       `json:"bag"`
	RegistrySeq  uint64      `json:"registry_seq"`
	RegistryHash Digest      `json:"registry_hash"`
	Nodes        []NodeState `json:"nodes"`
}

// ComputeFingerprint 计算某袋在给定注册表代数下的边界指纹。
func ComputeFingerprint(body BagBody, regHash Digest) (Digest, error) {
	nodes := append([]NodeState(nil), body.Nodes...)
	for i := range nodes {
		sortWitnesses(nodes[i].Witnesses)
		sortNodeIDs(nodes[i].Deps)
	}
	sortNodeStates(nodes)
	fp := BoundaryFingerprint{
		Tag: tagBoundary, Bag: body.Bag, RegistrySeq: body.RegistrySeq,
		RegistryHash: regHash, Nodes: nodes,
	}
	return hashCanon(fp)
}

// ReuseRecord 是未重新验证袋的复用披露：第三方可独立复核指纹一致。
type ReuseRecord struct {
	Bag         BagID  `json:"bag"`
	OldBodyHash Digest `json:"old_body_hash"`
	NewBodyHash Digest `json:"new_body_hash"` // 复用即主体未变，二者相等
	Fingerprint Digest `json:"fingerprint"`
	RegistrySeq uint64 `json:"registry_seq"`
}

// RecomputedBag 是被重算袋的完整披露。
type RecomputedBag struct {
	OldCert          *BagCert  `json:"old_cert,omitempty"`
	NewCert          BagCert   `json:"new_cert"`
	Fingerprint      Digest    `json:"fingerprint"`
	RemovedWitnesses []Witness `json:"removed_witnesses,omitempty"`
}

// Delta 是从旧证书到新证书的最小披露差分。未变化的袋既不在 Recomputed
// 中重复披露主体，也不要求重验；只在 Reused 中给出可复核的复用依据。
type Delta struct {
	Epoch           uint64          `json:"epoch"`
	OldRoot         SignedRoot      `json:"old_root"`
	NewRoot         SignedRoot      `json:"new_root"`
	OldRegistryHash Digest          `json:"old_registry_hash"`
	NewRegistryHash Digest          `json:"new_registry_hash"`
	Patches         []Envelope      `json:"patches"`
	Recomputed      []RecomputedBag `json:"recomputed"`
	Reused          []ReuseRecord   `json:"reused"`
	AffectedNodes   []NodeID        `json:"affected_nodes"`
	AncestorNodes   []NodeID        `json:"ancestor_nodes"`
	RecomputeCount  int             `json:"recompute_count"`
	TotalBags       int             `json:"total_bags"`
	SignatureChecks int             `json:"signature_checks"`
	HashOps         int             `json:"hash_ops"`
}

// UpdateInput 是增量更新的全部输入。
type UpdateInput struct {
	OldRoot     SignedRoot
	Authority   ed25519.PublicKey
	OldRegistry *RegistryState
	OldBags     map[BagID]BagCert
	Patches     []Envelope
	Signer      ed25519.PrivateKey
}

// UpdateResult 返回新根、全部新袋证书与最小差分。
type UpdateResult struct {
	NewRoot SignedRoot
	NewBags map[BagID]BagCert
	Delta   Delta
}

// verifyOldAnchor 校验旧根签名、注册表摘要与袋证书 Merkle 锚点。
func verifyOldAnchor(in UpdateInput) error {
	if err := VerifyRoot(in.OldRoot, in.Authority); err != nil {
		return err
	}
	regHash, err := in.OldRegistry.Hash()
	if err != nil {
		return err
	}
	if regHash != in.OldRoot.Body.RegistryHash {
		return ErrRootAnchor
	}
	entries := make([]BagHashEntry, 0, len(in.OldBags))
	for _, c := range in.OldBags {
		h, err := hashCanon(c.Body)
		if err != nil {
			return err
		}
		if h != c.BodyHash {
			return ErrRootAnchor
		}
		entries = append(entries, BagHashEntry{Bag: c.Body.Bag, Hash: h})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Bag < entries[j].Bag })
	if len(entries) != len(in.OldRoot.Body.Bags) {
		return ErrRootAnchor
	}
	for i, e := range entries {
		if e != in.OldRoot.Body.Bags[i] {
			return ErrRootAnchor
		}
	}
	if merkleRoot(entries) != in.OldRoot.Body.MerkleRoot {
		return ErrRootAnchor
	}
	return nil
}

// selectSource 为单个节点选择最优来源并产出登记失效与成本账本。
// 仅线性检查该节点自身的见证，按确定顺序在首个有效见证处停止，
// 不枚举任何跨节点签名组合。
func selectSource(n NodeState, reg *RegistryState) (SourceClaim, int) {
	claim := SourceClaim{Node: n.ID}
	ws := append([]Witness(nil), n.Witnesses...)
	sortWitnesses(ws)
	sigChecks := 0
	var valid []Witness
	for _, w := range ws {
		sigChecks++
		// 环境身份是节点身份的一部分：为另一环境同摘要对象签发的见证
		// 即使签名本身有效，也不能证明本节点。
		if w.Node != n.ID {
			claim.Failures = append(claim.Failures, Failure{
				Domain: w.Domain, Builder: w.Builder, Reason: ErrEnvMismatch.Error(),
			})
			continue
		}
		if err := VerifyWitness(w); err != nil {
			claim.Failures = append(claim.Failures, Failure{
				Domain: w.Domain, Builder: w.Builder, Reason: err.Error(),
			})
			continue
		}
		if !reg.IsWitnessValid(w) {
			reason := "builder revoked or domain retired"
			if r, ok := reg.RevocationReason(w.Builder); ok && r != "" {
				reason = r
			}
			claim.Failures = append(claim.Failures, Failure{
				Domain: w.Domain, Builder: w.Builder, Reason: reason,
			})
			continue
		}
		valid = append(valid, w)
	}
	// 评估全部见证后，按确定顺序取第一个有效者作为最优来源；不枚举
	// 任何跨节点签名组合。
	if len(valid) > 0 {
		chosen := cloneWitness(valid[0])
		claim.Chosen = &chosen
	}
	hashOps := 1 // 来源结论的规范哈希计入
	claim.Unsourced = claim.Chosen == nil
	claim.Cost = CostLedger{
		SigChecks: sigChecks,
		HashOps:   hashOps,
		Weighted:  sigChecks*WeightSignatureCheck + hashOps*WeightHashOp,
	}
	return claim, sigChecks
}

// recomputeBag 在新注册表下重算单个袋，返回新证书、被移除见证与验签次数。
func recomputeBag(old BagCert, reg *RegistryState) (BagCert, []Witness, int, error) {
	body := BagBody{Bag: old.Body.Bag, RegistrySeq: reg.Seq}
	nodes := append([]NodeState(nil), old.Body.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodeLess(nodes[i].ID, nodes[j].ID) })
	for i := range nodes {
		sortNodeIDs(nodes[i].Deps)
		sortWitnesses(nodes[i].Witnesses)
	}
	body.Nodes = nodes
	var removed []Witness
	totalSigChecks := 0
	for _, n := range nodes {
		claim, checks := selectSource(n, reg)
		totalSigChecks += checks
		body.Sources = append(body.Sources, claim)
		for _, w := range n.Witnesses {
			if !reg.IsWitnessValid(w) {
				removed = append(removed, cloneWitness(w))
			}
		}
	}
	sort.Slice(body.Sources, func(i, j int) bool { return nodeLess(body.Sources[i].Node, body.Sources[j].Node) })
	h, err := hashCanon(body)
	if err != nil {
		return BagCert{}, nil, 0, err
	}
	sortWitnesses(removed)
	return BagCert{Body: body, BodyHash: h}, removed, totalSigChecks, nil
}

func sortedNodeKeys(m map[nodeKey]bool) []nodeKey {
	out := make([]nodeKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.env != b.env {
			return a.env < b.env
		}
		return digestLess(a.payload, b.payload)
	})
	return out
}
