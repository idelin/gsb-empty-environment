package contentmatch

// automaton is an Aho-Corasick multi-pattern automaton shared by all rules.
// It is immutable after build and therefore safe for concurrent use by any
// number of streams.
type automaton struct {
	nodes []acNode
}

type acNode struct {
	next map[byte]int
	fail int
	out  []int // IDs of all patterns ending here, incl. via failure links
}

func newAutomaton() *automaton {
	return &automaton{nodes: []acNode{{next: make(map[byte]int)}}}
}

func (a *automaton) add(content []byte, id int) {
	n := 0
	for _, b := range content {
		c, ok := a.nodes[n].next[b]
		if !ok {
			c = len(a.nodes)
			a.nodes = append(a.nodes, acNode{next: make(map[byte]int)})
			a.nodes[n].next[b] = c
		}
		n = c
	}
	a.nodes[n].out = append(a.nodes[n].out, id)
}

// build computes failure links and merges output lists along them, so that
// after build every node's out lists exactly the patterns ending at or
// suffix-below that node.
func (a *automaton) build() {
	queue := make([]int, 0, len(a.nodes))
	for _, c := range a.nodes[0].next {
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for b, u := range a.nodes[v].next {
			f := a.nodes[v].fail
			for f != 0 {
				if t, ok := a.nodes[f].next[b]; ok {
					a.nodes[u].fail = t
					break
				}
				f = a.nodes[f].fail
			}
			if f == 0 {
				if t, ok := a.nodes[0].next[b]; ok && t != u {
					a.nodes[u].fail = t
				}
			}
			if fl := a.nodes[u].fail; fl != 0 {
				a.nodes[u].out = append(a.nodes[u].out, a.nodes[fl].out...)
			}
			queue = append(queue, u)
		}
	}
}

// step advances the automaton by one byte.
func (a *automaton) step(state int, b byte) int {
	for state != 0 {
		if t, ok := a.nodes[state].next[b]; ok {
			return t
		}
		state = a.nodes[state].fail
	}
	if t, ok := a.nodes[0].next[b]; ok {
		return t
	}
	return 0
}
