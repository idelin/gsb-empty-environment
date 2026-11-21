package evidence

import "crypto/sha256"

func sha256Sum(b []byte) Digest { return sha256.Sum256(b) }

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cloneWitness 返回见证深拷贝。
func cloneWitness(w Witness) Witness {
	w.PubKey = append([]byte(nil), w.PubKey...)
	w.Sig = append([]byte(nil), w.Sig...)
	return w
}

// cloneNodeState 返回节点状态深拷贝。
func cloneNodeState(n NodeState) NodeState {
	cp := n
	cp.Deps = append([]NodeID(nil), n.Deps...)
	if n.Witnesses != nil {
		cp.Witnesses = make([]Witness, len(n.Witnesses))
		for i, w := range n.Witnesses {
			cp.Witnesses[i] = cloneWitness(w)
		}
	}
	return cp
}

// sortNodes 按环境身份对节点切片做确定性排序。
func sortNodes(nodes []NodeState) {
	for i := range nodes {
		sortWitnesses(nodes[i].Witnesses)
	}
}

func sortWitnesses(ws []Witness) {
	// 简单插入排序：见证数量通常很少，且避免引入额外依赖。
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && witnessLess(ws[j], ws[j-1]); j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}

func witnessLess(a, b Witness) bool {
	if a.Domain != b.Domain {
		return a.Domain < b.Domain
	}
	if a.Builder != b.Builder {
		return a.Builder < b.Builder
	}
	if a.Node.Env != b.Node.Env {
		return a.Node.Env < b.Node.Env
	}
	return digestLess(a.Node.Payload, b.Node.Payload)
}

func digestLess(a, b Digest) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func nodeLess(a, b NodeID) bool {
	if a.Env != b.Env {
		return a.Env < b.Env
	}
	return digestLess(a.Payload, b.Payload)
}

func sortNodeIDs(ids []NodeID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && nodeLess(ids[j], ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// bagOf 在旧袋证书集合中查找某节点当前所属袋标识，未找到返回空串。
func bagOf(oldBags map[BagID]BagCert, id NodeID) BagID {
	k := keyOf(id)
	for _, c := range oldBags {
		for _, n := range c.Body.Nodes {
			if keyOf(n.ID) == k {
				return c.Body.Bag
			}
		}
	}
	return ""
}
