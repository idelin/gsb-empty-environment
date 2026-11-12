package msglang

// traceForProductTree rebuilds a product proof trace from an actual accepted
// tree and its product derivation. The derivation already records the chosen
// child states at every node, which is essential for non-deterministic
// automata where a label/state pair may have several rules.
func traceForProductTree(product *Automaton, tree *Node, derivation *Derivation) *trace {
	steps := derivation.Steps
	index := 0
	var walk func(*Node) *trace
	walk = func(node *Node) *trace {
		step := steps[index]
		index++
		if node.IsLeaf() {
			return &trace{
				leaf: true,
				leafRule: &LeafRule{
					Label: node.Label,
					Kind:  node.Leaf.Kind,
					Exact: step.Exact,
					Want:  append([]byte(nil), node.Leaf.Value...),
					State: step.State,
				},
				state:  step.State,
				node:   node,
				cost:   1,
				height: 0,
			}
		}
		children := make([]*trace, len(node.Children))
		for i, child := range node.Children {
			children[i] = walk(child)
		}
		return &trace{
			leaf: false,
			rule: &Rule{
				Label: node.Label,
				Child: append([]string(nil), step.Children...),
				State: step.State,
			},
			children: children,
			state:    step.State,
			node:     node,
			cost:     node.NodeCount(),
			height:   node.Height(),
		}
	}
	return walk(tree)
}
