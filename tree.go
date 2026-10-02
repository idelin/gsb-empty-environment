package decisiontable

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Node is a node of the compiled decision tree.
type Node struct {
	Leaf     bool
	Action   string // leaf only
	Dim      int    // internal nodes: dimension being tested
	DimName  string // internal nodes: name of that dimension
	Branches []Branch
	Default  *Node // fallback for values not covered by any branch
}

// Branch is one labelled edge of a decision node.
type Branch struct {
	Label string
	Child *Node
}

const maxTreeDepth = 64

// BuildTree compiles a (conflict-free, ideally compressed) rule set into a
// minimal decision tree. At every level it splits on the dimension with the
// most distinct conditions; adjacent segments covering identical rule sets
// are folded into a single branch.
func BuildTree(rules []Rule, dims []Dim) *Node {
	return build(rules, dims, 0)
}

func build(rules []Rule, dims []Dim, depth int) *Node {
	if len(rules) == 0 {
		return &Node{Leaf: true, Action: "(no match)"}
	}
	allSame := true
	for _, r := range rules[1:] {
		if r.Action != rules[0].Action {
			allSame = false
			break
		}
	}
	if allSame || depth >= maxTreeDepth {
		return &Node{Leaf: true, Action: rules[0].Action}
	}
	d := chooseDim(rules, dims)
	if d < 0 {
		// Rules are condition-identical; conflicts must be resolved first.
		return &Node{Leaf: true, Action: rules[0].Action}
	}
	if dims[d].Numeric {
		return buildNumeric(rules, dims, d, depth)
	}
	return buildCategorical(rules, dims, d, depth)
}

// chooseDim picks the dimension with the most distinct conditions (>1).
func chooseDim(rules []Rule, dims []Dim) int {
	best, bestScore := -1, 1
	for d := range dims {
		seen := map[string]struct{}{}
		for _, r := range rules {
			seen[fmt.Sprintf("%#v", r.Conds[d])] = struct{}{}
		}
		if len(seen) > bestScore {
			best, bestScore = d, len(seen)
		}
	}
	return best
}

func wildcardOnly(rules []Rule, d int) []Rule {
	var out []Rule
	for _, r := range rules {
		if r.Conds[d].IsWildcard() {
			out = append(out, r)
		}
	}
	return out
}

func defaultNode(rules []Rule, dims []Dim, d, depth int) *Node {
	sub := wildcardOnly(rules, d)
	if len(sub) == 0 {
		return &Node{Leaf: true, Action: "(no match)"}
	}
	return build(sub, dims, depth+1)
}

type segment struct {
	lo, hi       float64
	loInc, hiInc bool
	test         float64
}

func buildNumeric(rules []Rule, dims []Dim, d, depth int) *Node {
	bset := map[float64]struct{}{}
	for _, r := range rules {
		if rc, ok := r.Conds[d].(RangeCond); ok {
			if !math.IsInf(rc.Lo, -1) {
				bset[rc.Lo] = struct{}{}
			}
			if !math.IsInf(rc.Hi, 1) {
				bset[rc.Hi] = struct{}{}
			}
		}
	}
	bounds := make([]float64, 0, len(bset))
	for b := range bset {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)

	// Elementary segments: open intervals between bounds plus singletons at
	// each bound, so inclusive/exclusive edges are classified correctly.
	var segs []segment
	if len(bounds) > 0 {
		segs = append(segs, segment{math.Inf(-1), bounds[0], false, false, bounds[0] - 1})
		for i, b := range bounds {
			segs = append(segs, segment{b, b, true, true, b})
			if i+1 < len(bounds) {
				segs = append(segs, segment{b, bounds[i+1], false, false, (b + bounds[i+1]) / 2})
			}
		}
		segs = append(segs, segment{bounds[len(bounds)-1], math.Inf(1), false, false, bounds[len(bounds)-1] + 1})
	}

	type group struct {
		lo, hi       float64
		loInc, hiInc bool
		subset       []Rule
	}
	var groups []group
	for _, s := range segs {
		sub := covering(rules, d, s.test)
		if len(sub) == 0 {
			continue
		}
		if n := len(groups); n > 0 && sameIDs(groups[n-1].subset, sub) {
			groups[n-1].hi = s.hi
			groups[n-1].hiInc = s.hiInc
			continue
		}
		groups = append(groups, group{s.lo, s.hi, s.loInc, s.hiInc, sub})
	}

	node := &Node{Dim: d, DimName: dims[d].Name}
	for _, g := range groups {
		node.Branches = append(node.Branches, Branch{
			Label: rangeLabel(g.lo, g.loInc, g.hi, g.hiInc),
			Child: build(g.subset, dims, depth+1),
		})
	}
	node.Default = defaultNode(rules, dims, d, depth)
	return node
}

func covering(rules []Rule, d int, v Value) []Rule {
	var out []Rule
	for _, r := range rules {
		if r.Conds[d].IsWildcard() || r.Conds[d].Matches(v) {
			out = append(out, r)
		}
	}
	return out
}

func sameIDs(a, b []Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func rangeLabel(lo float64, loInc bool, hi float64, hiInc bool) string {
	loInf, hiInf := math.IsInf(lo, -1), math.IsInf(hi, 1)
	switch {
	case loInf && hiInf:
		return "*"
	case lo == hi && loInc && hiInc:
		return "= " + fmtNum(lo)
	case loInf:
		if hiInc {
			return "≤ " + fmtNum(hi)
		}
		return "< " + fmtNum(hi)
	case hiInf:
		if loInc {
			return "≥ " + fmtNum(lo)
		}
		return "> " + fmtNum(lo)
	default:
		l, r := "[", "]"
		if !loInc {
			l = "("
		}
		if !hiInc {
			r = ")"
		}
		return "in " + l + fmtNum(lo) + ", " + fmtNum(hi) + r
	}
}

func buildCategorical(rules []Rule, dims []Dim, d, depth int) *Node {
	values := dims[d].Domain
	if values == nil {
		seen := map[Value]struct{}{}
		for _, r := range rules {
			if sc, ok := r.Conds[d].(SetCond); ok {
				for v := range sc.Values {
					if _, dup := seen[v]; !dup {
						seen[v] = struct{}{}
						values = append(values, v)
					}
				}
			}
		}
	}

	// Group domain values that select the identical rule subset.
	var subsets [][]Rule
	var labels [][]string
	for _, v := range values {
		sub := covering(rules, d, v)
		if len(sub) == 0 {
			continue
		}
		placed := false
		for i := range subsets {
			if sameIDs(subsets[i], sub) {
				labels[i] = append(labels[i], fmt.Sprint(v))
				placed = true
				break
			}
		}
		if !placed {
			subsets = append(subsets, sub)
			labels = append(labels, []string{fmt.Sprint(v)})
		}
	}

	node := &Node{Dim: d, DimName: dims[d].Name}
	for i := range subsets {
		node.Branches = append(node.Branches, Branch{
			Label: "= " + strings.Join(labels[i], " | "),
			Child: build(subsets[i], dims, depth+1),
		})
	}
	node.Default = defaultNode(rules, dims, d, depth)
	return node
}

// String renders the tree as an indented if/else listing.
func (n *Node) String() string {
	var b strings.Builder
	n.render(&b, "")
	return b.String()
}

func (n *Node) render(b *strings.Builder, indent string) {
	if n.Leaf {
		fmt.Fprintf(b, "%s→ %s\n", indent, n.Action)
		return
	}
	for _, br := range n.Branches {
		if br.Child.Leaf {
			fmt.Fprintf(b, "%sif %s %s → %s\n", indent, n.DimName, br.Label, br.Child.Action)
		} else {
			fmt.Fprintf(b, "%sif %s %s:\n", indent, n.DimName, br.Label)
			br.Child.render(b, indent+"  ")
		}
	}
	if n.Default != nil {
		if n.Default.Leaf {
			fmt.Fprintf(b, "%selse → %s\n", indent, n.Default.Action)
		} else {
			fmt.Fprintf(b, "%selse:\n", indent)
			n.Default.render(b, indent+"  ")
		}
	}
}
