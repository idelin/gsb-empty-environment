package layermerge

import (
	"fmt"
	"strings"
)

// Dump renders the tree with one node per line, annotated with kind and
// source layer, e.g. "etc/ (dir) [layer 0]".
func (n *Node) Dump() string {
	var b strings.Builder
	b.WriteString(n.describe(true) + "\n")
	if n.Kind == KindDir {
		dumpChildren(&b, n, "")
	}
	return b.String()
}

func dumpChildren(b *strings.Builder, dir *Node, prefix string) {
	names := dir.sortedNames()
	for i, name := range names {
		last := i == len(names)-1
		connector, next := "├── ", prefix+"│   "
		if last {
			connector, next = "└── ", prefix+"    "
		}
		child := dir.Children[name]
		b.WriteString(prefix + connector + child.describe(false) + "\n")
		if child.Kind == KindDir {
			dumpChildren(b, child, next)
		}
	}
}

func (n *Node) describe(root bool) string {
	name := n.Name
	if root {
		name = "/"
	}
	s := fmt.Sprintf("%s (%s", name, n.Kind)
	switch n.Kind {
	case KindSymlink, KindHardlink:
		s += " -> " + n.Target
		if n.Kind == KindHardlink && n.Resolved != nil {
			s += fmt.Sprintf(" => %s", n.Resolved.Kind)
		}
	}
	s += ")"
	if n.Layer >= 0 {
		s += fmt.Sprintf(" [layer %d]", n.Layer)
	}
	return s
}
