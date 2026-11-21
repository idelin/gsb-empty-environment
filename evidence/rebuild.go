package evidence

import "sort"

// RebuildResult 是全量重建结果，作为增量更新的对照基准。
type RebuildResult struct {
	NewRoot SignedRoot
	NewBags map[BagID]BagCert
	// PerBagChecks 记录每个袋的验签尝试次数，用于与增量结果逐袋对照。
	PerBagChecks    map[BagID]int
	SignatureChecks int
}

// FullRebuild 忽略任何复用路径，从旧证书与同一批补丁出发，对所有袋
// 重新验证。它与 Update 共享补丁语义，但强制全图重算，供测试对照：
// 增量与全量产出的根、袋摘要、来源结论与成本必须逐字节一致。
func FullRebuild(in UpdateInput) (RebuildResult, error) {
	if err := verifyOldAnchor(in); err != nil {
		return RebuildResult{}, err
	}

	workNodes := indexBags(in.OldBags)
	reg := in.OldRegistry.Clone()
	regHash, err := reg.Hash()
	if err != nil {
		return RebuildResult{}, err
	}
	expectedSeq := in.OldRoot.Body.PatchSeq + 1
	for _, env := range in.Patches {
		if err := VerifyEnvelope(env, in.Authority); err != nil {
			return RebuildResult{}, err
		}
		if env.Seq != expectedSeq {
			return RebuildResult{}, ErrPatchSequence
		}
		expectedSeq++
		if env.Gov != nil {
			if env.PrevRegHash != regHash {
				return RebuildResult{}, ErrRegistryHash
			}
			next, err := reg.Apply(*env.Gov)
			if err != nil {
				return RebuildResult{}, err
			}
			reg = next
			regHash, err = reg.Hash()
			if err != nil {
				return RebuildResult{}, err
			}
			continue
		}
		if _, err := applyNodePatch(workNodes, *env.Node); err != nil {
			return RebuildResult{}, err
		}
	}

	newBagNodes := map[BagID][]NodeState{}
	for _, n := range workNodes {
		newBagNodes[n.Bag] = append(newBagNodes[n.Bag], n)
	}
	newBags := map[BagID]BagCert{}
	perBag := map[BagID]int{}
	totalChecks := 0

	bagIDs := make([]BagID, 0, len(newBagNodes))
	for bag := range newBagNodes {
		bagIDs = append(bagIDs, bag)
	}
	sort.Slice(bagIDs, func(i, j int) bool { return bagIDs[i] < bagIDs[j] })

	for _, bag := range bagIDs {
		nodes := newBagNodes[bag]
		sort.Slice(nodes, func(i, j int) bool { return nodeLess(nodes[i].ID, nodes[j].ID) })
		synthetic := BagCert{Body: BagBody{Bag: bag, Nodes: nodes, RegistrySeq: reg.Seq}}
		cert, _, checks, err := recomputeBag(synthetic, reg)
		if err != nil {
			return RebuildResult{}, err
		}
		newBags[bag] = cert
		perBag[bag] = checks
		totalChecks += checks
	}

	patchSeq := in.OldRoot.Body.PatchSeq
	if len(in.Patches) > 0 {
		patchSeq = in.Patches[len(in.Patches)-1].Seq
	}
	body, err := assembleRoot(in.OldRoot.Body.Epoch+1, newBags, reg, patchSeq)
	if err != nil {
		return RebuildResult{}, err
	}
	signed, err := SignRoot(body, in.Signer)
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{
		NewRoot: signed, NewBags: newBags,
		PerBagChecks: perBag, SignatureChecks: totalChecks,
	}, nil
}
