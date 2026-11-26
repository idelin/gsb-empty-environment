package crossmesh

import "sort"

// ScopeKind identifies the structural namespace a name lives in.
type ScopeKind uint8

const (
	// KindDomain treats the value as a DNS name; matching is suffix based on
	// dot-separated labels (reversed internally so the registered name becomes a
	// prefix), with explicit label-boundary checks.
	KindDomain ScopeKind = 1
	// KindURI treats the value as a URI/path string; matching is a literal
	// left-to-right component prefix (scheme://host/path...).
	KindURI ScopeKind = 2
)

// Name is a structured prefix. A prefix matches itself and every strictly more
// specific name in the same kind, following the boundary rules of the kind.
type Name struct {
	Kind  ScopeKind
	Value string
}

func (n Name) segments() []string {
	switch n.Kind {
	case KindDomain:
		return reverseSegments(splitDot(n.Value))
	case KindURI:
		return splitURI(n.Value)
	default:
		return nil
	}
}

func splitDot(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func reverseSegments(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[len(in)-1-i] = s
	}
	return out
}

// splitURI splits a URI on '/' and ':' so prefixes keep natural boundaries
// (e.g. "https://a" cannot match "https://abc").
func splitURI(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' || s[i] == ':' {
			out = append(out, s[start:i])
			out = append(out, string(s[i]))
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

type prefixTrieNode struct {
	name     Name
	segments []string
	children map[string]*prefixTrieNode
	parent   *prefixTrieNode
	cell     int
}

// ScopeCell is an atomic scope region: every include/exclude prefix boundary
// aligns with cell boundaries, so a trie subtree is always a union of whole
// cells and intersection along a chain never splits a cell.
type ScopeCell struct {
	Kind     ScopeKind
	Prefix   Name
	Relative bool
	// Display is a canonical human-readable description of the cell.
	Display string
}

type scopeUniverse struct {
	cells []ScopeCell
	roots map[ScopeKind]*prefixTrieNode
}

// buildScopeUniverse partitions the name space using every include and exclude
// prefix appearing in the graph. Cell 0 is always the "outside everything"
// cell, which no delegation can ever authorize.
func buildScopeUniverse(prefixes []Name) *scopeUniverse {
	u := &scopeUniverse{
		cells: []ScopeCell{{Display: "*outside*"}},
		roots: map[ScopeKind]*prefixTrieNode{},
	}

	uniq := map[Name]struct{}{}
	for _, n := range prefixes {
		uniq[n] = struct{}{}
	}
	all := make([]Name, 0, len(uniq))
	for n := range uniq {
		all = append(all, n)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].Value < all[j].Value
	})

	for _, n := range all {
		kind := n.Kind
		root := u.roots[kind]
		if root == nil {
			root = &prefixTrieNode{
				name:     Name{Kind: kind, Value: ""},
				children: map[string]*prefixTrieNode{},
				cell:     -1,
			}
			u.roots[kind] = root
		}
		cur := root
		for _, seg := range n.segments() {
			next := cur.children[seg]
			if next == nil {
				next = &prefixTrieNode{
					name:     Name{Kind: kind},
					children: map[string]*prefixTrieNode{},
					parent:   cur,
					cell:     -1,
				}
				cur.children[seg] = next
			}
			cur = next
		}
		cur.name = n
	}

	// DFS assignment: an internal trie node gets the cell immediately before the
	// subtree cells of its children.
	var assign func(t *prefixTrieNode)
	assign = func(t *prefixTrieNode) {
		if t.parent != nil {
			t.cell = len(u.cells)
			u.cells = append(u.cells, ScopeCell{
				Kind:     t.name.Kind,
				Prefix:   t.name,
				Relative: true,
				Display:  displayName(t.name) + "/*",
			})
		}
		keys := make([]string, 0, len(t.children))
		for k := range t.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			assign(t.children[k])
		}
	}
	for _, kind := range []ScopeKind{KindDomain, KindURI} {
		if r := u.roots[kind]; r != nil {
			assign(r)
		}
	}

	return u
}

func displayName(n Name) string {
	return n.Value
}

// lookupCell returns the atomic cell that the given concrete name belongs to.
func (u *scopeUniverse) lookupCell(n Name) int {
	root := u.roots[n.Kind]
	if root == nil {
		return 0
	}
	cur := root
	segs := n.segments()
	deepest := -1
	for _, seg := range segs {
		next := cur.children[seg]
		if next == nil {
			break
		}
		cur = next
		deepest = cur.cell
	}
	if deepest < 0 {
		return 0
	}
	return deepest
}

// subtreeCells returns every cell inside the subtree rooted at the trie node of
// prefix p (including the node's own cell).
func (u *scopeUniverse) subtreeCells(p Name) []int {
	root := u.roots[p.Kind]
	if root == nil {
		return nil
	}
	cur := root
	for _, seg := range p.segments() {
		next := cur.children[seg]
		if next == nil {
			return nil
		}
		cur = next
	}
	var out []int
	var walk func(t *prefixTrieNode)
	walk = func(t *prefixTrieNode) {
		if t.cell >= 0 {
			out = append(out, t.cell)
		}
		for _, ch := range t.children {
			walk(ch)
		}
	}
	walk(cur)
	return out
}

type bitSet map[int]struct{}

func (b bitSet) contains(v int) bool {
	_, ok := b[v]
	return ok
}

// scopeCover computes the cells authorized by one edge's include/exclude
// configuration. Excluded subtrees are never spliced back in: exclusion takes
// precedence over every include prefix on the same edge.
func scopeCover(u *scopeUniverse, includes, excludes []Name) bitSet {
	cover := bitSet{}
	for _, p := range includes {
		for _, c := range u.subtreeCells(p) {
			cover[c] = struct{}{}
		}
	}
	for _, p := range excludes {
		for _, c := range u.subtreeCells(p) {
			delete(cover, c)
		}
	}
	delete(cover, 0)
	return cover
}
