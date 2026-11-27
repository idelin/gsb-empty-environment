package samlsec

// NodeKind 标识自定义 DOM 节点的类型。刻意不复用 encoding/xml 的 token
// 接口，因为验证流程需要稳定的父子关系、命名空间栈以及对注释/PI 的显式
// 保留（在规范化阶段才能正确丢弃或拒绝）。
type NodeKind int

const (
	KindElement NodeKind = iota
	KindText
	KindComment
	KindProcInst
)

// Attr 保留属性的命名空间 URI、原始前缀与值。xmlns 声明同样以 Attr 表达，
// 此时 IsNamespace 为 true，Prefix 为声明前缀（默认命名空间前缀为空串）。
type Attr struct {
	Space       string
	Local       string
	Prefix      string
	Value       string
	IsNamespace bool
}

// Node 是严格解析后的最小 DOM 节点。
type Node struct {
	Kind     NodeKind
	Space    string
	Local    string
	Prefix   string
	Attrs    []*Attr
	Children []*Node
	Parent   *Node

	// Text 仅 KindText 使用，保留 XML 解码后的原始字符（实体已展开）。
	Text string
	// Comment 仅 KindComment 使用，不含注释定界符。
	Comment string
	// Target/Instruction 仅 KindProcInst 使用。
	Target      string
	Instruction string

	// 该元素起始标签上显式声明的命名空间绑定，按文档顺序保留。
	nsDecls []nsBinding
}

type nsBinding struct {
	prefix string
	uri    string
}

// Document 是一次严格解析的产物。
type Document struct {
	Root *Node

	// idIndex 覆盖 ID 与 xml:id 两类属性，用于 Reference 解析与重复 ID 检测。
	idIndex map[string]*Node

	assertionCount int
	signatureCount int
}

// AttrValue 返回元素上非命名空间属性值；ok 为 false 表示属性不存在。
func (n *Node) AttrValue(space, local string) (string, bool) {
	for _, a := range n.Attrs {
		if a.IsNamespace {
			continue
		}
		if a.Space == space && a.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// ID 返回元素的 ID / xml:id 属性值。
func (n *Node) ID() (string, bool) {
	if v, ok := n.AttrValue("", "ID"); ok {
		return v, true
	}
	return n.AttrValue(NSXML, "id")
}

// Is 返回元素的命名空间与本地名联合判定。
func (n *Node) Is(space, local string) bool {
	return n != nil && n.Kind == KindElement && n.Space == space && n.Local == local
}

// FindChild 返回第一个匹配的直接子元素。
func (n *Node) FindChild(space, local string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Is(space, local) {
			return c
		}
	}
	return nil
}

// ChildElements 返回全部匹配的直接子元素。
func (n *Node) ChildElements(space, local string) []*Node {
	var out []*Node
	if n == nil {
		return out
	}
	for _, c := range n.Children {
		if c.Is(space, local) {
			out = append(out, c)
		}
	}
	return out
}

// FindDescendant 返回文档序上第一个匹配的后代元素。
func (n *Node) FindDescendant(space, local string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Is(space, local) {
			return c
		}
		if hit := c.FindDescendant(space, local); hit != nil {
			return hit
		}
	}
	return nil
}

// FindAllDescendant 返回文档序上全部匹配的后代元素。
func (n *Node) FindAllDescendant(space, local string) []*Node {
	var out []*Node
	if n == nil {
		return out
	}
	for _, c := range n.Children {
		if c.Is(space, local) {
			out = append(out, c)
		}
		out = append(out, c.FindAllDescendant(space, local)...)
	}
	return out
}

// TextContent 按文档序拼接元素全部后代文本节点。注释与 PI 不会被拼入，
// 从根本上避免常规解析器“拼接文本节点”导致的注释截断问题。
func (n *Node) TextContent() string {
	if n == nil {
		return ""
	}
	var b []byte
	var walk func(*Node)
	walk = func(cur *Node) {
		for _, c := range cur.Children {
			switch c.Kind {
			case KindText:
				b = append(b, c.Text...)
			case KindElement:
				walk(c)
			}
		}
	}
	walk(n)
	return string(b)
}

// Path 返回从根到当前元素的斜杠路径，仅用于错误信息与调试。
func (n *Node) Path() string {
	if n == nil {
		return ""
	}
	var parts []string
	for cur := n; cur != nil; cur = cur.Parent {
		name := cur.Local
		if cur.Prefix != "" {
			name = cur.Prefix + ":" + name
		}
		parts = append(parts, name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	path := ""
	for _, p := range parts {
		path += "/" + p
	}
	return path
}
