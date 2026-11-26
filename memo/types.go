package memo

// 本文件定义 Memo 求解包的输入数据模型：基表统计、逻辑连接树与物理需求属性。

import "fmt"

// Distrib 描述一个关系在执行层的分布（分区）属性。
type Distrib struct {
	// Singleton 为 true 表示整个关系位于单个节点（默认/未知分布）。
	Singleton bool
	// Hash 为 true 表示按 Columns 做哈希分区。
	Hash bool
	// Columns 为哈希分区键（Hash=true 时有效）。
	Columns []string
}

// equalDistrib 判定两个分布是否等价（哈希键按集合比较，与顺序无关）。
func equalDistrib(a, b Distrib) bool {
	if a.Singleton != b.Singleton || a.Hash != b.Hash {
		return false
	}
	if !a.Hash {
		return true
	}
	return sameColumnSet(a.Columns, b.Columns)
}

// sameColumnSet 判定两个列名切片是否为同一个集合（列名在关系内唯一）。
func sameColumnSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, c := range a {
		seen[c]++
	}
	for _, c := range b {
		seen[c]--
		if seen[c] < 0 {
			return false
		}
	}
	return true
}

// Table 是一张基表的统计信息与原生物理属性。
type Table struct {
	// Name 为基表名，在整个 Schema 内唯一。
	Name string
	// Rows 为基表行数。
	Rows int64
	// SortKeys 为基表已有的有序排序列（前缀序）。
	SortKeys []string
	// Distrib 为基表的原生分布；零值表示单节点分布。
	Distrib Distrib
}

// Schema 是查询涉及的基表目录。
type Schema struct {
	Tables []*Table
}

// table 返回指定名称的基表。
func (s *Schema) table(name string) *Table {
	for _, t := range s.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// JoinCond 是一条等值连接条件 L = R。
type JoinCond struct {
	Left  string
	Right string
}

// 逻辑节点 Kind 取值。
const (
	KindScan      = "Scan"
	KindInnerJoin = "InnerJoin"
)

// LogicalNode 是初始逻辑连接树的节点。
//
// 叶子节点：Kind=KindScan，Table 指向基表名。
// 内部节点：Kind=KindInnerJoin，Left/Right 为子树，Conds 为等值条件。
type LogicalNode struct {
	Kind  string
	Table string
	Left  *LogicalNode
	Right *LogicalNode
	Conds []JoinCond
}

// NewScan 构造基表扫描逻辑节点。
func NewScan(table string) *LogicalNode {
	return &LogicalNode{Kind: KindScan, Table: table}
}

// NewInnerJoin 构造等值内连接逻辑节点。
func NewInnerJoin(left, right *LogicalNode, conds []JoinCond) *LogicalNode {
	return &LogicalNode{
		Kind:  KindInnerJoin,
		Left:  left,
		Right: right,
		Conds: append([]JoinCond(nil), conds...),
	}
}

// validate 校验逻辑树并确保所有基表在 Schema 中存在。
func (n *LogicalNode) validate(schema *Schema) error {
	switch n.Kind {
	case KindScan:
		if schema.table(n.Table) == nil {
			return fmt.Errorf("memo: scan references unknown table %q", n.Table)
		}
		return nil
	case KindInnerJoin:
		if n.Left == nil || n.Right == nil {
			return fmt.Errorf("memo: inner join requires two children")
		}
		if len(n.Conds) == 0 {
			return fmt.Errorf("memo: inner join requires at least one equi-condition")
		}
		if err := n.Left.validate(schema); err != nil {
			return err
		}
		return n.Right.validate(schema)
	default:
		return fmt.Errorf("memo: unknown logical node kind %q", n.Kind)
	}
}

// PhysProps 是需求/实际物理属性：有序排序列与分布列。
//
// 零值表示无排序要求、单节点分布。
type PhysProps struct {
	// SortCols 为所需（前缀）排序列；nil 表示无排序要求。
	SortCols []string
	// Distrib 为所需分布；零值表示单节点分布。
	Distrib Distrib
}

// key 把物理属性编码为缓存键使用的确定性字符串。
func (p PhysProps) key() string {
	h := newKeyBuilder()
	h.writeString("sort:")
	for _, c := range p.SortCols {
		h.writeString(c)
		h.writeByte(',')
	}
	h.writeString("|dist:")
	if p.Distrib.Singleton {
		h.writeString("S")
	} else if p.Distrib.Hash {
		h.writeString("H:")
		for _, c := range p.Distrib.Columns {
			h.writeString(c)
			h.writeByte(',')
		}
	} else {
		h.writeString("N")
	}
	return h.string()
}

// satisfies 判定实际属性 actual 是否满足需求 req。
//
//   - 排序：实际排序列必须以需求排序列为前缀。
//   - 分布：完全等价才视为满足（哈希键按集合比较）。
func satisfies(actual, req PhysProps) bool {
	if len(req.SortCols) > len(actual.SortCols) {
		return false
	}
	for i, c := range req.SortCols {
		if actual.SortCols[i] != c {
			return false
		}
	}
	// 零值分布表示“无分布需求”，任意实际分布都满足；否则要求完全等价。
	if req.Distrib.isZero() {
		return true
	}
	return equalDistrib(actual.Distrib, req.Distrib)
}

// isZero 判定分布是否为“无需求”零值。
func (d Distrib) isZero() bool {
	return !d.Singleton && !d.Hash && len(d.Columns) == 0
}
