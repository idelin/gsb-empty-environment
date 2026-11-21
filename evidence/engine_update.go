package evidence

import "sort"

// assembleRoot 从袋证书集合与注册表确定性地组装根主体（不签名）。
func assembleRoot(epoch uint64, bags map[BagID]BagCert, reg *RegistryState, patchSeq uint64) (RootBody, error) {
	entries := make([]BagHashEntry, 0, len(bags))
	for _, c := range bags {
		h, err := hashCanon(c.Body)
		if err != nil {
			return RootBody{}, err
		}
		if h != c.BodyHash {
			return RootBody{}, ErrRootAnchor
		}
		entries = append(entries, BagHashEntry{Bag: c.Body.Bag, Hash: h})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Bag < entries[j].Bag })
	regHash, err := reg.Hash()
	if err != nil {
		return RootBody{}, err
	}
	return RootBody{
		Epoch:        epoch,
		Bags:         entries,
		MerkleRoot:   merkleRoot(entries),
		RegistryHash: regHash,
		RegistrySeq:  reg.Seq,
		PatchSeq:     patchSeq,
	}, nil
}

// Update 执行一次受约束的增量证据更新：
//  1. 锚定旧根（根签名、注册表摘要、袋 Merkle 提交）；
//  2. 严格按序号验证并应用补丁，治理补丁校验前像注册表摘要；
//  3. 求直接受影响节点与必要祖先，定位受影响袋；
//  4. 只重算受影响袋，其余袋用“与新约束边界完全相同”的指纹复用；
//  5. 先独立组装确定性新根并与补丁结果对照，一致后才由根权威签名；
//  6. 输出新证书与最小披露差分。
func Update(in UpdateInput) (UpdateResult, error) {
	if err := verifyOldAnchor(in); err != nil {
		return UpdateResult{}, err
	}

	workNodes := indexBags(in.OldBags)
	oldRev := indexReverseDeps(workNodes)
	reg := in.OldRegistry.Clone()
	regHash, err := reg.Hash()
	if err != nil {
		return UpdateResult{}, err
	}

	direct := map[nodeKey]bool{}
	expectedSeq := in.OldRoot.Body.PatchSeq + 1
	govTouched := false
	for _, env := range in.Patches {
		if err := VerifyEnvelope(env, in.Authority); err != nil {
			return UpdateResult{}, err
		}
		if env.Seq != expectedSeq {
			return UpdateResult{}, ErrPatchSequence
		}
		expectedSeq++

		if env.Gov != nil {
			if env.PrevRegHash != regHash {
				return UpdateResult{}, ErrRegistryHash
			}
			next, err := reg.Apply(*env.Gov)
			if err != nil {
				return UpdateResult{}, err
			}
			reg = next
			regHash, err = reg.Hash()
			if err != nil {
				return UpdateResult{}, err
			}
			govTouched = true
			continue
		}
		if env.Node == nil {
			return UpdateResult{}, ErrPatchSignature
		}
		k, err := applyNodePatch(workNodes, *env.Node)
		if err != nil {
			return UpdateResult{}, err
		}
		direct[k] = true
	}

	// 治理变更后，见证有效性可能改变的节点才进入直接集合：
	// 线性检查各节点自身见证，不枚举签名组合。
	if govTouched {
		for k, n := range workNodes {
			for _, w := range n.Witnesses {
				if !reg.IsWitnessValid(w) {
					direct[k] = true
					break
				}
			}
		}
	}

	affected := closureOnGraph(direct, oldRev)
	ancestors := map[nodeKey]bool{}
	for k := range affected {
		if !direct[k] {
			ancestors[k] = true
		}
	}
	affectedBags := map[BagID]bool{}
	for k := range affected {
		n, ok := workNodes[k]
		if !ok {
			return UpdateResult{}, ErrNodeMissing
		}
		affectedBags[n.Bag] = true
	}
	// 节点补丁可能改变袋归属：节点离开的旧袋边界也已变化，必须重算。
	for _, env := range in.Patches {
		if env.Node == nil || env.Node.Op != NodeUpsert {
			continue
		}
		if old, ok := in.OldBags[bagOf(in.OldBags, env.Node.Node)]; ok {
			affectedBags[old.Body.Bag] = true
		}
		if env.Node.Bag != "" {
			affectedBags[env.Node.Bag] = true
		}
	}

	newBagNodes := map[BagID][]NodeState{}
	for _, n := range workNodes {
		newBagNodes[n.Bag] = append(newBagNodes[n.Bag], n)
	}
	// 注册表代数是约束边界的一部分：任何治理变更（合并/分裂/撤销）后，
	// 所有袋都必须在新代数下重验，不存在“代数变了还能复用”的袋。
	registryAdvanced := reg.Seq != in.OldRoot.Body.RegistrySeq
	if registryAdvanced {
		for bag := range newBagNodes {
			affectedBags[bag] = true
		}
	}

	newBags := map[BagID]BagCert{}
	delta := Delta{
		Epoch:           in.OldRoot.Body.Epoch + 1,
		OldRoot:         in.OldRoot,
		OldRegistryHash: in.OldRoot.Body.RegistryHash,
		NewRegistryHash: regHash,
		Patches:         append([]Envelope(nil), in.Patches...),
		TotalBags:       len(newBagNodes),
	}

	bagIDs := make([]BagID, 0, len(newBagNodes))
	for bag := range newBagNodes {
		bagIDs = append(bagIDs, bag)
	}
	sort.Slice(bagIDs, func(i, j int) bool { return bagIDs[i] < bagIDs[j] })

	for _, bag := range bagIDs {
		nodes := newBagNodes[bag]
		sort.Slice(nodes, func(i, j int) bool { return nodeLess(nodes[i].ID, nodes[j].ID) })

		if !affectedBags[bag] {
			old, ok := in.OldBags[bag]
			if !ok {
				return UpdateResult{}, ErrBagMissing
			}
			// 复用依据：以同一注册表代数为参照，旧证书边界与新事实边界
			// 的指纹必须完全相同。环境身份、见证身份/公钥、域身份、依赖
			// 边任意一项变化都会导致指纹不同而拒绝复用。
			oldFP, err := ComputeFingerprint(old.Body, in.OldRoot.Body.RegistryHash)
			if err != nil {
				return UpdateResult{}, err
			}
			newFP, err := ComputeFingerprint(
				BagBody{Bag: bag, Nodes: nodes, RegistrySeq: old.Body.RegistrySeq},
				in.OldRoot.Body.RegistryHash,
			)
			if err != nil {
				return UpdateResult{}, err
			}
			if oldFP != newFP {
				return UpdateResult{}, ErrReuseBoundary
			}
			delta.Reused = append(delta.Reused, ReuseRecord{
				Bag: bag, OldBodyHash: old.BodyHash, NewBodyHash: old.BodyHash,
				Fingerprint: oldFP, RegistrySeq: old.Body.RegistrySeq,
			})
			newBags[bag] = old
			delta.HashOps += 2
			continue
		}

		old, hadOld := in.OldBags[bag]
		synthetic := BagCert{Body: BagBody{Bag: bag, Nodes: nodes, RegistrySeq: reg.Seq}}
		fp, err := ComputeFingerprint(synthetic.Body, regHash)
		if err != nil {
			return UpdateResult{}, err
		}
		var cert BagCert
		var removed []Witness
		var checks int
		// 始终基于补丁后的节点事实重算，防止袋内容漂移。
		cert, removed, checks, err = recomputeBag(synthetic, reg)
		if err != nil {
			return UpdateResult{}, err
		}
		oldCopy := old
		rb := RecomputedBag{NewCert: cert, Fingerprint: fp, RemovedWitnesses: removed}
		if hadOld {
			rb.OldCert = &oldCopy
		}
		delta.Recomputed = append(delta.Recomputed, rb)
		delta.RecomputeCount++
		delta.SignatureChecks += checks
		delta.HashOps += 2
		newBags[bag] = cert
	}

	for _, k := range sortedNodeKeys(affected) {
		delta.AffectedNodes = append(delta.AffectedNodes, idOf(k))
	}
	for _, k := range sortedNodeKeys(ancestors) {
		delta.AncestorNodes = append(delta.AncestorNodes, idOf(k))
	}

	// 独立组装确定性新根；这一步不读取任何“声称”的新根。
	patchSeq := in.OldRoot.Body.PatchSeq
	if len(in.Patches) > 0 {
		patchSeq = in.Patches[len(in.Patches)-1].Seq
	}
	computed, err := assembleRoot(in.OldRoot.Body.Epoch+1, newBags, reg, patchSeq)
	if err != nil {
		return UpdateResult{}, err
	}
	signed, err := SignRoot(computed, in.Signer)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := VerifyRoot(signed, in.Authority); err != nil {
		return UpdateResult{}, ErrRootMismatch
	}
	delta.NewRoot = signed

	sort.Slice(delta.Recomputed, func(i, j int) bool {
		return delta.Recomputed[i].NewCert.Body.Bag < delta.Recomputed[j].NewCert.Body.Bag
	})
	sort.Slice(delta.Reused, func(i, j int) bool { return delta.Reused[i].Bag < delta.Reused[j].Bag })

	return UpdateResult{NewRoot: signed, NewBags: newBags, Delta: delta}, nil
}
