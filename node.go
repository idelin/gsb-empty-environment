package tablegrid

import "strings"

// Node 是采集器解析后的节点树需要实现的最小接口。
//
// 元素节点的 Tag 返回小写元素名（例如 "table"、"td"、"th"）；
// 文本节点返回空字符串，文本内容由 Text 返回。
// 表格展开只依赖标签名、单元格属性与树状结构，与具体的解析器解耦。
type Node interface {
	// Tag 返回元素的小写标签名；文本节点返回 ""。
	Tag() string
	// Attr 返回指定属性的原始字符串，属性缺失时返回 ("", false)。
	Attr(name string) (string, bool)
	// Child 返回第 i 个子节点，子节点超出范围时返回 nil。
	Child(i int) Node
	// NumChildren 返回子节点数量。
	NumChildren() int
	// Text 返回文本节点的内容，元素节点可返回空字符串。
	Text() string
}

// AttrInt 解析整型属性，符合 HTML 规范的解析规则：
// 非法、缺失或非正数均回退到 fallback；解析失败的前导数字仍按整数取值。
func AttrInt(n Node, name string, fallback int) int {
	raw, ok := n.Attr(name)
	if !ok {
		return fallback
	}
	value, ok := attrLeadingInt(raw)
	if !ok || value == 0 {
		return fallback
	}
	return value
}

// attrLeadingInt 解析字符串开头的十进制整数（允许前导空白）。
// 不存在前导数字时 ok 为 false。
func attrLeadingInt(raw string) (value int, ok bool) {
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
		ok = true
	}
	return value, ok
}

// TextOf 收集节点子树中按文档顺序出现的全部文本节点内容。
func TextOf(n Node) string {
	var b strings.Builder
	collectText(n, &b)
	return strings.TrimSpace(b.String())
}

func collectText(n Node, b *strings.Builder) {
	if n == nil {
		return
	}
	if n.Tag() == "" {
		b.WriteString(n.Text())
		return
	}
	for i := 0; i < n.NumChildren(); i++ {
		collectText(n.Child(i), b)
	}
}
