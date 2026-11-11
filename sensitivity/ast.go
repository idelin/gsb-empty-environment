package sensitivity

import "fmt"

// Node 是敏感度 AST 的公共接口。具体节点类型：
// [Source]、[BoundedCount]、[TruncatedSum]、[Project]、[Union]、[Join]。
type Node interface {
	nodeMarker()
	// Name 返回节点在推导报告中使用的稳定名称。
	Name() string
	// children 返回直接子节点，供推导器与核验器自底向上遍历。
	children() []Node
}

// Source 是基础关系叶子节点。单条输入变化定义为该表中一行的
// 加入或删除，因此行级敏感度恒为 1。
type Source struct {
	ID   string
	Rel  string
	Cols []string
}

// BoundedCount 是有界计数：先对输入行数计数，超过 Cap 则截断为 Cap。
// 截断不会降低单条变化的影响（计数仍可能 ±1），敏感度等于输入行级
// 敏感度。Cap 是容量上界（求值期约束），必须为非负整数。
type BoundedCount struct {
	ID  string
	In  Node
	Cap int64
}

// TruncatedSum 是截断求和：输入每行的 Col 列先夹取到 [Lower, Upper]
// 再累加。单条变化的最大贡献为 max(|Lower|, |Upper|)，总敏感度为
// 输入行级敏感度乘以该贡献上界。任一端点为 nil 表示该侧无界，
// 此时敏感度未知。
type TruncatedSum struct {
	ID    string
	In    Node
	Col   string
	Lower Cell
	Upper Cell
}

// Project 对输入表做列投影，丢弃 Drop 中的列。投影不改变行数，
// 行级敏感度与输入相同。
type Project struct {
	ID   string
	In   Node
	Drop []string
}

// Union 是两个同模式子计划的并集（UNION ALL 语义）。单条输入变化
// 只影响其中一侧，行级敏感度取两侧最大值。
type Union struct {
	ID string
	L  Node
	R  Node
}

// Join 是按已知唯一键的等值内连接。
//
// LeftKey/RightKey 是两侧连接列；LeftRepeat/RightRepeat 是显式给出的
// 重复上界：即任一键值在左/右关系中出现次数的最大值（至少为 1）。
// 键的唯一性由数据契约保证，推导器只信任显式上界：任一侧上界为
// nil，连接放大无法界定，敏感度直接返回未知。
//
// 行级敏感度 = max(左行级 × RightRepeat, 右行级 × LeftRepeat)。
type Join struct {
	ID          string
	Left        Node
	Right       Node
	LeftKey     string
	RightKey    string
	LeftRepeat  Cell
	RightRepeat Cell
}

func (s *Source) nodeMarker()       {}
func (b *BoundedCount) nodeMarker() {}
func (t *TruncatedSum) nodeMarker() {}
func (p *Project) nodeMarker()      {}
func (u *Union) nodeMarker()        {}
func (j *Join) nodeMarker()         {}

func (s *Source) Name() string       { return label(s.ID, "Source:"+s.Rel) }
func (b *BoundedCount) Name() string { return label(b.ID, "BoundedCount") }
func (t *TruncatedSum) Name() string { return label(t.ID, "TruncatedSum:"+t.Col) }
func (p *Project) Name() string      { return label(p.ID, "Project") }
func (u *Union) Name() string        { return label(u.ID, "Union") }
func (j *Join) Name() string         { return label(j.ID, "Join") }

func (s *Source) children() []Node       { return nil }
func (b *BoundedCount) children() []Node { return []Node{b.In} }
func (t *TruncatedSum) children() []Node { return []Node{t.In} }
func (p *Project) children() []Node      { return []Node{p.In} }
func (u *Union) children() []Node        { return []Node{u.L, u.R} }
func (j *Join) children() []Node         { return []Node{j.Left, j.Right} }

func label(id, kind string) string {
	if id == "" {
		return kind
	}
	return kind + "[" + id + "]"
}

// validateAST 做结构校验：节点非空、子节点非空、上界取值合法。
func validateAST(n Node) error {
	seen := map[string]bool{}
	var walk func(Node) error
	walk = func(n Node) error {
		if n == nil {
			return fmt.Errorf("sensitivity: AST 存在空节点")
		}
		key := fmt.Sprintf("%T:%s", n, n.Name())
		if seen[key] {
			return fmt.Errorf("sensitivity: AST 不是 DAG，节点 %s 被重复引用", n.Name())
		}
		seen[key] = true
		for _, c := range n.children() {
			if c == nil {
				return fmt.Errorf("sensitivity: 节点 %s 存在空子节点", n.Name())
			}
			if err := walk(c); err != nil {
				return err
			}
		}
		switch v := n.(type) {
		case *BoundedCount:
			if v.Cap < 0 {
				return fmt.Errorf("sensitivity: 节点 %s 容量上界为负", v.Name())
			}
		case *TruncatedSum:
			if v.Lower != nil && v.Upper != nil && v.Lower.Cmp(v.Upper) > 0 {
				return fmt.Errorf("sensitivity: 节点 %s 截断区间下界超过上界", v.Name())
			}
		case *Join:
			if v.LeftRepeat != nil && v.LeftRepeat.Sign() <= 0 {
				return fmt.Errorf("sensitivity: 节点 %s 左重复上界必须为正", v.Name())
			}
			if v.RightRepeat != nil && v.RightRepeat.Sign() <= 0 {
				return fmt.Errorf("sensitivity: 节点 %s 右重复上界必须为正", v.Name())
			}
		}
		return nil
	}
	return walk(n)
}
