package policydiff

import (
	"fmt"
	"strings"
)

// ExplainNode 是解释树节点：记录子表达式的求值结果及原因。
type ExplainNode struct {
	Desc     string         // 子表达式描述
	Value    Tri            // 三值求值结果
	Detail   string         // 叶子层面的原因说明
	Children []*ExplainNode // 复合节点的子解释
}

// Explain 评估规则并同时构建解释树，说明每个子表达式为何取该真值。
func Explain(e *Expr, env Env) *ExplainNode {
	n := &ExplainNode{Desc: e.Describe()}
	switch e.Kind {
	case AndExpr, OrExpr:
		v := False
		if e.Kind == AndExpr {
			v = True
		}
		for _, k := range e.Kids {
			c := Explain(k, env)
			n.Children = append(n.Children, c)
			if e.Kind == AndExpr {
				v = and3(v, c.Value)
			} else {
				v = or3(v, c.Value)
			}
		}
		n.Value = v
	case NotExpr:
		c := Explain(e.Kids[0], env)
		n.Children = []*ExplainNode{c}
		n.Value = not3(c.Value)
	default:
		n.Value = Eval(e, env)
		n.Detail = leafDetail(e, env, n.Value)
	}
	return n
}

func leafDetail(e *Expr, env Env, v Tri) string {
	val, ok := env[e.Attr]
	if !ok {
		return fmt.Sprintf("属性 %s 缺失 → 未知（按拒绝处理）", e.Attr)
	}
	if v == True {
		return fmt.Sprintf("%s = %s，满足条件", e.Attr, val)
	}
	return fmt.Sprintf("%s = %s，不满足条件", e.Attr, val)
}

// String 以缩进树形式渲染解释树。
func (n *ExplainNode) String() string {
	var b strings.Builder
	b.WriteString(rootLine(n))
	for i, c := range n.Children {
		renderNode(&b, c, "", i == len(n.Children)-1)
	}
	return b.String()
}

func rootLine(n *ExplainNode) string {
	s := n.Desc + " ⇒ " + n.Value.String()
	if n.Detail != "" {
		s += "（" + n.Detail + "）"
	}
	return s + "\n"
}

func renderNode(b *strings.Builder, n *ExplainNode, prefix string, last bool) {
	connector, nextPrefix := "├─ ", prefix+"│  "
	if last {
		connector, nextPrefix = "└─ ", prefix+"   "
	}
	b.WriteString(prefix + connector + n.Desc + " ⇒ " + n.Value.String())
	if n.Detail != "" {
		b.WriteString("（" + n.Detail + "）")
	}
	b.WriteString("\n")
	for i, c := range n.Children {
		renderNode(b, c, nextPrefix, i == len(n.Children)-1)
	}
}
