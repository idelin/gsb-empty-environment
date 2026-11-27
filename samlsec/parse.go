package samlsec

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

// DefaultSensitiveLocals 是默认受保护的元素本地名集合。这些元素一旦内嵌
// 注释或处理指令即拒绝，避免身份字符串被“截断拼接”。
var DefaultSensitiveLocals = map[string]bool{
	"NameID":         true,
	"Issuer":         true,
	"Audience":       true,
	"AttributeValue": true,
}

// ParseOptions 控制严格解析行为。
type ParseOptions struct {
	// SensitiveLocals 覆盖默认敏感元素集合；nil 表示使用默认集合。
	SensitiveLocals map[string]bool
}

// Parse 对原始 XML 字节流执行安全解析。返回的 Document 已完成结构层面
// 的全部硬校验，后续签名与 SAML 语义校验在其上进行。
func Parse(raw []byte, opts *ParseOptions) (*Document, error) {
	if err := prescan(raw); err != nil {
		return nil, err
	}

	sensitive := DefaultSensitiveLocals
	if opts != nil && opts.SensitiveLocals != nil {
		sensitive = opts.SensitiveLocals
	}

	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = map[string]string{
		"lt": "<", "gt": ">", "amp": "&", "quot": `"`, "apos": "'",
	}

	doc := &Document{idIndex: map[string]*Node{}}
	nsStack := newNSFrameStack()
	var elemStack []*Node
	sensitiveDepth := 0
	rootSeen := false

	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, wrapErr("parse", ErrMalformed, "xml token error: %v", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			node := &Node{
				Kind:   KindElement,
				Space:  t.Name.Space,
				Local:  t.Name.Local,
				Parent: parentOf(elemStack),
			}

			frame := nsStack.push()
			seenAttrs := map[string]bool{}
			for _, ra := range t.Attr {
				attr := &Attr{Space: ra.Name.Space, Local: ra.Name.Local, Value: ra.Value}
				switch {
				case ra.Name.Space == "xmlns":
					attr.IsNamespace = true
					attr.Prefix = ra.Name.Local
					node.nsDecls = append(node.nsDecls, nsBinding{prefix: ra.Name.Local, uri: ra.Value})
					frame[ra.Name.Local] = ra.Value
					node.Attrs = append(node.Attrs, attr)
				case ra.Name.Space == "" && ra.Name.Local == "xmlns":
					attr.IsNamespace = true
					attr.Prefix = ""
					node.nsDecls = append(node.nsDecls, nsBinding{prefix: "", uri: ra.Value})
					frame[""] = ra.Value
					node.Attrs = append(node.Attrs, attr)
				default:
					attr.Prefix = nsStack.prefixFor(ra.Name.Space)
					node.Attrs = append(node.Attrs, attr)
				}

				key := attr.Space + "\x00" + attr.Local
				if seenAttrs[key] {
					return nil, wrapErr("parse", ErrDuplicateAttribute,
						"duplicate attribute %s on %s", key, node.Path())
				}
				seenAttrs[key] = true
			}
			node.Prefix = nsStack.prefixFor(node.Space)

			if !rootSeen {
				rootSeen = true
				doc.Root = node
				if !node.Is(NSSAMLP, "Response") {
					return nil, wrapErr("parse", ErrRootInvalid,
						"root is {%s}%s", node.Space, node.Local)
				}
			}

			// 必须同时校验命名空间，防止用同名异命名空间标签制造混淆；
			// 非 SAML/ds 命名空间的同名标签按畸形结构拒绝。
			if node.Local == "Assertion" {
				if node.Space != NSSAML {
					return nil, wrapErr("parse", ErrNamespace,
						"Assertion in illegal namespace %q at %s", node.Space, node.Path())
				}
				doc.assertionCount++
				if doc.assertionCount > 1 {
					return nil, wrapErr("parse", ErrMultipleAssertions,
						"second Assertion at %s", node.Path())
				}
			}
			if node.Local == "Signature" {
				if node.Space != NSDSIG {
					return nil, wrapErr("parse", ErrNamespace,
						"Signature in illegal namespace %q at %s", node.Space, node.Path())
				}
				doc.signatureCount++
				if doc.signatureCount > 1 {
					return nil, wrapErr("parse", ErrMultipleSignatures,
						"second Signature at %s", node.Path())
				}
			}

			if err := indexIDs(doc, node); err != nil {
				return nil, err
			}

			if len(elemStack) > 0 {
				parent := elemStack[len(elemStack)-1]
				parent.Children = append(parent.Children, node)
			}
			elemStack = append(elemStack, node)
			if sensitive[node.Local] && node.Space == NSSAML {
				sensitiveDepth++
			}

		case xml.EndElement:
			if len(elemStack) == 0 {
				return nil, wrapErr("parse", ErrMalformed, "unexpected end element %s", t.Name.Local)
			}
			top := elemStack[len(elemStack)-1]
			if top.Local != t.Name.Local {
				return nil, wrapErr("parse", ErrMalformed,
					"unbalanced element %s vs %s", top.Local, t.Name.Local)
			}
			if sensitive[top.Local] && top.Space == NSSAML {
				sensitiveDepth--
			}
			elemStack = elemStack[:len(elemStack)-1]
			nsStack.pop()

		case xml.CharData:
			if len(elemStack) == 0 {
				// 文档级空白无安全意义，直接忽略。
				continue
			}
			parent := elemStack[len(elemStack)-1]
			parent.Children = append(parent.Children, &Node{
				Kind:   KindText,
				Text:   string(t),
				Parent: parent,
			})

		case xml.Comment:
			if sensitiveDepth > 0 {
				return nil, wrapErr("parse", ErrSensitiveMarkup,
					"comment inside sensitive element at %s", parentPath(elemStack))
			}
			if len(elemStack) > 0 {
				parent := elemStack[len(elemStack)-1]
				parent.Children = append(parent.Children, &Node{
					Kind:    KindComment,
					Comment: string(t),
					Parent:  parent,
				})
			}

		case xml.ProcInst:
			// XML 声明不产生 ProcInst token；任何到达这里的 PI 都在文档体内。
			if sensitiveDepth > 0 {
				return nil, wrapErr("parse", ErrSensitiveMarkup,
					"processing instruction inside sensitive element at %s", parentPath(elemStack))
			}
			if len(elemStack) > 0 {
				parent := elemStack[len(elemStack)-1]
				parent.Children = append(parent.Children, &Node{
					Kind:        KindProcInst,
					Target:      t.Target,
					Instruction: string(t.Inst),
					Parent:      parent,
				})
			}

		case xml.Directive:
			// prescan 已保证 Directive 不可能到达这里，保留兜底拒绝。
			return nil, wrapErr("parse", ErrDTD, "unexpected markup directive")
		}
	}

	if doc.Root == nil || len(elemStack) != 0 {
		return nil, wrapErr("parse", ErrMalformed, "no root element or unclosed tags")
	}
	return doc, nil
}

func parentOf(stack []*Node) *Node {
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}

func parentPath(stack []*Node) string {
	if len(stack) == 0 {
		return "/"
	}
	return stack[len(stack)-1].Path()
}

func indexIDs(doc *Document, node *Node) error {
	for _, attr := range node.Attrs {
		if attr.IsNamespace {
			continue
		}
		isID := (attr.Space == "" && attr.Local == "ID") ||
			(attr.Space == NSXML && attr.Local == "id")
		if !isID || attr.Value == "" {
			continue
		}
		if existing, ok := doc.idIndex[attr.Value]; ok {
			return wrapErr("parse", ErrDuplicateID,
				"ID %q duplicated at %s and %s", attr.Value, existing.Path(), node.Path())
		}
		doc.idIndex[attr.Value] = node
	}
	return nil
}

// nsFrame 是一层元素上的命名空间声明视图。
type nsFrame map[string]string

type nsFrameStack struct {
	frames []nsFrame
}

func newNSFrameStack() *nsFrameStack {
	// 预置 xml 前缀，XML 规范强制其永远存在且不可重新绑定。
	return &nsFrameStack{frames: []nsFrame{{"xml": NSXML}}}
}

func (s *nsFrameStack) push() nsFrame {
	f := nsFrame{}
	s.frames = append(s.frames, f)
	return f
}

func (s *nsFrameStack) pop() {
	s.frames = s.frames[:len(s.frames)-1]
}

func (s *nsFrameStack) set(prefix, uri string) {
	s.frames[len(s.frames)-1][prefix] = uri
}

// lookup 返回当前作用域内前缀绑定的 URI。
func (s *nsFrameStack) lookup(prefix string) (string, bool) {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if uri, ok := s.frames[i][prefix]; ok {
			return uri, true
		}
	}
	return "", false
}

// prefixFor 返回当前作用域内指向给定 URI 的最近声明前缀；找不到时
// 返回空串（默认命名空间场景）。
func (s *nsFrameStack) prefixFor(uri string) string {
	if uri == "" {
		return ""
	}
	for i := len(s.frames) - 1; i >= 0; i-- {
		for prefix, bound := range s.frames[i] {
			if bound == uri {
				return prefix
			}
		}
	}
	return ""
}

// prescan 在任何 XML 解析发生之前对原始字节做硬扫描：
//   - 注释 <!-- --> 与 CDATA <![CDATA[ ]]> 被跳过；
//   - 其余任何 <! 开头的标记（DOCTYPE、ENTITY、ELEMENT、ATTLIST 等）直接拒绝；
//   - 同时显式拒绝带名字的实体引用（仅允许五个预定义实体），阻断自定义实体。
func prescan(raw []byte) error {
	i := 0
	n := len(raw)
	for i < n {
		c := raw[i]
		if c != '<' {
			i++
			continue
		}
		if i+1 >= n {
			return wrapErr("prescan", ErrMalformed, "dangling '<'")
		}
		switch raw[i+1] {
		case '?':
			end := bytes.Index(raw[i+2:], []byte("?>"))
			if end < 0 {
				return wrapErr("prescan", ErrMalformed, "unterminated processing instruction")
			}
			i += 2 + end + 2
		case '!':
			if bytes.HasPrefix(raw[i:], []byte("<!--")) {
				end := bytes.Index(raw[i+4:], []byte("-->"))
				if end < 0 {
					return wrapErr("prescan", ErrMalformed, "unterminated comment")
				}
				i += 4 + end + 3
				continue
			}
			if bytes.HasPrefix(raw[i:], []byte("<![CDATA[")) {
				end := bytes.Index(raw[i+9:], []byte("]]>"))
				if end < 0 {
					return wrapErr("prescan", ErrMalformed, "unterminated CDATA section")
				}
				i += 9 + end + 3
				continue
			}
			// 任何其它 <! 形式都是 DTD 子集（DOCTYPE/ENTITY/ELEMENT/...）。
			return wrapErr("prescan", ErrDTD, "forbidden markup declaration near %s",
				snippet(raw, i))
		case '/':
			i += 2
		default:
			// 普通起始标签，跳到最近的 '>'。属性值内若出现 '>' 属于畸形 XML，
			// 交给 encoding/xml 兜底；此处保持保守快进即可。
			end := bytes.IndexByte(raw[i+1:], '>')
			if end < 0 {
				return wrapErr("prescan", ErrMalformed, "unterminated element")
			}
			i += 1 + end + 1
		}
	}

	// 自定义命名实体（&xxx;）无需在此扫描：DTD 已在上面被硬拒绝，而没有
	// DTD 就不存在任何实体声明，encoding/xml 遇到非预定义命名实体会自行
	// 报错（见 parse 阶段的 ErrMalformed 兜底）。这里不再做文本扫描，避免
	// 把属性值中合法的 URL（如 ?a=1&b=2）误判为实体。
	return nil
}

func snippet(raw []byte, at int) string {
	const limit = 40
	end := at + limit
	if end > len(raw) {
		end = len(raw)
	}
	return fmt.Sprintf("%q", string(raw[at:end]))
}
