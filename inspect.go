package bptree

import (
	"fmt"
	"strings"
)

// Violation describes a single broken invariant, located by a root-to-node
// path such as "root.children[2].children[0]".
type Violation struct {
	Path   string // location of the offending node
	Rule   string // invariant identifier
	Detail string // human-readable explanation with concrete key values
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] %s: %s", v.Path, v.Rule, v.Detail)
}

// Report is the result of a full-tree inspection.
type Report struct {
	Violations   []Violation
	NodesVisited int
	KeysChecked  int
	LeafCount    int
	Height       int
}

// OK reports whether the tree passed every invariant.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "inspection: nodes=%d leaves=%d keys=%d height=%d\n",
		r.NodesVisited, r.LeafCount, r.KeysChecked, r.Height)
	if r.OK() {
		b.WriteString("result: OK - all invariants hold\n")
		return b.String()
	}
	fmt.Fprintf(&b, "result: CORRUPT - %d violation(s)\n", len(r.Violations))
	for i, v := range r.Violations {
		fmt.Fprintf(&b, "  #%d %s\n", i+1, v)
	}
	return b.String()
}

func (r *Report) add(path, rule, detail string) {
	r.Violations = append(r.Violations, Violation{Path: path, Rule: rule, Detail: detail})
}

// Inspect walks the whole tree top-down and verifies:
//
//   - keys strictly increasing within every node
//   - occupancy bounds (MinKeys..MaxKeys) for every non-root node
//   - parent pointers consistent with the top-down path
//   - internal fan-out: len(children) == len(keys)+1
//   - routing key k[i] equals the max key of the left subtree children[i]
//     (catches "routing key greater than left subtree max" corruption)
//   - adjacent subtree ranges do not overlap: max(c[i-1]) < min(c[i])
//   - leaf chain: starts at the leftmost leaf, prev/next fully consistent,
//     no cycles, keys ordered across leaf boundaries, every leaf reachable
//     from the root appears in the chain exactly once
func Inspect(t *Tree) *Report {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r := &Report{}
	if t.root == nil {
		r.add("root", "NilRoot", "tree has no root node")
		return r
	}
	leaves := map[*Node]string{}
	r.walk(t.root, "root", nil, true, 1, leaves)
	r.checkLeafChain(t.root, leaves)
	return r
}

// walk verifies node-local and routing invariants, returning the min/max
// key of the subtree and whether they are defined.
func (r *Report) walk(n *Node, path string, parent *Node, isRoot bool, depth int, leaves map[*Node]string) (min, max int, ok bool) {
	r.NodesVisited++
	r.KeysChecked += len(n.keys)
	if depth > r.Height {
		r.Height = depth
	}

	if n.parent != parent {
		r.add(path, "ParentPointer",
			fmt.Sprintf("parent pointer is %p, expected %p", n.parent, parent))
	}
	for i := 1; i < len(n.keys); i++ {
		if n.keys[i] <= n.keys[i-1] {
			r.add(path, "KeyOrder",
				fmt.Sprintf("keys not strictly increasing: keys[%d]=%d <= keys[%d]=%d",
					i, n.keys[i], i-1, n.keys[i-1]))
		}
	}
	if !isRoot && (len(n.keys) < MinKeys || len(n.keys) > MaxKeys) {
		r.add(path, "Occupancy",
			fmt.Sprintf("node holds %d keys, must be within [%d, %d]",
				len(n.keys), MinKeys, MaxKeys))
	}

	if n.leaf {
		leaves[n] = path
		if len(n.vals) != len(n.keys) {
			r.add(path, "LeafPayload",
				fmt.Sprintf("leaf has %d keys but %d values", len(n.keys), len(n.vals)))
		}
		if len(n.keys) == 0 {
			if !isRoot {
				r.add(path, "EmptyLeaf", "non-root leaf is empty")
			}
			return 0, 0, false
		}
		return n.keys[0], n.keys[len(n.keys)-1], true
	}

	if isRoot && len(n.keys) == 0 {
		r.add(path, "EmptyInternalRoot", "internal root has no keys")
	}
	if len(n.children) != len(n.keys)+1 {
		r.add(path, "FanOut",
			fmt.Sprintf("internal node has %d keys but %d children (expected %d)",
				len(n.keys), len(n.children), len(n.keys)+1))
	}

	ok = false
	prevMax := 0
	prevOK := false
	for i, c := range n.children {
		cpath := fmt.Sprintf("%s.children[%d]", path, i)
		cmin, cmax, cok := r.walk(c, cpath, n, false, depth+1, leaves)
		if !cok {
			continue
		}
		if !ok {
			min, ok = cmin, true
		}
		max = cmax
		if i < len(n.keys) {
			// Routing invariant: k[i] must equal the max key of the
			// subtree rooted at children[i].
			switch {
			case n.keys[i] > cmax:
				r.add(path, "RoutingKeyMismatch",
					fmt.Sprintf("routing key keys[%d]=%d is greater than left subtree max %d (subtree at %s)",
						i, n.keys[i], cmax, cpath))
			case n.keys[i] < cmax:
				r.add(path, "RoutingKeyMismatch",
					fmt.Sprintf("routing key keys[%d]=%d is smaller than left subtree max %d (subtree at %s)",
						i, n.keys[i], cmax, cpath))
			}
		}
		if prevOK && cmin <= prevMax {
			r.add(path, "SubtreeRangeOverlap",
				fmt.Sprintf("children[%d] min key %d overlaps children[%d] max key %d",
					i, cmin, i-1, prevMax))
		}
		prevMax, prevOK = cmax, true
	}
	return min, max, ok
}

// checkLeafChain verifies the doubly linked leaf list is closed and
// consistent with the tree structure discovered top-down.
func (r *Report) checkLeafChain(root *Node, leaves map[*Node]string) {
	// Descend to the leftmost leaf.
	head := root
	guard := 0
	for !head.leaf {
		if len(head.children) == 0 {
			r.add("root", "LeafChainHead",
				"cannot reach leftmost leaf: internal node without children")
			return
		}
		head = head.children[0]
		guard++
		if guard > 1_000_000 {
			r.add("root", "LeafChainHead", "descent to leftmost leaf did not terminate")
			return
		}
	}
	headPath := leaves[head]
	if head.prev != nil {
		r.add(headPath, "LeafChainHead",
			fmt.Sprintf("leftmost leaf has non-nil prev pointer to %p", head.prev))
	}

	visited := map[*Node]int{}
	prevMax := 0
	prevOK := false
	var prev *Node
	idx := 0
	for n := head; n != nil; n = n.next {
		path := leaves[n]
		if path == "" {
			path = fmt.Sprintf("leaf#%d(%p)", idx, n)
			r.add(path, "LeafChainStranger",
				"leaf reachable via next chain but not from the root")
		}
		if _, dup := visited[n]; dup {
			r.add(path, "LeafChainCycle",
				fmt.Sprintf("cycle detected: leaf revisited at chain position %d", idx))
			break
		}
		visited[n] = idx
		if !n.leaf {
			r.add(path, "LeafChainType", "next pointer leads to an internal node")
		}
		if n.prev != prev {
			want := "nil"
			if prev != nil {
				want = fmt.Sprintf("%p", prev)
			}
			r.add(path, "LeafChainPrev",
				fmt.Sprintf("prev pointer is %p, expected %s (chain position %d)",
					n.prev, want, idx))
		}
		if len(n.keys) > 0 {
			if prevOK && n.keys[0] <= prevMax {
				r.add(path, "LeafChainOrder",
					fmt.Sprintf("first key %d not greater than previous leaf max %d",
						n.keys[0], prevMax))
			}
			prevMax, prevOK = n.keys[len(n.keys)-1], true
		}
		prev = n
		idx++
	}
	r.LeafCount = idx

	// Every leaf reachable from the root must appear in the chain.
	for leaf, path := range leaves {
		if _, ok := visited[leaf]; !ok {
			r.add(path, "LeafUnreachable",
				"leaf reachable from root is missing from the next chain")
		}
	}
}
