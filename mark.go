package netsec

// computeTaint is the first pass. It walks the whole graph from root
// and returns the set of confidential nodes: every node reachable from
// any Secret-tagged node, inclusive. Taint flows forward only, so a
// node reachable from both a public and a secret path is tainted.
// The traversal is iterative so adversarially deep graphs cannot
// overflow the goroutine stack.
func computeTaint(root *Value) map[*Value]bool {
	visited := make(map[*Value]bool)
	tainted := make(map[*Value]bool)
	var taintStack []*Value

	stack := []*Value{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if v == nil || visited[v] {
			continue
		}
		visited[v] = true

		if v.Secret {
			taintStack = append(taintStack, v)
			for len(taintStack) > 0 {
				t := taintStack[len(taintStack)-1]
				taintStack = taintStack[:len(taintStack)-1]
				if t == nil || tainted[t] {
					continue
				}
				tainted[t] = true
				taintStack = append(taintStack, children(t)...)
			}
		}

		stack = append(stack, children(v)...)
	}
	return tainted
}
