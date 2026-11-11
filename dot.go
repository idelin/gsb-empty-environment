package wafdecision

import (
	"fmt"
	"strings"
)

// DOT renders the decision DAG in Graphviz DOT format. It is intended for
// operational debugging and documentation.
func (g *Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph wafdecision {\n")
	b.WriteString("  rankdir=TB;\n")
	for id, nd := range g.nodes {
		switch nd.Kind {
		case NodeTest:
			fmt.Fprintf(&b, "  n%d [shape=diamond,label=\"L%d (cost %d)\"];\n",
				id, nd.Leaf, g.costs[nd.Leaf])
		case NodeAllow:
			fmt.Fprintf(&b, "  n%d [shape=box,style=rounded,label=\"ALLOW\"];\n", id)
		case NodeDeny:
			fmt.Fprintf(&b, "  n%d [shape=box,style=rounded,label=\"DENY\"];\n", id)
		}
	}
	for id, nd := range g.nodes {
		if nd.Kind != NodeTest {
			continue
		}
		fmt.Fprintf(&b, "  n%d -> n%d [label=\"false\"];\n", id, nd.OnFalse)
		fmt.Fprintf(&b, "  n%d -> n%d [label=\"true\"];\n", id, nd.OnTrue)
	}
	b.WriteString("}\n")
	return b.String()
}
