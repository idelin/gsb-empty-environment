package bplustree

import (
	"fmt"
	"strings"
)

// Violation describes a single broken invariant, located by Path.
type Violation struct {
	Code   string // machine-readable invariant code, e.g. ROUTE_KEY_GT_LEFT_MAX
	Path   string // node location, e.g. root/child[2]/child[0]
	Detail string // human-readable explanation with key values
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] %s: %s", v.Code, v.Path, v.Detail)
}

// Report is the result of a full-tree inspection.
type Report struct {
	Violations    []Violation
	NodesChecked  int
	LeavesChecked int
	KeysChecked   int
	Depth         int // leaf depth (root-only tree => 0)
}

// OK reports whether the tree passed every invariant.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "inspection: nodes=%d leaves=%d keys=%d depth=%d violations=%d\n",
		r.NodesChecked, r.LeavesChecked, r.KeysChecked, r.Depth, len(r.Violations))
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "  %s\n", v)
	}
	if r.OK() {
		b.WriteString("  OK: all invariants hold\n")
	}
	return b.String()
}

const (
	codeNodeCycle       = "NODE_CYCLE"
	codeUnsorted        = "KEYS_NOT_SORTED"
	codeKeyValMismatch  = "KEY_VAL_LEN_MISMATCH"
	codeArity           = "ARITY_MISMATCH"
	codeUnderfull       = "NODE_UNDERFULL"
	codeOverfull        = "NODE_OVERFULL"
	codeRouteGTLeftMax  = "ROUTE_KEY_GT_LEFT_MAX"
	codeRouteLTLeftMax  = "ROUTE_KEY_LT_LEFT_MAX"
	codeRouteGERightMin = "ROUTE_KEY_GE_RIGHT_MIN"
	codeRangeLow        = "KEY_BELOW_RANGE"
	codeRangeHigh       = "KEY_ABOVE_RANGE"
	codeBadParent       = "PARENT_PTR_MISMATCH"
	codeLeafDepth       = "LEAF_DEPTH_SKEW"
	codeLeafCycle       = "LEAF_CHAIN_CYCLE"
	codeLeafPrev        = "LEAF_PREV_MISMATCH"
	codeLeafOrder       = "LEAF_CHAIN_DISORDER"
	codeLeafMissing     = "LEAF_MISSING_FROM_CHAIN"
	codeLeafExtra       = "LEAF_NOT_IN_TREE"
)

// Inspect walks the whole tree top-down and verifies every invariant:
//   - keys strictly sorted inside each node
//   - routing keys consistent with child subtrees: keys[i] must equal the
//     max key of children[i] and be smaller than every key of children[i+1]
//     (no overlap, no gap)
//   - node occupancy within [min, max] bounds
//   - parent pointers consistent
//   - all leaves at the same depth
//   - leaf chain acyclic, doubly linked and fully closed in both directions
func Inspect(t *Tree) *Report {
	r := &Report{}
	if t.Root == nil {
		r.add(codeNodeCycle, "root", "tree has nil root")
		return r
	}
	visited := make(map[*Node]bool)
	leavesInTree := make(map[*Node]bool)
	leafDepths := make(map[int]bool)
	r.walk(t, t.Root, "root", 0, nil, nil, visited, leavesInTree, leafDepths)
	if len(leafDepths) > 1 {
		depths := []int{}
		for d := range leafDepths {
			depths = append(depths, d)
		}
		r.add(codeLeafDepth, "root", fmt.Sprintf("leaves found at differing depths %v", depths))
	}
	r.checkLeafChain(t, leavesInTree)
	return r
}

func (r *Report) add(code, path, detail string) {
	r.Violations = append(r.Violations, Violation{Code: code, Path: path, Detail: detail})
}

// walk verifies one subtree. lo is an exclusive lower bound and hi an
// inclusive upper bound imposed by ancestors (nil means unbounded).
func (r *Report) walk(t *Tree, n *Node, path string, depth int, lo, hi *int,
	visited, leavesInTree map[*Node]bool, leafDepths map[int]bool) {
	if visited[n] {
		r.add(codeNodeCycle, path, "node reachable via multiple paths (cycle or shared child)")
		return
	}
	visited[n] = true
	r.NodesChecked++
	r.KeysChecked += len(n.Keys)

	// 1. keys strictly sorted
	for i := 1; i < len(n.Keys); i++ {
		if n.Keys[i-1] >= n.Keys[i] {
			r.add(codeUnsorted, path, fmt.Sprintf("keys[%d]=%d >= keys[%d]=%d",
				i-1, n.Keys[i-1], i, n.Keys[i]))
		}
	}

	// 2. keys within ancestor bounds
	for _, k := range n.Keys {
		if lo != nil && k <= *lo {
			r.add(codeRangeLow, path, fmt.Sprintf("key %d <= lower bound %d", k, *lo))
		}
		if hi != nil && k > *hi {
			r.add(codeRangeHigh, path, fmt.Sprintf("key %d > upper bound %d", k, *hi))
		}
	}

	// 3. occupancy
	isRoot := n.Parent == nil
	if n.IsLeaf {
		r.LeavesChecked++
		leafDepths[depth] = true
		leavesInTree[n] = true
		if len(n.Vals) != len(n.Keys) {
			r.add(codeKeyValMismatch, path, fmt.Sprintf("%d keys but %d values", len(n.Keys), len(n.Vals)))
		}
		if !isRoot {
			if len(n.Keys) < t.minLeafKeys() {
				r.add(codeUnderfull, path, fmt.Sprintf("leaf has %d keys, min is %d", len(n.Keys), t.minLeafKeys()))
			}
			if len(n.Keys) > t.maxLeafKeys() {
				r.add(codeOverfull, path, fmt.Sprintf("leaf has %d keys, max is %d", len(n.Keys), t.maxLeafKeys()))
			}
		}
		if depth > r.Depth {
			r.Depth = depth
		}
		return
	}

	// internal node
	if len(n.Children) != len(n.Keys)+1 {
		r.add(codeArity, path, fmt.Sprintf("%d children but %d keys", len(n.Children), len(n.Keys)))
		return // cannot trust routing invariants below
	}
	if isRoot {
		if len(n.Children) < 2 {
			r.add(codeUnderfull, path, fmt.Sprintf("internal root has %d children, min is 2", len(n.Children)))
		}
	} else {
		if len(n.Children) < t.minChildren() {
			r.add(codeUnderfull, path, fmt.Sprintf("internal node has %d children, min is %d", len(n.Children), t.minChildren()))
		}
		if len(n.Children) > t.Order {
			r.add(codeOverfull, path, fmt.Sprintf("internal node has %d children, max is %d", len(n.Children), t.Order))
		}
	}

	// 4. routing keys vs. child subtrees
	for i := 0; i < len(n.Children); i++ {
		child := n.Children[i]
		if child == nil {
			r.add(codeArity, fmt.Sprintf("%s/child[%d]", path, i), "nil child pointer")
			continue
		}
		if child.Parent != n {
			r.add(codeBadParent, fmt.Sprintf("%s/child[%d]", path, i), "child.Parent does not point back to this node")
		}
		if i < len(n.Keys) {
			if leftMax, ok := safeSubtreeMax(child); ok {
				if n.Keys[i] > leftMax {
					r.add(codeRouteGTLeftMax, path, fmt.Sprintf(
						"routing key keys[%d]=%d exceeds max key %d of left subtree child[%d]",
						i, n.Keys[i], leftMax, i))
				} else if n.Keys[i] < leftMax {
					r.add(codeRouteLTLeftMax, path, fmt.Sprintf(
						"routing key keys[%d]=%d below max key %d of left subtree child[%d]",
						i, n.Keys[i], leftMax, i))
				}
			}
		}
		if i > 0 && len(child.Keys) > 0 {
			if min, ok := safeSubtreeMin(child); ok && n.Keys[i-1] >= min {
				r.add(codeRouteGERightMin, path, fmt.Sprintf(
					"routing key keys[%d]=%d >= min key %d of right subtree child[%d] (ranges overlap)",
					i-1, n.Keys[i-1], min, i))
			}
		}
	}

	// 5. recurse with tightened bounds (guarantees non-overlapping ranges)
	for i, child := range n.Children {
		if child == nil {
			continue
		}
		clo, chi := lo, hi
		if i > 0 {
			v := n.Keys[i-1]
			clo = &v
		}
		if i < len(n.Keys) {
			v := n.Keys[i]
			chi = &v
		}
		r.walk(t, child, fmt.Sprintf("%s/child[%d]", path, i), depth+1, clo, chi, visited, leavesInTree, leafDepths)
	}
}

// checkLeafChain verifies the doubly linked leaf level: starts at the
// leftmost leaf, follows Next pointers, and demands a fully closed,
// acyclic, strictly ordered chain containing exactly the tree's leaves.
func (r *Report) checkLeafChain(t *Tree, leavesInTree map[*Node]bool) {
	head := t.Root
	for head != nil && !head.IsLeaf {
		if len(head.Children) == 0 {
			return // arity error already reported
		}
		head = head.Children[0]
	}
	if head == nil {
		return
	}
	if head.Prev != nil {
		r.add(codeLeafPrev, "leaf-chain/head", "leftmost leaf has non-nil Prev")
	}
	seen := make(map[*Node]bool)
	var prev *Node
	count := 0
	for cur := head; cur != nil; cur = cur.Next {
		if seen[cur] {
			r.add(codeLeafCycle, "leaf-chain", "Next pointers form a cycle")
			return
		}
		seen[cur] = true
		count++
		if !leavesInTree[cur] {
			r.add(codeLeafExtra, "leaf-chain", "chain contains a node not reachable from the root")
		}
		if cur.Prev != prev {
			r.add(codeLeafPrev, "leaf-chain", fmt.Sprintf(
				"leaf %v has broken Prev pointer (chain not closed backwards)", cur.Keys))
		}
		if prev != nil && len(prev.Keys) > 0 && len(cur.Keys) > 0 &&
			prev.Keys[len(prev.Keys)-1] >= cur.Keys[0] {
			r.add(codeLeafOrder, "leaf-chain", fmt.Sprintf(
				"leaf %v followed by leaf %v: chain order broken",
				prev.Keys, cur.Keys))
		}
		prev = cur
	}
	for leaf := range leavesInTree {
		if !seen[leaf] {
			r.add(codeLeafMissing, "leaf-chain", fmt.Sprintf(
				"leaf %v unreachable from the leftmost leaf", leaf.Keys))
		}
	}
}

func safeSubtreeMax(n *Node) (int, bool) {
	for !n.IsLeaf {
		if len(n.Children) == 0 {
			return 0, false
		}
		n = n.Children[len(n.Children)-1]
	}
	if len(n.Keys) == 0 {
		return 0, false
	}
	return n.Keys[len(n.Keys)-1], true
}

func safeSubtreeMin(n *Node) (int, bool) {
	for !n.IsLeaf {
		if len(n.Children) == 0 {
			return 0, false
		}
		n = n.Children[0]
	}
	if len(n.Keys) == 0 {
		return 0, false
	}
	return n.Keys[0], true
}
