package sensitivity

import (
	"fmt"
	"strings"
)

// Trace 是单个 AST 节点的可复核推导记录。
type Trace struct {
	// Node 是该记录对应的 AST 节点。
	Node Node
	// Kind 为 "表" 或 "标量"。
	Kind string
	// Schema 是表节点的输出列序；标量节点为 nil。
	Schema []string
	// Sens 是推导出的敏感度。
	Sens Sensitivity
	// Formula 是人读的推导依据，包含具体数值与公式。
	Formula string
	// Inputs 是直接子节点的推导记录，按 AST 子节点顺序排列。
	Inputs []*Trace
}

// Derive 自底向上推导整棵 AST 的敏感度，返回根节点的逐节点推导树。
// 同一节点在 DAG 中被多处引用时只推导一次并复用记录。
func Derive(root Node) (*Trace, error) {
	if err := validateAST(root); err != nil {
		return nil, err
	}
	memo := map[Node]*Trace{}
	var derive func(Node) (*Trace, error)
	derive = func(n Node) (*Trace, error) {
		if t, ok := memo[n]; ok {
			return t, nil
		}
		childTraces := make([]*Trace, 0, len(n.children()))
		for _, c := range n.children() {
			t, err := derive(c)
			if err != nil {
				return nil, err
			}
			childTraces = append(childTraces, t)
		}
		t, err := deriveNode(n, childTraces)
		if err != nil {
			return nil, err
		}
		memo[n] = t
		return t, nil
	}
	return derive(root)
}

func deriveNode(n Node, in []*Trace) (*Trace, error) {
	t := &Trace{Node: n, Inputs: in}
	switch v := n.(type) {
	case *Source:
		t.Kind = "表"
		t.Schema = append([]string(nil), v.Cols...)
		t.Sens = RowBound(NewRat(1))
		t.Formula = "基础关系叶子：单条输入变化定义为一行加入/删除，行级敏感度 = 1"
	case *BoundedCount:
		t.Kind = "标量"
		child := in[0]
		if child.Sens.IsUnknown() {
			t.Sens = Unknown("子节点 " + child.Node.Name() + " 敏感度未知：" + child.Sens.Reason)
			t.Formula = "无法推导：子节点行级敏感度未知"
			break
		}
		s := child.Sens.Rows
		t.Sens = ScalarBound(ratClone(s))
		t.Formula = fmt.Sprintf(
			"count = min(行数, %d)；有界截断只限制总量，单行增删仍使计数变化 1，敏感度 = 子行级 %s",
			v.Cap, s.RatString())
	case *TruncatedSum:
		t.Kind = "标量"
		child := in[0]
		if v.Lower == nil || v.Upper == nil {
			missing := "Lower"
			if v.Upper == nil {
				missing = "Upper"
			}
			t.Sens = Unknown(fmt.Sprintf("节点 %s 缺少截断界 %s，单行贡献无上界", v.Name(), missing))
			t.Formula = "无法推导：截断求和必须同时给出 Lower 与 Upper"
			break
		}
		if _, err := schemaIndex(child.Schema, v.Col); err != nil {
			return nil, fmt.Errorf("节点 %s: %w", v.Name(), err)
		}
		if child.Sens.IsUnknown() {
			t.Sens = Unknown("子节点 " + child.Node.Name() + " 敏感度未知：" + child.Sens.Reason)
			t.Formula = "无法推导：子节点行级敏感度未知"
			break
		}
		contribution := ratMax(ratAbs(v.Lower), ratAbs(v.Upper))
		bound := mulRat(child.Sens.Rows, contribution)
		t.Sens = ScalarBound(bound)
		t.Formula = fmt.Sprintf(
			"截断后单行贡献上界 max(|%s|, |%s|) = %s；敏感度 = 子行级 %s × %s = %s",
			v.Lower.RatString(), v.Upper.RatString(), contribution.RatString(),
			child.Sens.Rows.RatString(), contribution.RatString(), bound.RatString())
	case *Project:
		t.Kind = "表"
		child := in[0]
		out, err := projectSchema(child.Schema, v.Drop)
		if err != nil {
			return nil, fmt.Errorf("节点 %s: %w", v.Name(), err)
		}
		t.Schema = out
		if child.Sens.IsUnknown() {
			t.Sens = Unknown("子节点 " + child.Node.Name() + " 敏感度未知：" + child.Sens.Reason)
			t.Formula = "无法推导：子节点行级敏感度未知"
			break
		}
		t.Sens = RowBound(ratClone(child.Sens.Rows))
		t.Formula = "投影只删除列、不改变行数，行级敏感度 = 子行级 " + child.Sens.Rows.RatString()
	case *Union:
		t.Kind = "表"
		l, r := in[0], in[1]
		if len(l.Schema) != len(r.Schema) {
			return nil, fmt.Errorf("节点 %s: 并集两侧列数不一致（%d 与 %d）",
				v.Name(), len(l.Schema), len(r.Schema))
		}
		for i := range l.Schema {
			if l.Schema[i] != r.Schema[i] {
				return nil, fmt.Errorf("节点 %s: 并集两侧第 %d 列名不一致（%q 与 %q）",
					v.Name(), i, l.Schema[i], r.Schema[i])
			}
		}
		t.Schema = append([]string(nil), l.Schema...)
		if l.Sens.IsUnknown() || r.Sens.IsUnknown() {
			reason := unknownChildReason(l, r)
			t.Sens = Unknown(reason)
			t.Formula = "无法推导：并集一侧行级敏感度未知"
			break
		}
		bound := ratMax(l.Sens.Rows, r.Sens.Rows)
		t.Sens = RowBound(bound)
		t.Formula = fmt.Sprintf(
			"UNION ALL 下单条变化只落在一侧，行级敏感度 = max(%s, %s) = %s",
			l.Sens.Rows.RatString(), r.Sens.Rows.RatString(), bound.RatString())
	case *Join:
		t.Kind = "表"
		l, r := in[0], in[1]
		if _, err := schemaIndex(l.Schema, v.LeftKey); err != nil {
			return nil, fmt.Errorf("节点 %s: %w", v.Name(), err)
		}
		if _, err := schemaIndex(r.Schema, v.RightKey); err != nil {
			return nil, fmt.Errorf("节点 %s: %w", v.Name(), err)
		}
		t.Schema = append(append([]string(nil), l.Schema...), r.Schema...)
		switch {
		case v.LeftRepeat == nil:
			t.Sens = Unknown(fmt.Sprintf("节点 %s 缺少左关系键重复上界 LeftRepeat，连接放大无界", v.Name()))
			t.Formula = "无法推导：连接左侧缺少显式重复上界"
		case v.RightRepeat == nil:
			t.Sens = Unknown(fmt.Sprintf("节点 %s 缺少右关系键重复上界 RightRepeat，连接放大无界", v.Name()))
			t.Formula = "无法推导：连接右侧缺少显式重复上界"
		case l.Sens.IsUnknown() || r.Sens.IsUnknown():
			t.Sens = Unknown(unknownChildReason(l, r))
			t.Formula = "无法推导：连接一侧子节点敏感度未知"
		default:
			leftAmp := mulRat(l.Sens.Rows, v.RightRepeat)
			rightAmp := mulRat(r.Sens.Rows, v.LeftRepeat)
			bound := ratMax(leftAmp, rightAmp)
			t.Sens = RowBound(bound)
			t.Formula = fmt.Sprintf(
				"单行变化经连接放大：左侧变化 %s×RightRepeat %s = %s；右侧变化 %s×LeftRepeat %s = %s；取 max = %s",
				l.Sens.Rows.RatString(), v.RightRepeat.RatString(), leftAmp.RatString(),
				r.Sens.Rows.RatString(), v.LeftRepeat.RatString(), rightAmp.RatString(),
				bound.RatString())
		}
	default:
		return nil, fmt.Errorf("sensitivity: 未知节点类型 %T", n)
	}
	return t, nil
}

func unknownChildReason(l, r *Trace) string {
	if l.Sens.IsUnknown() {
		return "子节点 " + l.Node.Name() + " 敏感度未知：" + l.Sens.Reason
	}
	return "子节点 " + r.Node.Name() + " 敏感度未知：" + r.Sens.Reason
}

func schemaIndex(schema []string, col string) (int, error) {
	for i, name := range schema {
		if name == col {
			return i, nil
		}
	}
	return 0, fmt.Errorf("列 %q 不存在于输出模式 %v", col, schema)
}

func projectSchema(schema, drop []string) ([]string, error) {
	dropped := map[string]bool{}
	for _, d := range drop {
		if _, err := schemaIndex(schema, d); err != nil {
			return nil, err
		}
		dropped[d] = true
	}
	out := make([]string, 0, len(schema))
	for _, name := range schema {
		if !dropped[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// Render 以缩进树形式渲染逐节点推导，供编译宿主输出复核日志。
func (t *Trace) Render() string {
	var b strings.Builder
	t.render(&b, 0)
	return b.String()
}

func (t *Trace) render(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	b.WriteString(indent)
	b.WriteString("- ")
	b.WriteString(t.Node.Name())
	b.WriteString(" <")
	b.WriteString(t.Kind)
	if t.Kind == "表" {
		b.WriteString(" 列=")
		b.WriteString(strings.Join(t.Schema, ","))
	}
	b.WriteString("> 敏感度=")
	b.WriteString(t.Sens.String())
	b.WriteString("\n")
	b.WriteString(indent)
	b.WriteString("  依据: ")
	b.WriteString(t.Formula)
	b.WriteString("\n")
	for _, c := range t.Inputs {
		c.render(b, depth+1)
	}
}
