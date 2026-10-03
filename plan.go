package relalg

import (
	"fmt"
	"strings"
)

// Plan 是关系代数逻辑算子树的节点接口。
type Plan interface {
	// Children 返回子算子（叶子算子返回 nil）。
	Children() []Plan
	// Schema 返回该算子输出的限定列名（如 u.name）。
	Schema() []string
	// Aliases 返回该子树中出现的表引用名（别名）。
	Aliases() []string
	nodeString() string
}

// Scan 表扫描算子（叶子节点）。
// Columns 为该表在查询中被引用的列（无系统目录时的已知列集合）。
type Scan struct {
	Table   string
	Alias   string
	Columns []string
}

// Ref 返回表引用名（别名优先，否则为表名）。
func (s *Scan) Ref() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Table
}

func (s *Scan) Children() []Plan { return nil }

func (s *Scan) Schema() []string {
	out := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		out[i] = s.Ref() + "." + c
	}
	return out
}

func (s *Scan) Aliases() []string { return []string{s.Ref()} }

func (s *Scan) nodeString() string {
	if s.Alias != "" && s.Alias != s.Table {
		return fmt.Sprintf("Scan(%s AS %s)", s.Table, s.Alias)
	}
	return fmt.Sprintf("Scan(%s)", s.Table)
}

// Project 投影算子（一元）。
type Project struct {
	Exprs []Expr
	Input Plan
}

func (p *Project) Children() []Plan { return []Plan{p.Input} }

func (p *Project) Schema() []string {
	var out []string
	for _, e := range p.Exprs {
		switch c := e.(type) {
		case *ColumnRef:
			out = append(out, c.String())
		case *Star:
			if c.Table == "" {
				return p.Input.Schema()
			}
			prefix := c.Table + "."
			for _, col := range p.Input.Schema() {
				if strings.HasPrefix(col, prefix) {
					out = append(out, col)
				}
			}
		default:
			out = append(out, e.String())
		}
	}
	return out
}

func (p *Project) Aliases() []string { return p.Input.Aliases() }

func (p *Project) nodeString() string {
	parts := make([]string, len(p.Exprs))
	for i, e := range p.Exprs {
		parts[i] = e.String()
	}
	return "Project(" + strings.Join(parts, ", ") + ")"
}

// Filter 选择（谓词过滤）算子（一元）。
type Filter struct {
	Cond  Expr
	Input Plan
}

func (f *Filter) Children() []Plan { return []Plan{f.Input} }
func (f *Filter) Schema() []string { return f.Input.Schema() }
func (f *Filter) Aliases() []string {
	return f.Input.Aliases()
}
func (f *Filter) nodeString() string {
	return "Filter(" + f.Cond.String() + ")"
}

// Join 连接算子（二元）。
type Join struct {
	Kind  string // INNER / LEFT / RIGHT
	Cond  Expr
	Left  Plan
	Right Plan
}

func (j *Join) Children() []Plan { return []Plan{j.Left, j.Right} }

func (j *Join) Schema() []string {
	out := append([]string{}, j.Left.Schema()...)
	return append(out, j.Right.Schema()...)
}

func (j *Join) Aliases() []string {
	out := append([]string{}, j.Left.Aliases()...)
	return append(out, j.Right.Aliases()...)
}

func (j *Join) nodeString() string {
	if j.Cond == nil {
		return "Join(" + j.Kind + ")"
	}
	return "Join(" + j.Kind + ", " + j.Cond.String() + ")"
}

// Walk 先序遍历算子树，对每个节点调用 visit。
func Walk(p Plan, visit func(Plan)) {
	visit(p)
	for _, c := range p.Children() {
		Walk(c, visit)
	}
}

// Format 将算子树序列化为带缩进连接线的多行文本，例如：
//   Project(u.name)
//   └── Join(INNER, u.id = o.uid)
//       ├── Scan(users AS u)
//       └── Scan(orders AS o)
func Format(root Plan) string {
	lines := []string{root.nodeString()}
	var rec func(p Plan, prefix string)
	rec = func(p Plan, prefix string) {
		children := p.Children()
		for i, c := range children {
			last := i == len(children)-1
			conn := "├── "
			next := prefix + "│   "
			if last {
				conn = "└── "
				next = prefix + "    "
			}
			lines = append(lines, prefix+conn+c.nodeString())
			rec(c, next)
		}
	}
	rec(root, "")
	return strings.Join(lines, "\n")
}
