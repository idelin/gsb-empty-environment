package msglang

import (
	"bytes"
	"fmt"
	"strings"
)

// LeafRule maps a typed leaf symbol to a state.
//
// When Exact is true the rule fires only for the literal value Want of the
// declared Kind. When Exact is false the rule fires for any value of Kind;
// Want is ignored. An empty Want with Exact true models an empty field.
type LeafRule struct {
	Label string
	Kind  LeafKind
	Exact bool
	Want  []byte
	State string
}

// Rule is an internal (ranked) transition:
// Label(Child[0], ..., Child[arity-1]) -> State.
type Rule struct {
	Label string
	Child []string
	State string
}

// Automaton is a bottom-up non-deterministic finite labeled-tree automaton
// (NFTA). States are strings; leaf symbols are typed.
type Automaton struct {
	Name   string
	States []string
	Final  []string
	Leaves []LeafRule
	Rules  []Rule

	compiled *compiled
}

// Step is one bottom-up reduction in a parsing derivation.
type Step struct {
	NodeLabel string
	Leaf      *Leaf
	Children  []string // child states, in tree order
	State     string   // resulting state
	Exact     bool     // whether a leaf rule matched an exact value
}

// Derivation is an accepting parsing path as an ordered list of reductions.
// Steps appear in postorder: every child reduction precedes its parent.
type Derivation struct {
	Automaton string
	Final     string
	Steps     []Step
}

type leafIndex struct {
	rule  LeafRule
	order int
}

type internalIndex struct {
	rule  Rule
	order int
}

type compiled struct {
	state    map[string]int
	final    map[string]bool
	leaf     map[string][]leafIndex // label -> rules, declaration order
	internal map[string][]internalIndex
}

// Validate checks structural well-formedness and compiles lookup indexes.
func (a *Automaton) Validate() error {
	if a == nil {
		return fmt.Errorf("msglang: nil automaton")
	}
	if len(a.States) == 0 {
		return fmt.Errorf("msglang: automaton %q has no states", a.Name)
	}
	stateSet := make(map[string]bool, len(a.States))
	for _, state := range a.States {
		if state == "" {
			return fmt.Errorf("msglang: automaton %q has an empty state name", a.Name)
		}
		if stateSet[state] {
			return fmt.Errorf("msglang: automaton %q declares duplicate state %q", a.Name, state)
		}
		stateSet[state] = true
	}
	if len(a.Final) == 0 {
		return fmt.Errorf("msglang: automaton %q has no final states", a.Name)
	}
	for _, state := range a.Final {
		if !stateSet[state] {
			return fmt.Errorf("msglang: final state %q not declared in automaton %q", state, a.Name)
		}
	}
	leafLabels := make(map[string]bool)
	for i, rule := range a.Leaves {
		if rule.Label == "" {
			return fmt.Errorf("msglang: leaf rule %d in %q has empty label", i, a.Name)
		}
		if !rule.Kind.Valid() {
			return fmt.Errorf("msglang: leaf rule %q in %q has invalid kind", rule.Label, a.Name)
		}
		if rule.State == "" || !stateSet[rule.State] {
			return fmt.Errorf("msglang: leaf rule %q in %q targets undeclared state %q", rule.Label, a.Name, rule.State)
		}
		if leafLabels[rule.Label] {
			return fmt.Errorf("msglang: duplicate leaf label %q in %q", rule.Label, a.Name)
		}
		leafLabels[rule.Label] = true
	}
	for i, rule := range a.Rules {
		if rule.Label == "" {
			return fmt.Errorf("msglang: rule %d in %q has empty label", i, a.Name)
		}
		if leafLabels[rule.Label] {
			return fmt.Errorf("msglang: label %q in %q is used by both a leaf and an internal rule", rule.Label, a.Name)
		}
		if rule.State == "" || !stateSet[rule.State] {
			return fmt.Errorf("msglang: rule %q in %q targets undeclared state %q", rule.Label, a.Name, rule.State)
		}
		for j, child := range rule.Child {
			if child == "" || !stateSet[child] {
				return fmt.Errorf("msglang: rule %q in %q references undeclared child state %q at position %d", rule.Label, a.Name, child, j)
			}
		}
	}
	a.compiled = &compiled{
		state:    make(map[string]int, len(a.States)),
		final:    make(map[string]bool, len(a.Final)),
		leaf:     make(map[string][]leafIndex, len(a.Leaves)),
		internal: make(map[string][]internalIndex, len(a.Rules)),
	}
	for i, state := range a.States {
		a.compiled.state[state] = i
	}
	for _, state := range a.Final {
		a.compiled.final[state] = true
	}
	for i, rule := range a.Leaves {
		a.compiled.leaf[rule.Label] = append(a.compiled.leaf[rule.Label], leafIndex{rule: rule, order: i})
	}
	for i, rule := range a.Rules {
		a.compiled.internal[rule.Label] = append(a.compiled.internal[rule.Label], internalIndex{rule: rule, order: i})
	}
	return nil
}

func (a *Automaton) ensureCompiled() error {
	if a.compiled != nil {
		return nil
	}
	return a.Validate()
}

// Rank returns the arity of an internal labeled symbol, or -1 for unknown
// labels and leaf symbols.
func (a *Automaton) Rank(label string) int {
	if err := a.ensureCompiled(); err != nil {
		return -1
	}
	if idx, ok := a.compiled.internal[label]; ok {
		return len(idx[0].rule.Child)
	}
	return -1
}

// Accept reports whether tree is accepted and, when it is, the derivation.
func (a *Automaton) Accept(tree *Node) (bool, *Derivation, error) {
	if err := a.ensureCompiled(); err != nil {
		return false, nil, err
	}
	if err := tree.Validate(); err != nil {
		return false, nil, err
	}
	type frame struct {
		node  *Node
		step  map[string]Step // state -> reduction reaching it at this node
		child []*frame        // child frames, nil for leaves
	}
	var walk func(*Node) (*frame, error)
	walk = func(node *Node) (*frame, error) {
		current := &frame{node: node, step: map[string]Step{}}
		if node.IsLeaf() {
			for _, idx := range a.compiled.leaf[node.Label] {
				rule := idx.rule
				if rule.Kind != node.Leaf.Kind {
					continue
				}
				if rule.Exact && !bytes.Equal(rule.Want, node.Leaf.Value) {
					continue
				}
				if _, seen := current.step[rule.State]; !seen {
					current.step[rule.State] = Step{
						NodeLabel: node.Label,
						Leaf:      &Leaf{Kind: node.Leaf.Kind, Value: append([]byte(nil), node.Leaf.Value...)},
						State:     rule.State,
						Exact:     rule.Exact,
					}
				}
			}
			// No matching leaf rule means the node simply does not reduce to
			// any state of this automaton (a non-member, not a malformed tree).
			return current, nil
		}

		current.child = make([]*frame, len(node.Children))
		for i, child := range node.Children {
			frame, err := walk(child)
			if err != nil {
				return nil, err
			}
			current.child[i] = frame
		}
		expectedArity := -1
		for _, idx := range a.compiled.internal[node.Label] {
			expectedArity = len(idx.rule.Child)
			break
		}
		if expectedArity >= 0 && expectedArity != len(node.Children) {
			return current, nil
		}
		for _, idx := range a.compiled.internal[node.Label] {
			rule := idx.rule
			if len(rule.Child) != len(node.Children) {
				continue
			}
			ok := true
			for i, want := range rule.Child {
				if _, reachable := current.child[i].step[want]; !reachable {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			if _, seen := current.step[rule.State]; seen {
				continue
			}
			current.step[rule.State] = Step{
				NodeLabel: node.Label,
				Children:  append([]string(nil), rule.Child...),
				State:     rule.State,
			}
		}
		return current, nil
	}

	root, err := walk(tree)
	if err != nil {
		return false, nil, err
	}
	if len(root.step) == 0 {
		return false, nil, nil
	}
	finalState := ""
	for _, state := range a.Final {
		if _, reachable := root.step[state]; reachable {
			finalState = state
			break
		}
	}
	if finalState == "" {
		return false, nil, nil
	}

	steps := make([]Step, 0, tree.NodeCount())
	var build func(*frame, string)
	build = func(current *frame, state string) {
		step := current.step[state]
		if current.node.IsLeaf() {
			steps = append(steps, step)
			return
		}
		for i, childState := range step.Children {
			build(current.child[i], childState)
		}
		steps = append(steps, step)
	}
	build(root, finalState)
	return true, &Derivation{Automaton: a.Name, Final: finalState, Steps: steps}, nil
}

// String renders a derivation as one reduction per line.
func (d *Derivation) String() string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "automaton %q -> final %s\n", d.Automaton, d.Final)
	for i, step := range d.Steps {
		fmt.Fprintf(&b, "  %d: %s\n", i+1, step)
	}
	return b.String()
}

// String renders a single bottom-up reduction.
func (s Step) String() string {
	if s.Leaf != nil {
		match := "any"
		if s.Exact {
			match = "exact"
		}
		return fmt.Sprintf("%s[%s:%s](%s) -> %s", s.NodeLabel, s.Leaf.Kind, formatLeafValue(s.Leaf.Value), match, s.State)
	}
	return fmt.Sprintf("%s(%s) -> %s", s.NodeLabel, strings.Join(s.Children, ", "), s.State)
}
