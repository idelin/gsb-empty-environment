package memo

// 本文件定义物理计划节点、精确整型代价模型接口与行数估计。

// 物理算子种类。
const (
	PhysScan      = "Scan"
	PhysHashJoin  = "HashJoin"
	PhysMergeJoin = "MergeJoin"
	PhysSort      = "Sort"
	PhysExchange  = "Exchange"
)

// Plan 是一棵物理计划树。
type Plan interface {
	// Kind 返回物理算子名（Phys* 常量）。
	Kind() string
	// Cost 返回该计划（含所有子节点）的精确整型总代价。
	Cost() int64
	// Props 返回该计划输出的实际物理属性。
	Props() PhysProps
	// Rows 返回输出行数估计。
	Rows() int64
	// Children 返回子计划。
	Children() []Plan
	// PlanKey 返回与枚举器共享的确定性结构键，用于计划等价性比对。
	PlanKey() string
}

type basePlan struct {
	cost  int64
	props PhysProps
	rows  int64
}

func (p basePlan) Cost() int64      { return p.cost }
func (p basePlan) Props() PhysProps { return p.props }
func (p basePlan) Rows() int64      { return p.rows }

// ScanPlan 是基表扫描物理计划。
type ScanPlan struct {
	basePlan
	Table string
}

func (p *ScanPlan) Kind() string     { return PhysScan }
func (p *ScanPlan) Children() []Plan { return nil }
func (p *ScanPlan) PlanKey() string {
	b := newKeyBuilder()
	b.writeString(PhysScan)
	b.writeByte('(')
	b.writeString(p.Table)
	b.writeByte(')')
	return b.string()
}

// JoinPlan 是 HashJoin / MergeJoin 物理计划。
type JoinPlan struct {
	basePlan
	kind     string
	keys     []JoinCond
	left     Plan
	right    Plan
	enforced bool // 是否为满足分布需求而显式插入了 Exchange 子计划（在 Children 中可见）。
}

func (p *JoinPlan) Kind() string { return p.kind }
func (p *JoinPlan) Children() []Plan {
	return []Plan{p.left, p.right}
}
func (p *JoinPlan) PlanKey() string {
	b := newKeyBuilder()
	b.writeString(p.kind)
	b.writeByte('(')
	b.writeString(p.left.PlanKey())
	b.writeByte(',')
	b.writeString(p.right.PlanKey())
	b.writeByte(';')
	for i, k := range p.keys {
		if i > 0 {
			b.writeByte('&')
		}
		b.writeString(k.Left)
		b.writeByte('=')
		b.writeString(k.Right)
	}
	b.writeByte(')')
	return b.string()
}

// SortPlan 是 Sort Enforcer。
type SortPlan struct {
	basePlan
	cols  []string
	child Plan
}

func (p *SortPlan) Kind() string     { return PhysSort }
func (p *SortPlan) Children() []Plan { return []Plan{p.child} }
func (p *SortPlan) PlanKey() string {
	b := newKeyBuilder()
	b.writeString(PhysSort)
	b.writeByte('[')
	for i, c := range p.cols {
		if i > 0 {
			b.writeByte(',')
		}
		b.writeString(c)
	}
	b.writeString("](")
	b.writeString(p.child.PlanKey())
	b.writeByte(')')
	return b.string()
}

// ExchangePlan 是 Exchange Enforcer（按指定分布重分区/汇聚）。
type ExchangePlan struct {
	basePlan
	child Plan
}

func (p *ExchangePlan) Kind() string     { return PhysExchange }
func (p *ExchangePlan) Children() []Plan { return []Plan{p.child} }
func (p *ExchangePlan) PlanKey() string {
	b := newKeyBuilder()
	b.writeString(PhysExchange)
	b.writeByte('[')
	b.writeString(p.props.Distrib.keySegment())
	b.writeString("](")
	b.writeString(p.child.PlanKey())
	b.writeByte(')')
	return b.string()
}

// keySegment 把分布编码为确定性字符串片段。
func (d Distrib) keySegment() string {
	if d.Singleton {
		return "S"
	}
	if d.Hash {
		b := newKeyBuilder()
		b.writeString("H:")
		for i, c := range d.Columns {
			if i > 0 {
				b.writeByte(',')
			}
			b.writeString(c)
		}
		return b.string()
	}
	return "N"
}

// CostModel 给出五类物理算子的精确整型代价公式。
//
// 所有公式均为确定性整数运算，便于与穷举器逐值对齐。
type CostModel interface {
	// ScanCost 基表扫描代价，参数为基表行数。
	ScanCost(rows int64) int64
	// HashJoinCost 哈希连接代价，参数为左/右输入行数与输出行数。
	HashJoinCost(leftRows, rightRows, outRows int64) int64
	// MergeJoinCost 归并连接代价，参数为左/右输入行数与输出行数。
	MergeJoinCost(leftRows, rightRows, outRows int64) int64
	// SortCost 排序代价，参数为输入行数与排序列数。
	SortCost(rows int64, sortCols int) int64
	// ExchangeCost 重分区/汇聚代价，参数为输入行数与分布键列数。
	ExchangeCost(rows int64, distCols int) int64
}

// DefaultCostModel 是一组精确整型默认公式，可整体替换为自定义实现。
type DefaultCostModel struct{}

func (DefaultCostModel) ScanCost(rows int64) int64 { return rows }

func (DefaultCostModel) HashJoinCost(leftRows, rightRows, outRows int64) int64 {
	// 建表侧 + 探测侧 + 结果输出。
	return leftRows + rightRows + outRows
}

func (DefaultCostModel) MergeJoinCost(leftRows, rightRows, outRows int64) int64 {
	// 有序归并无需建哈希表，单位代价低于 HashJoin。
	return (leftRows+rightRows)/2 + outRows
}

func (DefaultCostModel) SortCost(rows int64, sortCols int) int64 {
	return rows * int64(sortCols) * 2
}

func (DefaultCostModel) ExchangeCost(rows int64, distCols int) int64 {
	return rows*int64(distCols) + rows
}

// estimateRows 估计连接输出行数：默认取两侧行数的最大值（无选择率统计时的
// 保守整型估计）。扫描行数直接取基表统计。
func estimateRows(leftRows, rightRows int64) int64 {
	if leftRows > rightRows {
		return leftRows
	}
	return rightRows
}
