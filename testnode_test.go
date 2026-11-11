package tablegrid

// testNode 是测试用的最小节点树实现。
type testNode struct {
	tag      string
	text     string
	attrs    map[string]string
	children []*testNode
}

func elem(tag string, children ...*testNode) *testNode {
	return &testNode{tag: tag, children: children}
}

func text(s string) *testNode {
	return &testNode{text: s}
}

func (n *testNode) with(attr, value string) *testNode {
	if n.attrs == nil {
		n.attrs = map[string]string{}
	}
	n.attrs[attr] = value
	return n
}

func (n *testNode) Tag() string { return n.tag }

func (n *testNode) Attr(name string) (string, bool) {
	v, ok := n.attrs[name]
	return v, ok
}

func (n *testNode) Child(i int) Node {
	if i < 0 || i >= len(n.children) {
		return nil
	}
	return n.children[i]
}

func (n *testNode) NumChildren() int { return len(n.children) }

func (n *testNode) Text() string { return n.text }

// cellNode 构造带文本的 td/th，文本作为文本子节点存在。
func cellNode(tag, value string, attrs ...[2]string) *testNode {
	n := elem(tag, text(value))
	for _, a := range attrs {
		n.with(a[0], a[1])
	}
	return n
}

func td(value string, attrs ...[2]string) *testNode { return cellNode("td", value, attrs...) }
func th(value string, attrs ...[2]string) *testNode { return cellNode("th", value, attrs...) }

func tr(cells ...*testNode) *testNode { return elem("tr", cells...) }

func table(children ...*testNode) *testNode { return elem("table", children...) }

func thead(rows ...*testNode) *testNode { return elem("thead", rows...) }
func tbody(rows ...*testNode) *testNode { return elem("tbody", rows...) }
func tfoot(rows ...*testNode) *testNode { return elem("tfoot", rows...) }
