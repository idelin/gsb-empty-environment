package samlsec

import (
	"sort"
	"strings"
)

// c14nContext 表示规范化输出中“当前已经写出”的命名空间作用域。
// key 为前缀（默认命名空间前缀是空串），value 为命名空间 URI。
type c14nContext map[string]string

func cloneContext(c c14nContext) c14nContext {
	out := make(c14nContext, len(c)+2)
	for k, v := range c {
		out[k] = v
	}
	return out
}

// inheritedContext 沿祖先链收集目标元素可见的全部命名空间声明，
// 用于 SignedInfo 这类“从中间节点开始规范化”的场景。
func inheritedContext(n *Node) c14nContext {
	ctx := c14nContext{"xml": NSXML}
	var chain []*Node
	for cur := n; cur != nil; cur = cur.Parent {
		chain = append(chain, cur)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for _, b := range chain[i].nsDecls {
			ctx[b.prefix] = b.uri
		}
	}
	return ctx
}

// canonicalExclusive 实现 Exclusive XML Canonicalization（无注释版本）子集。
// skipSignature 非空时，作为 enveloped signature transform 跳过该 ds:Signature
// 子树（其祖先命名空间仍参与可见性分析）。
func canonicalExclusive(target, skipSignature *Node) ([]byte, error) {
	var b strings.Builder
	// 规范化从目标元素自身开始：Exc-C14N 只输出目标子树内“可见利用”的
	// 命名空间，因此渲染目标时的输出上下文必须为空。inherited 仅用于
	// 前缀解析（merged 仍会合并祖先绑定），不用于决定是否输出 xmlns。
	if err := renderElement(&b, target, skipSignature,
		c14nContext{"xml": NSXML}, c14nContext{"xml": NSXML}); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func renderElement(b *strings.Builder, el, skip *Node, outCtx, resolveCtx c14nContext) error {
	if el == skip {
		return nil
	}

	// 1. 准备两套上下文：
	//   - nextResolve：解析上下文，包含祖先链上的全部原始命名空间绑定，
	//     仅用于把 QName 前缀还原成 URI；
	//   - merged：输出上下文，在父输出上下文基础上并入祖先原始绑定与
	//     本元素声明，用于判定某个 xmlns 是否已经在输出中出现过。
	nextResolve := cloneContext(resolveCtx)
	for _, decl := range el.nsDecls {
		nextResolve[decl.prefix] = decl.uri
	}
	for k, v := range inheritedContext(el) {
		if _, ok := nextResolve[k]; !ok {
			nextResolve[k] = v
		}
	}
	// merged 是传给子节点的输出上下文：父级已渲染的 outCtx 加上本元素
	// 即将渲染的声明（下面 decls 计算后填充），不含“仅原始可见”的祖先绑定。
	merged := cloneContext(outCtx)

	// 2. 收集“可见性利用”的命名空间：元素自身 QName、属性 QName 以及
	// 本元素上显式声明的前缀。Exc-C14N 只输出这些前缀。
	utilized := map[string]bool{}
	if el.Prefix != "" {
		utilized[el.Prefix] = true
	}
	for _, a := range el.Attrs {
		if !a.IsNamespace && a.Prefix != "" {
			utilized[a.Prefix] = true
		}
	}
	// 元素处于默认命名空间时，空前缀本身就是可见利用的命名空间。
	if el.Prefix == "" && el.Space != "" {
		utilized[""] = true
	}

	// 3. 渲染输出上下文中尚不存在（或被重新声明为不同 URI）的可见利用
	// 绑定。绑定可能声明在祖先节点上（通过 nextResolve 还原 URI），
	// Exc-C14N 要求在首次利用的元素处将其输出。
	type kv struct{ prefix, uri string }
	var decls []kv
	for prefix := range utilized {
		uri, ok := nextResolve[prefix]
		if !ok {
			// 找不到命名空间绑定的 QName 无法规范化，直接失败而不是猜测。
			return wrapErr("c14n", ErrMalformed,
				"unresolved namespace prefix %q at %s", prefix, el.Path())
		}
		if prior, ok := outCtx[prefix]; ok && prior == uri {
			continue
		}
		decls = append(decls, kv{prefix, uri})
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].prefix < decls[j].prefix })
	for _, d := range decls {
		merged[d.prefix] = d.uri
	}

	// 4. 非命名空间属性按 nsURI + localName 字典序排列。命名空间轴与
	// 普通属性轴在 C14N 中独立排序，且命名空间轴恒在普通属性之前。
	var attrs []*Attr
	for _, a := range el.Attrs {
		if !a.IsNamespace {
			attrs = append(attrs, a)
		}
	}
	sort.Slice(attrs, func(i, j int) bool {
		if attrs[i].Space != attrs[j].Space {
			return attrs[i].Space < attrs[j].Space
		}
		return attrs[i].Local < attrs[j].Local
	})

	// 5. 输出起始标签。
	b.WriteByte('<')
	writeQName(b, el.Prefix, el.Local)
	for _, d := range decls {
		if d.prefix == "" {
			b.WriteString(` xmlns="`)
		} else {
			b.WriteString(` xmlns:`)
			b.WriteString(d.prefix)
			b.WriteString(`="`)
		}
		writeAttrValue(b, d.uri)
		b.WriteByte('"')
	}
	for _, a := range attrs {
		b.WriteByte(' ')
		writeQName(b, a.Prefix, a.Local)
		b.WriteString(`="`)
		writeAttrValue(b, a.Value)
		b.WriteByte('"')
	}
	b.WriteByte('>')

	// 6. 递归子节点。注释被 Exc-C14N 无注释版本丢弃；PI 仅输出其内容；
	// 文本节点按规范化转义规则输出。
	for _, c := range el.Children {
		switch c.Kind {
		case KindElement:
			if err := renderElement(b, c, skip, merged, nextResolve); err != nil {
				return err
			}
		case KindText:
			writeText(b, c.Text)
		case KindProcInst:
			b.WriteString(`<?`)
			b.WriteString(c.Target)
			if c.Instruction != "" {
				b.WriteByte(' ')
				b.WriteString(c.Instruction)
			}
			b.WriteString(`?>`)
		case KindComment:
			// 无注释 C14N：静默丢弃。
		}
	}

	b.WriteString(`</`)
	writeQName(b, el.Prefix, el.Local)
	b.WriteByte('>')
	return nil
}

func writeQName(b *strings.Builder, prefix, local string) {
	if prefix != "" {
		b.WriteString(prefix)
		b.WriteByte(':')
	}
	b.WriteString(local)
}

// writeText 实现 C14N 文本节点转义：& < > 以及回车符。
func writeText(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteByte(s[i])
		}
	}
}

// writeAttrValue 实现 C14N 属性值转义：在文本转义基础上额外处理引号、
// 制表符与换行符（属性空白规范化在解析阶段已由解码器完成）。
func writeAttrValue(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#x9;")
		case '\n':
			b.WriteString("&#xA;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteByte(s[i])
		}
	}
}
