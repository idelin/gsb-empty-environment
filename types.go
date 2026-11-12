package taskpkg

import "math/big"

// 分组规则常量。小数符与分组符不得相同。
const (
	// GroupNone 不允许出现分组符。
	GroupNone = 0
	// GroupComma 以半角逗号作为分组符，分组符不得出现在小数段，整数段每组至多 3 位。
	GroupComma = 1
	// GroupDot 以半角句点作为分组符，规则同 GroupComma。
	GroupDot = 2
	// GroupSpace 以空格（半角空格或不换行空格 U+00A0）作为分组符，整数段每组至多 3 位。
	GroupSpace = 3
)

// NoRHSColumn 表示 Relation 没有右端取值列，RHS 仅为常量。
// 由于 Go 结构体零值 0 同时是合法列下标，宿主在“不需要 RHSCol”时
// 必须显式把 RHSCol 设为 NoRHSColumn。
const NoRHSColumn = -1

// Status 是一次联合推断的终止状态。
type Status string

const (
	// StatusOK 预算内完成且至少存在一个可行组合。
	StatusOK Status = "ok"
	// StatusInfeasible 预算内完成且不存在任何可行组合，Conflicts 给出冲突关系集。
	StatusInfeasible Status = "infeasible"
	// StatusIncomplete 预算耗尽，搜索未完成，结果（如有）只是部分结果，不能宣称唯一。
	StatusIncomplete Status = "incomplete"
)

// Candidate 描述一列的一种格式/单位解释。
//
// 同一列在一次完整解释中只能选定一个 Candidate。Decimal 为小数符（',' 或 '.'）；
// Grouping 取 Group* 常量，且分组符必须与小数符不同；UnitScale 为有理单位倍率，
// 解析得到的数值会乘以该倍率后作为规范值，nil 表示倍率 1。
type Candidate struct {
	Name      string
	Decimal   byte
	Grouping  int
	UnitScale *big.Rat
}

// Relation 是一条行级线性校验关系，至多 18 条。
//
// 两种形态：
//
//   - 行内线性方程（KeyCol < 0）：对每一行 r，仅当该行所有 Coeffs 涉及列以及
//     RHSCol（若 >= 0）均有解析值时才校验
//     sum(Coeffs[c]*value[r][c]) == RHS + (RHSCol>=0 ? value[r][RHSCol] : 0)。
//     不需要右端列时必须显式设置 RHSCol = NoRHSColumn，不能依赖零值。
//   - 业务键等值（KeyCol >= 0）：对每个业务键分组，要求每个参与列
//     （Coeffs 的键与可选的 RHSCol）在该分组内所有非缺失取值彼此相等。
//     用于捕获重复业务键之间的冲突。
//
// 缺失值永远不按零处理：相关值不齐备的行/分组跳过，不施加约束。
type Relation struct {
	ID     string
	KeyCol int
	Coeffs map[int]*big.Rat
	RHS    *big.Rat
	RHSCol int
}

// Input 是联合推断的全部输入。
//
// Keys 为每行业务键（可重复）；Rows 为原始字符串单元，Rows[r][c] 为空串表示缺失。
// 列数至多 12，每列 Candidates 至多 4 个；关系至多 18 条。Budget 为检查次数预算，
// 每次“评估一个格式组合”或“针对一个关系子集做一次可满足性检查”计 1，<=0 表示不限。
type Input struct {
	Keys       []string
	Rows       [][]string
	Candidates [][]Candidate
	Relations  []Relation
	Budget     int64
}

// Combo 是一个满足全部关系的格式组合及其规范化结果。
type Combo struct {
	// Picked[c] 为第 c 列选定的候选在 Input.Candidates[c] 中的下标。
	Picked []int
	// Values[r][c] 为规范有理值；缺失单元为 nil。
	Values [][]*big.Rat
	// Signature 为规范值矩阵的稳定指纹，用于等价类归并。
	Signature string
}

// EquivalenceClass 是产生完全相同规范值矩阵的格式组合集合。
// 同一等价类内的不同组合仅在格式层面有差异，规范值不可区分。
type EquivalenceClass struct {
	Signature string
	// Members 每个元素是一列列候选下标的组合（与 Combo.Picked 同构）。
	Members [][]int
	// Representative 为该等价类中按字典序最小的组合对应结果。
	Representative *Combo
}

// UndecidableColumn 标记一列在全部可行组合中存在多种无法由关系区分的解释。
type UndecidableColumn struct {
	Column int
	// CandidateIndexes 为该列在可行组合中实际出现过的候选下标集合，升序。
	CandidateIndexes []int
	// ValueVaries 为 true 表示不同候选会改变规范值（真正的不可判定）；
	// false 表示候选虽不同，但在当前数据上规范值完全一致。
	ValueVaries bool
}

// Witness 是删除冲突集中某一条关系后的可行解释。
type Witness struct {
	// RemovedRelation 为被删除的关系 ID。
	RemovedRelation string
	// ConflictOnly 为 true 表示该解释仅满足“除被删关系外的其余冲突集关系”
	// （冲突集之外的关系可能不满足）；false 表示它同时满足全部其余关系，
	// 即全局可行的松弛解释。
	ConflictOnly bool
	// Combo 为对应的可行格式组合。
	Combo *Combo
}

// Conflict 是一个冲突关系集（最小不可满足核心，MUS）。
type Conflict struct {
	// RelationIDs 按字典序排列的冲突关系 ID。
	RelationIDs []string
	// Witnesses 与 RelationIDs 一一对应，给出删除该关系后的可行解释。
	Witnesses []Witness
}

// Report 是 Solve 的返回结果。
type Report struct {
	Status Status
	Rows   int
	Cols   int
	// BudgetUsed 已消耗的检查次数；BudgetTotal <=0 时为 0。
	BudgetUsed  int64
	BudgetTotal int64

	// StatusOK / StatusIncomplete 时填充。
	Feasible    []Combo
	Classes     []EquivalenceClass
	Undecidable []UndecidableColumn

	// StatusInfeasible 时填充；StatusIncomplete 且一个可行解都未找到时也可能为空。
	Conflicts []Conflict

	// UnparseableColumns 在某列所有候选都无法解析其非缺失单元时填充，
	// 此时不产生关系冲突集。
	UnparseableColumns []int

	// IncompleteReason 在 StatusIncomplete 时简述中断发生在枚举阶段还是冲突分析阶段。
	IncompleteReason string
}
