package authsolve

import "fmt"

type policyNode struct {
	threshold  int
	children   []int
	role       string
	identity   string
	device     string
	credential string
}

type policyTree struct {
	nodes []policyNode
	root  int
	creds []Credential
	match []uint64 // match[node] is the credential mask matching this leaf
	memo  map[policyMemoKey]bool
	leaf  []bool
}

type policyMemoKey struct {
	node int
	mask uint64
}

func buildPolicyTree(p Problem, creds []Credential) (*policyTree, error) {
	pt := &policyTree{
		creds: creds,
		memo:  make(map[policyMemoKey]bool),
	}
	root, err := pt.add(p.Policy)
	if err != nil {
		return nil, err
	}
	pt.root = root
	pt.match = make([]uint64, len(pt.nodes))
	pt.leaf = make([]bool, len(pt.nodes))
	for i := range pt.nodes {
		n := &pt.nodes[i]
		pt.leaf[i] = len(n.children) == 0
		if !pt.leaf[i] {
			continue
		}
		var mask uint64
		for j, c := range creds {
			if leafMatches(*n, c, p) {
				mask |= uint64(1) << uint(j)
			}
		}
		pt.match[i] = mask
	}
	return pt, nil
}

func (pt *policyTree) add(n PolicyNode) (int, error) {
	idx := len(pt.nodes)
	pt.nodes = append(pt.nodes, policyNode{
		threshold:  thresholdOf(n),
		role:       n.Role,
		identity:   n.Identity,
		device:     n.Device,
		credential: n.Credential,
	})
	children := make([]int, 0, len(n.Children))
	for _, child := range n.Children {
		ci, err := pt.add(child)
		if err != nil {
			return 0, err
		}
		children = append(children, ci)
	}
	pt.nodes[idx].children = children
	if len(children) > 0 {
		k := pt.nodes[idx].threshold
		if k < 0 {
			return 0, fmt.Errorf("%w: policy threshold %d for %d children", ErrInvalidInput, k, len(children))
		}
	}
	return idx, nil
}

func thresholdOf(n PolicyNode) int {
	if n.Threshold != 0 {
		return n.Threshold
	}
	if n.K != 0 {
		return n.K
	}
	return len(n.Children)
}

func leafMatches(n policyNode, c Credential, p Problem) bool {
	if n.identity != "" && n.identity != c.Identity {
		return false
	}
	if n.device != "" && n.device != c.Device {
		return false
	}
	if n.credential != "" && n.credential != c.ID {
		return false
	}
	if n.role == "" {
		return true
	}
	if len(p.RoleLevels) == 0 {
		return c.Role == n.role
	}
	have, ok := p.RoleLevels[c.Role]
	if !ok {
		return false
	}
	need, ok := p.RoleLevels[n.role]
	if !ok {
		return c.Role == n.role
	}
	return have >= need
}

func (pt *policyTree) satisfied(mask uint64) bool {
	return pt.nodeSatisfied(pt.root, mask)
}

func (pt *policyTree) nodeSatisfied(node int, mask uint64) bool {
	key := policyMemoKey{node: node, mask: mask}
	if v, ok := pt.memo[key]; ok {
		return v
	}
	n := pt.nodes[node]
	var result bool
	if len(n.children) == 0 {
		result = pt.match[node]&mask != 0
	} else {
		k := n.threshold
		if k == 0 {
			result = true
		} else {
			result = pt.pick(n.children, k, 0, mask)
		}
	}
	pt.memo[key] = result
	return result
}

// pick assigns pairwise-disjoint credential subsets to the first k satisfied
// children.  A credential (and therefore a permanent identity, because masks
// with duplicate identities are rejected earlier) can be consumed at most
// once across the whole tree.
func (pt *policyTree) pick(children []int, k int, start int, available uint64) bool {
	if k == 0 {
		return true
	}
	if start >= len(children) || len(children)-start < k {
		return false
	}
	// Leave this child unsatisfied.
	if pt.pick(children, k, start+1, available) {
		return true
	}
	// Or satisfy it with any non-empty subset of the still-available
	// credentials.
	sub := available
	for sub != 0 {
		if pt.nodeSatisfied(children[start], sub) {
			if pt.pick(children, k-1, start+1, available&^sub) {
				return true
			}
		}
		sub = (sub - 1) & available
	}
	return false
}
