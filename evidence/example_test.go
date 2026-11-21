package evidence_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"

	"prescreen.local/ddd-10492-v4/evidence"
)

// Example 展示宿主在撤销一批构建器后如何做增量更新，并让第三方独立重验。
func Example() {
	authPub, authPriv := mustKey("root")
	b1Pub, b1Priv := mustKey("b1")
	b2Pub, _ := mustKey("b2")

	reg := evidence.GenesisRegistry()
	reg = mustApply(reg, evidence.RegistryEvent{
		Kind: evidence.GovAddDomain, DomainID: "dom-a", Name: "A",
	})
	reg = mustApply(reg, evidence.RegistryEvent{
		Kind: evidence.GovAddBuilder, DomainID: "dom-a", Builder: "b1", PubKey: b1Pub,
	})
	reg = mustApply(reg, evidence.RegistryEvent{
		Kind: evidence.GovAddBuilder, DomainID: "dom-a", Builder: "b2", PubKey: b2Pub,
	})

	payload := sha256.Sum256([]byte("artifact"))
	node := evidence.NodeID{Env: "prod", Payload: payload}
	w1, _ := evidence.SignWitness(b1Priv, node.Env, node.Payload, "dom-a", "b1", b1Pub)

	root, bags, err := evidence.Genesis(evidence.GenesisInput{
		Epoch: 1, Registry: reg,
		Nodes: []evidence.NodeState{
			{ID: node, Bag: "bag-1", Witnesses: []evidence.Witness{w1}},
		},
		Signer: authPriv,
	})
	if err != nil {
		panic(err)
	}

	// 下一世代：撤销 b1（补丁由根权威签名，前像注册表摘要受约束）。
	oldReg := reg.Clone()
	regHash, _ := oldReg.Hash()
	rev, _ := evidence.SignEnvelope(evidence.Envelope{
		Seq: root.Body.PatchSeq + 1,
		Gov: &evidence.RegistryEvent{
			Kind:     evidence.GovRevokeBuilders,
			Builders: []evidence.BuilderID{"b1"},
			Reason:   "rotation",
		},
		PrevRegHash: regHash,
	}, authPriv)

	res, err := evidence.Update(evidence.UpdateInput{
		OldRoot: root, Authority: authPub, OldRegistry: oldReg,
		OldBags: bags, Patches: []evidence.Envelope{rev}, Signer: authPriv,
	})
	if err != nil {
		panic(err)
	}

	// 第三方只凭旧根、旧注册表、权威公钥、旧袋库和差分即可独立重验。
	if err := evidence.VerifyDelta(root, oldReg, authPub, bags, res.Delta); err != nil {
		panic(err)
	}
	fmt.Printf("epoch=%d recomputed=%d unsourced=%v\n",
		res.NewRoot.Body.Epoch,
		res.Delta.RecomputeCount,
		res.NewBags["bag-1"].Body.Sources[0].Unsourced,
	)
	// Output: epoch=2 recomputed=1 unsourced=true
}

func mustKey(seed string) (ed25519.PublicKey, ed25519.PrivateKey) {
	var s [ed25519.SeedSize]byte
	sum := sha256.Sum256([]byte(seed))
	copy(s[:], sum[:])
	priv := ed25519.NewKeyFromSeed(s[:])
	return priv.Public().(ed25519.PublicKey), priv
}

func mustApply(r *evidence.RegistryState, ev evidence.RegistryEvent) *evidence.RegistryState {
	next, err := r.Apply(ev)
	if err != nil {
		panic(err)
	}
	return next
}
