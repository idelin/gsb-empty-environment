package rpalocate

// Attr 是录制时保存的一个稳定属性锚点。
type Attr struct {
	Name  string
	Value string
}

// 相对邻居锚点的关系类型。
const (
	// RelPrevSibling 表示前一个兄弟元素的文本。
	RelPrevSibling = "prev-sibling"
	// RelSectionHeading 表示最近祖先区块中、位于目标之前的标题文本。
	RelSectionHeading = "section-heading"
)

// Neighbor 是录制时保存的一个相对邻居锚点。
type Neighbor struct {
	Relation string
	Text     string
}

// Locator 是录制产物：描述目标元素的多锚点定位器。
// XPath 仅作为兼容旧运行时的参考信息，Locate 不依赖它。
type Locator struct {
	XPath     string
	Tag       string
	Text      string
	Role      string
	Attrs     []Attr
	Neighbors []Neighbor
}

// stableAttrNames 是改版后通常仍然稳定的属性，录制时按此顺序保存。
var stableAttrNames = []string{
	"id", "name", "type", "value", "href",
	"aria-label", "title", "placeholder", "class",
}

// Record 在录制时从目标元素提取多锚点定位器。
func Record(el *Element) Locator {
	loc := Locator{
		XPath: el.Path(),
		Tag:   el.Tag,
		Text:  el.TextContent(),
		Role:  roleOf(el),
	}
	for _, name := range stableAttrNames {
		if v, ok := el.Attr(name); ok && normalize(v) != "" {
			loc.Attrs = append(loc.Attrs, Attr{Name: name, Value: normalize(v)})
		}
	}
	if prev := prevElementSibling(el); prev != nil {
		if t := prev.TextContent(); t != "" {
			loc.Neighbors = append(loc.Neighbors, Neighbor{Relation: RelPrevSibling, Text: t})
		}
	}
	if h := sectionHeading(el); h != "" {
		loc.Neighbors = append(loc.Neighbors, Neighbor{Relation: RelSectionHeading, Text: h})
	}
	return loc
}

func prevElementSibling(el *Element) *Element {
	if el.Parent == nil {
		return nil
	}
	for i, c := range el.Parent.Children {
		if c == el {
			if i == 0 {
				return nil
			}
			return el.Parent.Children[i-1]
		}
	}
	return nil
}

// sectionHeading 在祖先链上找最近的、含有位于 el 之前标题的祖先，
// 返回该标题文本（如订单卡片上的 "订单 #1024"）。
func sectionHeading(el *Element) string {
	idx := docOrder(el)
	for a := el.Parent; a != nil; a = a.Parent {
		for _, h := range a.Elements() {
			if isHeading(h.Tag) && idx[h] < idx[el] {
				return h.TextContent()
			}
		}
	}
	return ""
}

func isHeading(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func docOrder(el *Element) map[*Element]int {
	root := el
	for root.Parent != nil {
		root = root.Parent
	}
	idx := map[*Element]int{}
	for i, e := range root.Elements() {
		idx[e] = i
	}
	return idx
}
