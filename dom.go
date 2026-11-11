package streamsel

import "strings"

// Node is a node in the naive full-document tree. Tag == "" marks a text
// node (with the content in Text); the root is an element node with Tag "#root".
type Node struct {
	Tag      string
	Text     string // text nodes only
	Attrs    map[string]string
	Children []*Node
	Parent   *Node
}

// ParseDOM builds the full document tree in memory. It shares the tokenizer
// and repair logic with the streaming matcher, so the two agree on the
// interpretation of malformed markup; it differs only in keeping everything.
func ParseDOM(html string) *Node {
	root := &Node{Tag: "#root"}
	b := &domBuilder{stack: []*Node{root}}
	p := parser{ev: b}
	p.feed([]byte(html))
	p.close()
	return root
}

type domBuilder struct {
	stack []*Node
}

func (b *domBuilder) top() *Node { return b.stack[len(b.stack)-1] }

func (b *domBuilder) startTag(name string, attrs []attr) {
	n := &Node{Tag: name, Parent: b.top()}
	if len(attrs) > 0 {
		n.Attrs = make(map[string]string, len(attrs))
		for _, a := range attrs {
			n.Attrs[a.name] = a.value
		}
	}
	top := b.top()
	top.Children = append(top.Children, n)
	b.stack = append(b.stack, n)
}

func (b *domBuilder) endTag(name string) {
	b.stack = b.stack[:len(b.stack)-1]
}

func (b *domBuilder) text(s string) {
	top := b.top()
	top.Children = append(top.Children, &Node{Text: s, Parent: top})
}

// TextContent returns the concatenation of all descendant text, like the
// DOM textContent property.
func TextContent(n *Node) string {
	var sb strings.Builder
	writeText(n, &sb)
	return sb.String()
}

func writeText(n *Node, sb *strings.Builder) {
	if n.Tag == "" {
		sb.WriteString(n.Text)
		return
	}
	for _, c := range n.Children {
		writeText(c, sb)
	}
}

// QueryAll is the naive baseline: it walks the fully built tree and returns
// the text content of every element matching the selector, in document order.
func QueryAll(root *Node, sel string) ([]string, error) {
	s, err := parseSelector(sel)
	if err != nil {
		return nil, err
	}
	var out []string
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if c.Tag == "" {
				continue
			}
			if matchSteps(c, s.steps, len(s.steps)-1) {
				out = append(out, TextContent(c))
			}
			walk(c)
		}
	}
	walk(root)
	return out, nil
}

// matchSteps reports whether n satisfies steps[i], with steps[0..i-1]
// satisfied by ancestors according to the combinators.
func matchSteps(n *Node, steps []selectorStep, i int) bool {
	if !steps[i].sel.matchesMap(n.Tag, n.Attrs) {
		return false
	}
	if i == 0 {
		return true
	}
	if steps[i].comb == combChild {
		p := n.Parent
		return p != nil && p.Tag != "#root" && matchSteps(p, steps, i-1)
	}
	for p := n.Parent; p != nil && p.Tag != "#root"; p = p.Parent {
		if matchSteps(p, steps, i-1) {
			return true
		}
	}
	return false
}
