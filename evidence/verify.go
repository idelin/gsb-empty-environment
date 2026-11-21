package evidence

import (
	"crypto/ed25519"
	"reflect"
)

// VerifyDelta 是面向第三方的独立重验入口。验证方只持有：
//   - 上一代受签根与对应注册表（自己持久化并已核对）；
//   - 根权威公钥；
//   - 本地持久化的上一代袋证书库 oldBags（已由旧根 Merkle 锚定）；
//   - 待验证的最小披露差分。
//
// 它独立重放补丁、重算受影响袋、复核复用边界、按固定权重重算成本，
// 并重新组装根与差分中的新根逐字节对照，不相信差分里的任何声明值。
// 复用袋遵循最小披露：差分只给复用依据（指纹），不给袋主体，主体由
// 验证方的本地旧袋库提供。
func VerifyDelta(oldRoot SignedRoot, oldRegistry *RegistryState, authority ed25519.PublicKey, oldBags map[BagID]BagCert, d Delta) error {
	if err := VerifyRoot(oldRoot, authority); err != nil {
		return err
	}
	if !reflect.DeepEqual(d.OldRoot.Body, oldRoot.Body) || d.NewRoot.Body.Epoch != oldRoot.Body.Epoch+1 {
		return ErrDelta
	}
	reg := oldRegistry.Clone()
	regHash, err := reg.Hash()
	if err != nil {
		return err
	}
	if regHash != oldRoot.Body.RegistryHash || regHash != d.OldRegistryHash {
		return ErrDelta
	}

	// 校验本地旧袋库确实由旧根锚定，并恢复旧节点事实（完整环境身份）。
	oldCerts := map[BagID]BagCert{}
	anchored := map[BagID]Digest{}
	for _, e := range oldRoot.Body.Bags {
		anchored[e.Bag] = e.Hash
	}
	for bag, c := range oldBags {
		h, err := hashCanon(c.Body)
		if err != nil {
			return err
		}
		want, ok := anchored[bag]
		if !ok || want != h || h != c.BodyHash {
			return ErrRootAnchor
		}
		oldCerts[bag] = c
	}
	if len(oldCerts) != len(oldRoot.Body.Bags) {
		return ErrBagMissing
	}

	workNodes := indexBags(oldCerts)
	oldRev := indexReverseDeps(workNodes)
	expectedSeq := oldRoot.Body.PatchSeq + 1
	direct := map[nodeKey]bool{}
	govTouched := false
	for _, env := range d.Patches {
		if err := VerifyEnvelope(env, authority); err != nil {
			return err
		}
		if env.Seq != expectedSeq {
			return ErrPatchSequence
		}
		expectedSeq++
		if env.Gov != nil {
			if env.PrevRegHash != regHash {
				return ErrRegistryHash
			}
			next, err := reg.Apply(*env.Gov)
			if err != nil {
				return err
			}
			reg = next
			regHash, err = reg.Hash()
			if err != nil {
				return err
			}
			govTouched = true
			continue
		}
		k, err := applyNodePatch(workNodes, *env.Node)
		if err != nil {
			return err
		}
		direct[k] = true
	}
	if regHash != d.NewRegistryHash {
		return ErrDelta
	}
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

	// 受影响节点按依赖关系在补丁后的图上闭包；袋归属变化不改变节点集合。
	affected := closureOnGraph(direct, oldRev)
	if !sameNodeSet(d.AffectedNodes, affected) {
		return ErrDelta
	}
	ancestors := map[nodeKey]bool{}
	for k := range affected {
		if !direct[k] {
			ancestors[k] = true
		}
	}
	if !sameNodeSet(d.AncestorNodes, ancestors) {
		return ErrDelta
	}

	// 受影响袋 = 受影响节点当前所在袋 + 跨袋移动离开的旧袋；
	// 注册表代数推进则所有袋都必须在新约束边界下重验。
	affectedBags := map[BagID]bool{}
	for k := range affected {
		affectedBags[workNodes[k].Bag] = true
	}
	for _, env := range d.Patches {
		if env.Node == nil || env.Node.Op != NodeUpsert {
			continue
		}
		if oldBag := bagOf(oldCerts, env.Node.Node); oldBag != "" {
			affectedBags[oldBag] = true
		}
		if env.Node.Bag != "" {
			affectedBags[env.Node.Bag] = true
		}
	}
	if reg.Seq != oldRoot.Body.RegistrySeq {
		for bag := range oldCerts {
			affectedBags[bag] = true
		}
	}

	gotRecomputed, gotReused := map[BagID]bool{}, map[BagID]bool{}
	rebuilt := map[BagID]BagCert{}
	sigWeight, hashWeight := CostWeights()
	totalSigChecks := 0

	for _, rb := range d.Recomputed {
		bag := rb.NewCert.Body.Bag
		if gotRecomputed[bag] {
			return ErrDelta
		}
		if !affectedBags[bag] {
			return ErrReuseBoundary
		}
		gotRecomputed[bag] = true
		if !sameNodeStates(nodesForBag(workNodes, bag), rb.NewCert.Body.Nodes) {
			return ErrDelta
		}
		if rb.NewCert.Body.RegistrySeq != reg.Seq {
			return ErrDelta
		}
		for _, n := range rb.NewCert.Body.Nodes {
			claim, _ := selectSource(n, reg)
			got := findClaim(rb.NewCert.Body.Sources, n.ID)
			if got == nil || !claimsEqual(*got, claim) {
				return ErrDelta
			}
			if got.Cost.SigChecks*sigWeight+got.Cost.HashOps*hashWeight != got.Cost.Weighted {
				return ErrCostLedger
			}
			totalSigChecks += got.Cost.SigChecks
		}
		for _, w := range rb.RemovedWitnesses {
			if reg.IsWitnessValid(w) {
				return ErrDelta
			}
		}
		h, err := hashCanon(rb.NewCert.Body)
		if err != nil {
			return err
		}
		if h != rb.NewCert.BodyHash {
			return ErrDelta
		}
		fp, err := ComputeFingerprint(rb.NewCert.Body, regHash)
		if err != nil {
			return err
		}
		if fp != rb.Fingerprint {
			return ErrDelta
		}
		rebuilt[bag] = rb.NewCert
	}

	for _, rr := range d.Reused {
		bag := rr.Bag
		if gotReused[bag] || gotRecomputed[bag] {
			return ErrDelta
		}
		if affectedBags[bag] {
			return ErrReuseBoundary
		}
		gotReused[bag] = true
		old, ok := oldCerts[bag]
		if !ok {
			return ErrBagMissing
		}
		expectNodes := nodesForBag(workNodes, bag)
		if !sameNodeStates(expectNodes, old.Body.Nodes) {
			return ErrReuseBoundary
		}
		oldFP, err := ComputeFingerprint(old.Body, oldRoot.Body.RegistryHash)
		if err != nil {
			return err
		}
		newFP, err := ComputeFingerprint(
			BagBody{Bag: bag, Nodes: expectNodes, RegistrySeq: old.Body.RegistrySeq},
			oldRoot.Body.RegistryHash,
		)
		if err != nil {
			return err
		}
		if oldFP != newFP || oldFP != rr.Fingerprint {
			return ErrReuseBoundary
		}
		if rr.OldBodyHash != old.BodyHash || rr.NewBodyHash != old.BodyHash {
			return ErrDelta
		}
		rebuilt[bag] = old
	}

	if len(rebuilt) != len(oldRoot.Body.Bags) {
		return ErrDelta
	}
	if d.RecomputeCount != len(d.Recomputed) || d.TotalBags != len(rebuilt) {
		return ErrDelta
	}
	if d.SignatureChecks != totalSigChecks {
		return ErrCostLedger
	}

	patchSeq := oldRoot.Body.PatchSeq
	if len(d.Patches) > 0 {
		patchSeq = d.Patches[len(d.Patches)-1].Seq
	}
	body, err := assembleRoot(oldRoot.Body.Epoch+1, rebuilt, reg, patchSeq)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(body, d.NewRoot.Body) {
		return ErrRootMismatch
	}
	return VerifyRoot(d.NewRoot, authority)
}

func nodesForBag(nodes map[nodeKey]NodeState, bag BagID) []NodeState {
	var out []NodeState
	for _, n := range nodes {
		if n.Bag == bag {
			out = append(out, cloneNodeState(n))
		}
	}
	for i := range out {
		sortWitnesses(out[i].Witnesses)
	}
	sortNodeStates(out)
	return out
}

func sortNodeStates(ns []NodeState) {
	for i := 1; i < len(ns); i++ {
		for j := i; j > 0 && nodeLess(ns[j].ID, ns[j-1].ID); j-- {
			ns[j], ns[j-1] = ns[j-1], ns[j]
		}
	}
}

func findClaim(claims []SourceClaim, id NodeID) *SourceClaim {
	for i := range claims {
		if claims[i].Node == id {
			return &claims[i]
		}
	}
	return nil
}

func claimsEqual(a, b SourceClaim) bool {
	if a.Node != b.Node || a.Unsourced != b.Unsourced || a.Cost != b.Cost {
		return false
	}
	if (a.Chosen == nil) != (b.Chosen == nil) {
		return false
	}
	if a.Chosen != nil && !reflect.DeepEqual(*a.Chosen, *b.Chosen) {
		return false
	}
	if len(a.Failures) != len(b.Failures) {
		return false
	}
	for i := range a.Failures {
		if a.Failures[i] != b.Failures[i] {
			return false
		}
	}
	return true
}

func sameNodeSet(got []NodeID, want map[nodeKey]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for _, n := range got {
		if !want[keyOf(n)] {
			return false
		}
	}
	return true
}

func sameNodeStates(a, b []NodeState) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]NodeState(nil), a...)
	bb := append([]NodeState(nil), b...)
	sortNodeStates(aa)
	sortNodeStates(bb)
	for i := range aa {
		na, nb := aa[i], bb[i]
		if na.ID != nb.ID || na.Bag != nb.Bag || len(na.Deps) != len(nb.Deps) ||
			len(na.Witnesses) != len(nb.Witnesses) {
			return false
		}
		for j := range na.Deps {
			if na.Deps[j] != nb.Deps[j] {
				return false
			}
		}
		for j := range na.Witnesses {
			if !reflect.DeepEqual(na.Witnesses[j], nb.Witnesses[j]) {
				return false
			}
		}
	}
	return true
}
