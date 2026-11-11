package rpalocate

import (
	"fmt"
	"strconv"
	"strings"
)

// Element 是 DOM 快照中的一个元素节点。
type Element struct {
	Tag      string
	Attrs    map[string]string
	Parent   *Element
	Children []*Element
	text     string
}

// Attr 返回元素的属性值。
func (e *Element) Attr(name string) (string, bool) {
	v, ok := e.Attrs[name]
	return v, ok
}

// TextContent 返回元素及其后代拼接并归一化后的可见文本。
func (e *Element) TextContent() string {
	var b strings.Builder
	var walk func(*Element)
	walk = func(el *Element) {
		b.WriteString(el.text)
		b.WriteByte(' ')
		for _, c := range el.Children {
			walk(c)
		}
	}
	walk(e)
	return normalize(b.String())
}

// Elements 按文档前序返回自身及全部后代元素（不含文档根）。
func (e *Element) Elements() []*Element {
	var out []*Element
	var walk func(*Element)
	walk = func(el *Element) {
		if el.Tag != "#document" {
			out = append(out, el)
		}
		for _, c := range el.Children {
			walk(c)
		}
	}
	walk(e)
	return out
}

// Path 返回元素的绝对位置路径，形如 /html[1]/body[1]/main[1]/button[2]。
func (e *Element) Path() string {
	var parts []string
	for el := e; el != nil && el.Tag != "#document"; el = el.Parent {
		n := 1
		if el.Parent != nil {
			for _, s := range el.Parent.Children {
				if s == el {
					break
				}
				if s.Tag == el.Tag {
					n++
				}
			}
		}
		parts = append([]string{fmt.Sprintf("%s[%d]", el.Tag, n)}, parts...)
	}
	return "/" + strings.Join(parts, "/")
}

// FindByXPath 支持 XPath 的最小子集：//tag[n] 后代定位与
// /a[1]/b[2] 形式的绝对位置路径，用于兼容旧版单 XPath 定位器。
func FindByXPath(root *Element, path string) (*Element, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("rpalocate: 空 XPath")
	}
	if strings.HasPrefix(path, "//") {
		tag, n, err := parseStep(strings.TrimPrefix(path, "//"))
		if err != nil {
			return nil, err
		}
		count := 0
		for _, el := range root.Elements() {
			if el.Tag == tag {
				count++
				if count == n {
					return el, nil
				}
			}
		}
		return nil, fmt.Errorf("rpalocate: XPath %q 未匹配任何元素", path)
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("rpalocate: 仅支持 //tag[n] 或绝对路径，得到 %q", path)
	}
	cur := []*Element{root}
	for _, step := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		tag, n, err := parseStep(step)
		if err != nil {
			return nil, err
		}
		var next []*Element
		for _, c := range cur {
			count := 0
			for _, ch := range c.Children {
				if ch.Tag == tag {
					count++
					if count == n {
						next = append(next, ch)
					}
				}
			}
		}
		if len(next) == 0 {
			return nil, fmt.Errorf("rpalocate: XPath %q 在 %q 处未匹配", path, step)
		}
		cur = next
	}
	return cur[0], nil
}

func parseStep(s string) (string, int, error) {
	if i := strings.IndexByte(s, '['); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return "", 0, fmt.Errorf("rpalocate: 非法的路径步 %q", s)
		}
		n, err := strconv.Atoi(s[i+1 : len(s)-1])
		if err != nil || n < 1 {
			return "", 0, fmt.Errorf("rpalocate: 非法的位置谓词 %q", s)
		}
		return strings.ToLower(s[:i]), n, nil
	}
	if s == "" {
		return "", 0, fmt.Errorf("rpalocate: 空的路径步")
	}
	return strings.ToLower(s), 1, nil
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// ParseHTML 把 HTML 快照解析为元素树，返回文档根（Tag 为 "#document"）。
func ParseHTML(doc string) (*Element, error) {
	root := &Element{Tag: "#document", Attrs: map[string]string{}}
	cur := root
	i := 0
	for i < len(doc) {
		if doc[i] != '<' {
			j := strings.IndexByte(doc[i:], '<')
			if j < 0 {
				j = len(doc) - i
			}
			cur.text += doc[i : i+j]
			i += j
			continue
		}
		switch {
		case strings.HasPrefix(doc[i:], "<!--"):
			end := strings.Index(doc[i+4:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("rpalocate: 未闭合的注释")
			}
			i += 4 + end + 3
		case strings.HasPrefix(doc[i:], "<!"), strings.HasPrefix(doc[i:], "<?"):
			end := strings.IndexByte(doc[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("rpalocate: 未闭合的声明")
			}
			i += end + 1
		case strings.HasPrefix(doc[i:], "</"):
			end := strings.IndexByte(doc[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("rpalocate: 未闭合的结束标签")
			}
			name := strings.ToLower(strings.TrimSpace(doc[i+2 : i+end]))
			for el := cur; el != root; el = el.Parent {
				if el.Tag == name {
					cur = el.Parent
					break
				}
			}
			i += end + 1
		default:
			el, selfClosing, n, err := parseOpenTag(doc[i:])
			if err != nil {
				return nil, err
			}
			el.Parent = cur
			cur.Children = append(cur.Children, el)
			i += n
			if el.Tag == "script" || el.Tag == "style" {
				lower := strings.ToLower(doc[i:])
				if close := strings.Index(lower, "</"+el.Tag); close >= 0 {
					gt := strings.IndexByte(doc[i+close:], '>')
					if gt >= 0 {
						i += close + gt + 1
						continue
					}
				}
				continue
			}
			if !selfClosing && !voidElements[el.Tag] {
				cur = el
			}
		}
	}
	return root, nil
}

func parseOpenTag(s string) (el *Element, selfClosing bool, consumed int, err error) {
	i := 1
	start := i
	for i < len(s) && !isSpace(s[i]) && s[i] != '>' && s[i] != '/' {
		i++
	}
	name := strings.ToLower(s[start:i])
	if name == "" {
		return nil, false, 0, fmt.Errorf("rpalocate: 非法的标签")
	}
	el = &Element{Tag: name, Attrs: map[string]string{}}
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return nil, false, 0, fmt.Errorf("rpalocate: 标签 <%s> 未闭合", name)
		}
		if s[i] == '>' {
			return el, false, i + 1, nil
		}
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '>' {
			return el, true, i + 2, nil
		}
		start = i
		for i < len(s) && !isSpace(s[i]) && s[i] != '=' && s[i] != '>' && s[i] != '/' {
			i++
		}
		attrName := strings.ToLower(s[start:i])
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		val := ""
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && isSpace(s[i]) {
				i++
			}
			if i < len(s) && (s[i] == '"' || s[i] == '\'') {
				q := s[i]
				i++
				end := strings.IndexByte(s[i:], q)
				if end < 0 {
					return nil, false, 0, fmt.Errorf("rpalocate: 属性 %s 的引号未闭合", attrName)
				}
				val = s[i : i+end]
				i += end + 1
			} else {
				start = i
				for i < len(s) && !isSpace(s[i]) && s[i] != '>' {
					i++
				}
				val = s[start:i]
			}
		}
		if attrName != "" {
			el.Attrs[attrName] = val
		}
	}
	return nil, false, 0, fmt.Errorf("rpalocate: 标签 <%s> 未闭合", name)
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}
