package evidence

import (
	"crypto/ed25519"
)

// 节点补丁操作类型。
const (
	NodeUpsert        = "upsert"         // 新增或整体替换节点状态（含跨袋移动）
	NodeAddWitness    = "add_witness"    // 为已登记节点追加来源见证
	NodeRemoveWitness = "remove_witness" // 移除指定域/构建器的见证
)

// NodePatch 是针对单个节点（环境+摘要）的签名补丁。
type NodePatch struct {
	Op      string    `json:"op"`
	Node    NodeID    `json:"node"`
	Bag     BagID     `json:"bag,omitempty"`     // upsert / 跨袋移动
	Deps    []NodeID  `json:"deps,omitempty"`    // upsert
	Witness *Witness  `json:"witness,omitempty"` // add_witness
	Domain  DomainID  `json:"domain,omitempty"`  // remove_witness
	Builder BuilderID `json:"builder,omitempty"` // remove_witness
}

// Envelope 是统一的带签名补丁信封，节点补丁与治理补丁共用一条序号序列。
type Envelope struct {
	Seq         uint64         `json:"seq"`
	Node        *NodePatch     `json:"node_patch,omitempty"`
	Gov         *RegistryEvent `json:"gov_patch,omitempty"`
	PrevRegHash Digest         `json:"prev_reg_hash"` // 治理补丁的前像注册表摘要
	Sig         []byte         `json:"sig"`
}

type envelopeWire struct {
	Seq         uint64         `json:"seq"`
	Node        *NodePatch     `json:"node_patch,omitempty"`
	Gov         *RegistryEvent `json:"gov_patch,omitempty"`
	PrevRegHash Digest         `json:"prev_reg_hash"`
}

// SignEnvelope 用根权威私钥对信封内容签名。
func SignEnvelope(env Envelope, priv ed25519.PrivateKey) (Envelope, error) {
	raw, err := canonicalJSON(envelopeWire{
		Seq: env.Seq, Node: env.Node, Gov: env.Gov, PrevRegHash: env.PrevRegHash,
	})
	if err != nil {
		return Envelope{}, err
	}
	msg := taggedHash(tagEnvelope, raw)
	env.Sig = ed25519.Sign(priv, msg[:])
	return env, nil
}

// VerifyEnvelope 校验信封签名与内容一致性。
func VerifyEnvelope(env Envelope, authority ed25519.PublicKey) error {
	if len(authority) != ed25519.PublicKeySize || len(env.Sig) != ed25519.SignatureSize {
		return ErrPatchSignature
	}
	raw, err := canonicalJSON(envelopeWire{
		Seq: env.Seq, Node: env.Node, Gov: env.Gov, PrevRegHash: env.PrevRegHash,
	})
	if err != nil {
		return err
	}
	msg := taggedHash(tagEnvelope, raw)
	if !ed25519.Verify(authority, msg[:], env.Sig) {
		return ErrPatchSignature
	}
	return nil
}

// applyNodePatch 在节点索引上应用节点补丁，返回被修改节点的键。
func applyNodePatch(nodes map[nodeKey]NodeState, np NodePatch) (nodeKey, error) {
	k := keyOf(np.Node)
	switch np.Op {
	case NodeUpsert:
		if np.Bag == "" {
			return nodeKey{}, ErrNodeMissing
		}
		ns := NodeState{
			ID: np.Node, Bag: np.Bag, Deps: append([]NodeID(nil), np.Deps...),
		}
		if np.Witness != nil {
			if np.Witness.Node != np.Node {
				return nodeKey{}, ErrEnvMismatch
			}
			ns.Witnesses = []Witness{cloneWitness(*np.Witness)}
		}
		sortNodes([]NodeState{ns})
		nodes[k] = ns
	case NodeAddWitness:
		ns, ok := nodes[k]
		if !ok {
			return nodeKey{}, ErrNodeMissing
		}
		if np.Witness == nil {
			return nodeKey{}, ErrEnvMismatch
		}
		// 注意：不为另一环境同摘要对象的见证在这里报错——它会被登记，
		// 但在来源验证时因环境身份不匹配而失效（见 selectSource）。
		for _, w := range ns.Witnesses {
			if w.Domain == np.Witness.Domain && w.Builder == np.Witness.Builder {
				return nodeKey{}, ErrDomainExists
			}
		}
		ns.Witnesses = append(ns.Witnesses, cloneWitness(*np.Witness))
		sortWitnesses(ns.Witnesses)
		nodes[k] = ns
	case NodeRemoveWitness:
		ns, ok := nodes[k]
		if !ok {
			return nodeKey{}, ErrNodeMissing
		}
		filtered := ns.Witnesses[:0]
		removed := false
		for _, w := range ns.Witnesses {
			if w.Domain == np.Domain && w.Builder == np.Builder {
				removed = true
				continue
			}
			filtered = append(filtered, w)
		}
		if !removed {
			return nodeKey{}, ErrNodeMissing
		}
		ns.Witnesses = filtered
		nodes[k] = ns
	default:
		return nodeKey{}, ErrPatchSignature
	}
	return k, nil
}

// indexBags 从袋证书集合重建当前节点索引（环境身份完整保留）。
func indexBags(certs map[BagID]BagCert) map[nodeKey]NodeState {
	nodes := map[nodeKey]NodeState{}
	for _, c := range certs {
		for _, n := range c.Body.Nodes {
			nodes[keyOf(n.ID)] = cloneNodeState(n)
		}
	}
	return nodes
}

// indexReverseDeps 构造 被依赖者 -> 依赖它的节点集合（祖先传播用）。
func indexReverseDeps(nodes map[nodeKey]NodeState) map[nodeKey][]nodeKey {
	rev := map[nodeKey][]nodeKey{}
	for k, n := range nodes {
		for _, dep := range n.Deps {
			dk := keyOf(dep)
			rev[dk] = append(rev[dk], k)
		}
	}
	return rev
}

// closureOnGraph 在给定反向依赖图上求直接集合的祖先闭包。反向图在应用
// 补丁前从稳定的旧图快照取得，因此跨袋移动改变节点当前所属袋时，闭包
// 的节点集合仍是确定且可被独立重放的。
func closureOnGraph(direct map[nodeKey]bool, rev map[nodeKey][]nodeKey) map[nodeKey]bool {
	out := map[nodeKey]bool{}
	queue := make([]nodeKey, 0, len(direct))
	for k := range direct {
		if !out[k] {
			out[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, parent := range rev[cur] {
			if !out[parent] {
				out[parent] = true
				queue = append(queue, parent)
			}
		}
	}
	return out
}
