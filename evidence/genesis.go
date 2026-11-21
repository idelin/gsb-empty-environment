package evidence

import "crypto/ed25519"

// GenesisInput 是建立第一代受签根所需的节点事实与注册表。
type GenesisInput struct {
	Epoch    uint64
	Registry *RegistryState
	Nodes    []NodeState
	Signer   ed25519.PrivateKey
	// PatchSeq 是创世建图阶段已使用的最高补丁信封序号；后续世代
	// 的补丁必须从 PatchSeq+1 连续编号。
	PatchSeq uint64
}

// Genesis 执行首次全量验证并产生受签根、袋证书。后续世代应使用 Update
// 增量维护；全量验证逻辑与 FullRebuild 保持同一套来源/成本规则。
func Genesis(in GenesisInput) (SignedRoot, map[BagID]BagCert, error) {
	reg := in.Registry
	bagNodes := map[BagID][]NodeState{}
	for _, n := range in.Nodes {
		bagNodes[n.Bag] = append(bagNodes[n.Bag], cloneNodeState(n))
	}
	bags := map[BagID]BagCert{}
	for bag, nodes := range bagNodes {
		synthetic := BagCert{Body: BagBody{Bag: bag, Nodes: nodes, RegistrySeq: reg.Seq}}
		cert, _, _, err := recomputeBag(synthetic, reg)
		if err != nil {
			return SignedRoot{}, nil, err
		}
		bags[bag] = cert
	}
	body, err := assembleRoot(in.Epoch, bags, reg, in.PatchSeq)
	if err != nil {
		return SignedRoot{}, nil, err
	}
	signed, err := SignRoot(body, in.Signer)
	if err != nil {
		return SignedRoot{}, nil, err
	}
	return signed, bags, nil
}
