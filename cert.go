package netsec

// Interval 为闭区间 [Lo, Hi]；Lo > Hi 表示空区间（ bottom ）。
type Interval struct {
	Lo int64 `json:"lo"`
	Hi int64 `json:"hi"`
}

// IsEmpty 报告区间是否为空。
func (iv Interval) IsEmpty() bool { return iv.Lo > iv.Hi }

// SupersetOf 报告 iv 是否包含 o（区间包含关系，空区间被任何区间包含）。
func (iv Interval) SupersetOf(o Interval) bool {
	if o.IsEmpty() {
		return true
	}
	return !iv.IsEmpty() && iv.Lo <= o.Lo && o.Hi <= iv.Hi
}

// Intersect 返回两区间的交集，可能为空。
func Intersect(a, b Interval) Interval {
	lo := a.Lo
	if b.Lo > lo {
		lo = b.Lo
	}
	hi := a.Hi
	if b.Hi < hi {
		hi = b.Hi
	}
	return Interval{Lo: lo, Hi: hi}
}

// Hull 返回包含两区间的最小区间（分支合流）。
func Hull(a, b Interval) Interval {
	if a.IsEmpty() {
		return b
	}
	if b.IsEmpty() {
		return a
	}
	lo := a.Lo
	if b.Lo < lo {
		lo = b.Lo
	}
	hi := a.Hi
	if b.Hi > hi {
		hi = b.Hi
	}
	return Interval{Lo: lo, Hi: hi}
}

// AddConst 返回区间整体加常数后的结果，溢出时饱和到 int64 边界（保持过近似，保证sound）。
func AddConst(iv Interval, c int64) Interval {
	if iv.IsEmpty() {
		return iv
	}
	return Interval{Lo: addSat(iv.Lo, c), Hi: addSat(iv.Hi, c)}
}

func addSat(v, c int64) int64 {
	r := v + c
	if c > 0 && r < v {
		return maxInt64
	}
	if c < 0 && r > v {
		return minInt64
	}
	return r
}

const (
	maxInt64 = int64(^uint64(0) >> 1)
	minInt64 = -maxInt64 - 1
)

// InstrKind 为原程序指令类别。
type InstrKind int

const (
	// InstrInput 输入指令，值带有声明边界 Bounds。
	InstrInput InstrKind = iota
	// InstrAdd 加常数指令，常量为 Const。
	InstrAdd
	// InstrBranch 分支合流点。
	InstrBranch
)

func (k InstrKind) valid() bool { return k >= InstrInput && k <= InstrBranch }

// Instruction 为原程序中的一条指令，证明步骤必须绑定到它。
type Instruction struct {
	Kind   InstrKind `json:"kind"`
	Const  int64     `json:"const,omitempty"`
	Bounds Interval  `json:"bounds,omitempty"`
}

// Rule 为允许的证明规则，仅四种。
type Rule int

const (
	// RuleContain 区间包含：无前提时从绑定的输入指令引出基础事实；
	// 有一个前提时对先前结论做弱化（结论包含前提即可）。
	RuleContain Rule = iota
	// RuleIntersect 交集：结论包含两前提结论之交。
	RuleIntersect
	// RuleAddConst 加常数：结论包含前提结论平移绑定指令常量后的区间。
	RuleAddConst
	// RuleJoin 分支合流：结论包含两前提结论之并的凸包。
	RuleJoin
)

func (r Rule) valid() bool { return r >= RuleContain && r <= RuleJoin }

// Step 为证书中的一步证明。Refs 为引用的先前步骤编号（必须严格小于本步编号），
// Instr 为本步绑定的原指令编号，Claim 为本步断言的结论区间。
type Step struct {
	Rule  Rule     `json:"rule"`
	Refs  []int    `json:"refs,omitempty"`
	Instr int      `json:"instr"`
	Claim Interval `json:"claim"`
}

// Certificate 为分析器提交的证书：原程序、证明步骤序列与最终结论编号。
type Certificate struct {
	Program []Instruction `json:"program"`
	Steps   []Step        `json:"steps"`
	Goal    int           `json:"goal"`
}
