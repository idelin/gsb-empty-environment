// Package proof compresses acyclic derivation trees into a shared graph.
//
// A derivation is built bottom-up through a Builder, which interns nodes:
// two nodes are identical (and therefore shared) exactly when they use the
// same rule, state the same conclusion, and have the same premises in the
// same order. Nodes that merely share a conclusion but differ in premises
// or premise order are never merged.
//
// Verify checks the graph topologically: every node is visited only after
// all of its premises, and each conclusion must follow from its premises'
// conclusions under one of the public, finite rules of a RuleSet. Cycles
// are rejected.
package proof

import (
	"fmt"
	"strconv"
	"strings"
)

// Rule is a public inference rule with a fixed number of premises.
// Check reports whether conclusion is derivable from the given premise
// conclusions under this rule. A rule of arity 0 is an axiom schema.
type Rule struct {
	Name  string
	Arity int
	Check func(conclusion string, premises []string) bool
}

// RuleSet is the finite, public collection of rules a proof may use.
type RuleSet struct {
	rules map[string]Rule
}

// NewRuleSet builds a RuleSet. Rule names must be unique and non-empty,
// and every rule must provide a Check function.
func NewRuleSet(rules ...Rule) (*RuleSet, error) {
	rs := &RuleSet{rules: make(map[string]Rule, len(rules))}
	for _, r := range rules {
		if r.Name == "" {
			return nil, fmt.Errorf("proof: rule with empty name")
		}
		if r.Arity < 0 {
			return nil, fmt.Errorf("proof: rule %q has negative arity", r.Name)
		}
		if r.Check == nil {
			return nil, fmt.Errorf("proof: rule %q has no Check function", r.Name)
		}
		if _, dup := rs.rules[r.Name]; dup {
			return nil, fmt.Errorf("proof: duplicate rule %q", r.Name)
		}
		rs.rules[r.Name] = r
	}
	return rs, nil
}

func (rs *RuleSet) lookup(name string) (Rule, bool) {
	r, ok := rs.rules[name]
	return r, ok
}

// Node is a single derivation step in the shared graph. Nodes are created
// exclusively through Builder.Apply and are immutable once created.
type Node struct {
	owner    *Builder
	id       int
	rule     string
	concl    string
	premises []*Node
}

// ID returns the node's builder-local identity, assigned in creation order.
func (n *Node) ID() int { return n.id }

// Rule returns the name of the rule applied at this node.
func (n *Node) Rule() string { return n.rule }

// Conclusion returns the content derived at this node.
func (n *Node) Conclusion() string { return n.concl }

// Premises returns the node's premises in order.
func (n *Node) Premises() []*Node {
	out := make([]*Node, len(n.premises))
	copy(out, n.premises)
	return out
}

// Builder constructs derivation graphs with structural sharing (hash
// consing). All nodes created by a Builder form a DAG by construction:
// a node can only reference nodes created earlier.
type Builder struct {
	rules  *RuleSet
	intern map[string]*Node
	nodes  []*Node
}

// NewBuilder returns a Builder that verifies against rs.
func NewBuilder(rs *RuleSet) *Builder {
	return &Builder{rules: rs, intern: make(map[string]*Node)}
}

// internKey encodes rule, conclusion, and the ordered premise identities.
// Fields are quoted so the encoding is unambiguous.
func internKey(rule, concl string, premises []*Node) string {
	var sb strings.Builder
	sb.WriteString(strconv.Quote(rule))
	sb.WriteByte(0)
	sb.WriteString(strconv.Quote(concl))
	for _, p := range premises {
		sb.WriteByte(0)
		sb.WriteString(strconv.Itoa(p.id))
	}
	return sb.String()
}

// Apply creates (or reuses) the node for applying the named rule to the
// given premises, deriving conclusion. If an identical node — same rule,
// same conclusion, same premises in the same order — already exists, that
// node is returned instead. Premises must belong to this builder.
func (b *Builder) Apply(rule, conclusion string, premises ...*Node) (*Node, error) {
	for _, p := range premises {
		if p == nil {
			return nil, fmt.Errorf("proof: nil premise for rule %q", rule)
		}
		if p.owner != b {
			return nil, fmt.Errorf("proof: premise %d belongs to a different builder", p.id)
		}
	}
	key := internKey(rule, conclusion, premises)
	if n, ok := b.intern[key]; ok {
		return n, nil
	}
	n := &Node{
		owner:    b,
		id:       len(b.nodes),
		rule:     rule,
		concl:    conclusion,
		premises: append([]*Node(nil), premises...),
	}
	b.intern[key] = n
	b.nodes = append(b.nodes, n)
	return n, nil
}

// Axiom creates (or reuses) a premise-less node using a rule of arity 0.
func (b *Builder) Axiom(rule, conclusion string) (*Node, error) {
	return b.Apply(rule, conclusion)
}

// Verify checks that the subgraph reachable from roots is a valid DAG
// derivation under the builder's RuleSet. Nodes are checked in topological
// order: a node is validated only after all of its premises. Each node's
// rule must exist in the RuleSet, match the premise count, and its Check
// must accept the conclusion. Any cycle is reported as an error.
func (b *Builder) Verify(roots ...*Node) error {
	const (
		white = iota // unvisited
		gray         // on the current DFS path
		black        // fully checked
	)
	color := make(map[*Node]int)
	checked := make(map[*Node]bool)

	// check validates a single node once its premises are known valid.
	check := func(n *Node) error {
		if checked[n] {
			return nil
		}
		rule, ok := b.rules.lookup(n.rule)
		if !ok {
			return fmt.Errorf("proof: node %d uses unknown rule %q", n.id, n.rule)
		}
		if rule.Arity != len(n.premises) {
			return fmt.Errorf("proof: node %d rule %q expects %d premises, has %d",
				n.id, n.rule, rule.Arity, len(n.premises))
		}
		prems := make([]string, len(n.premises))
		for i, p := range n.premises {
			prems[i] = p.concl
		}
		if !rule.Check(n.concl, prems) {
			return fmt.Errorf("proof: node %d conclusion %q not derivable by rule %q",
				n.id, n.concl, n.rule)
		}
		checked[n] = true
		return nil
	}

	// Iterative post-order DFS; gray revisited means a back edge, i.e. a cycle.
	type frame struct {
		n    *Node
		next int
	}
	for _, root := range roots {
		if root == nil {
			return fmt.Errorf("proof: nil root")
		}
		if color[root] == black {
			continue
		}
		stack := []frame{{n: root}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			n := top.n
			if top.next == 0 {
				if color[n] == gray {
					return fmt.Errorf("proof: cycle detected at node %d (%q)", n.id, n.concl)
				}
				if color[n] == black {
					stack = stack[:len(stack)-1]
					continue
				}
				color[n] = gray
			}
			if top.next < len(n.premises) {
				child := n.premises[top.next]
				top.next++
				if child == nil {
					return fmt.Errorf("proof: node %d has nil premise", n.id)
				}
				switch color[child] {
				case gray:
					return fmt.Errorf("proof: cycle detected through node %d (%q)", child.id, child.concl)
				case black:
					// already checked
				default:
					stack = append(stack, frame{n: child})
				}
				continue
			}
			color[n] = black
			if err := check(n); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
		}
	}
	return nil
}

// Tree is a fully expanded (unshared) derivation tree.
type Tree struct {
	Rule       string
	Conclusion string
	Premises   []*Tree
}

// Expand unrolls the shared graph below n into a plain tree. Shared
// subderivations are duplicated, so the result may be exponentially
// larger than the graph. It is intended for comparison and testing.
func Expand(n *Node) *Tree {
	t := &Tree{Rule: n.rule, Conclusion: n.concl}
	for _, p := range n.premises {
		t.Premises = append(t.Premises, Expand(p))
	}
	return t
}

// Equal reports whether two trees are structurally identical: same rule,
// same conclusion, and pairwise equal premises in the same order.
func (t *Tree) Equal(o *Tree) bool {
	if t == nil || o == nil {
		return t == o
	}
	if t.Rule != o.Rule || t.Conclusion != o.Conclusion {
		return false
	}
	if len(t.Premises) != len(o.Premises) {
		return false
	}
	for i := range t.Premises {
		if !t.Premises[i].Equal(o.Premises[i]) {
			return false
		}
	}
	return true
}

// Equal reports whether two nodes represent the same derivation when
// fully expanded, regardless of how sharing was arranged. The nodes may
// come from different builders.
func Equal(a, b *Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	return Expand(a).Equal(Expand(b))
}
