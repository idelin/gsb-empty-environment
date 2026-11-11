package netsec

// 本包实现供验证宿主调用的定时状态机：
//   - 至多三只时钟；
//   - 守卫只能是时钟与整数常数（0..10）的比较；
//   - 迁移动作只能把时钟归零；
//   - 位置不变量只允许上界（< 或 <=）。
//
// 符号引擎使用差界定界矩阵（DBM）表示时区，并按区域等价做归一化，
// 因此状态空间有限、可枚举错误状态并重建含具体延迟的见证。

// Cmp 是守卫允许的比较运算。
type Cmp int

const (
	LE Cmp = iota // <=
	LT            // <
	GE            // >=
	GT            // >
	EQ            // ==
)

func (c Cmp) String() string {
	switch c {
	case LE:
		return "<="
	case LT:
		return "<"
	case GE:
		return ">="
	case GT:
		return ">"
	case EQ:
		return "=="
	default:
		return "?"
	}
}

// 包级硬限制：题目要求三只时钟、常数不超过十。
const (
	MaxClocks = 3
	MaxConst  = 10
)

// Guard 是单条时钟守卫：Clock Op Const。
type Guard struct {
	Clock int
	Op    Cmp
	Const int
}

// Action 是迁移动作，目前只支持把指定时钟归零。
type Action struct {
	Clock int
}

// Edge 是一条带守卫与归零动作的离散迁移。
type Edge struct {
	From   int
	Event  string
	Guards []Guard
	Resets []Action
	To     int
}

// Machine 是定时状态机的完整描述。
// Invariants 按位置索引，缺省表示无不变量；只接受 LT/LE 上界。
type Machine struct {
	Name       string
	Clocks     int
	Locations  []string
	Invariants [][]Guard
	Edges      []Edge
	Initial    int
	Errors     []int
}

// Step 是见证中的一步：在 Location 上等待 Delay 后，于 At 估值处触发 Event。
type Step struct {
	Location int
	Event    string
	Delay    float64
	At       []float64
	Guards   []Guard
	Resets   []Action
	To       int
}

// Witness 是错误状态可达性结论与具体延迟见证。
type Witness struct {
	Reachable bool
	Target    int
	Steps     []Step
	Final     []float64
	// Zones 与 Edges 是符号探索统计，便于宿主评估覆盖规模。
	Zones int
	Edges int
}

// Validate 检查机器描述是否满足包级限制与内部一致性。
func (m *Machine) Validate() error {
	if m.Clocks < 1 || m.Clocks > MaxClocks {
		return fmtError("netsec: clocks must be in 1..%d, got %d", MaxClocks, m.Clocks)
	}
	if len(m.Locations) == 0 {
		return fmtError("netsec: machine has no locations")
	}
	if m.Initial < 0 || m.Initial >= len(m.Locations) {
		return fmtError("netsec: initial location %d out of range", m.Initial)
	}
	if len(m.Invariants) > len(m.Locations) {
		return fmtError("netsec: more invariant rows than locations")
	}
	for loc, gs := range m.Invariants {
		for _, g := range gs {
			if err := m.checkGuard(g, true); err != nil {
				return fmtError("netsec: invariant of %s: %w", m.Locations[loc], err)
			}
		}
	}
	seen := make(map[int]bool)
	for _, e := range m.Errors {
		if e < 0 || e >= len(m.Locations) {
			return fmtError("netsec: error location %d out of range", e)
		}
		if seen[e] {
			return fmtError("netsec: duplicate error location %d", e)
		}
		seen[e] = true
	}
	for i, e := range m.Edges {
		if e.Event == "" {
			return fmtError("netsec: edge %d has empty event", i)
		}
		if e.From < 0 || e.From >= len(m.Locations) {
			return fmtError("netsec: edge %d From out of range", i)
		}
		if e.To < 0 || e.To >= len(m.Locations) {
			return fmtError("netsec: edge %d To out of range", i)
		}
		for _, g := range e.Guards {
			if err := m.checkGuard(g, false); err != nil {
				return fmtError("netsec: edge %d (%s): %w", i, e.Event, err)
			}
		}
		for _, a := range e.Resets {
			if a.Clock < 0 || a.Clock >= m.Clocks {
				return fmtError("netsec: edge %d (%s): reset clock %d out of range", i, e.Event, a.Clock)
			}
		}
	}
	return nil
}

func (m *Machine) checkGuard(g Guard, invariant bool) error {
	if g.Clock < 0 || g.Clock >= m.Clocks {
		return fmtError("guard clock %d out of range", g.Clock)
	}
	if g.Const < 0 || g.Const > MaxConst {
		return fmtError("guard constant %d outside 0..%d", g.Const, MaxConst)
	}
	if invariant && g.Op != LT && g.Op != LE {
		return fmtError("invariant only allows < or <=, got %s", g.Op)
	}
	switch g.Op {
	case LE, LT, GE, GT, EQ:
		return nil
	default:
		return fmtError("unknown comparison %d", g.Op)
	}
}

// EvalGuards 在具体估值上判断一组守卫是否全部成立，eps 为浮点容差。
func EvalGuards(guards []Guard, vals []float64, eps float64) bool {
	for _, g := range guards {
		if g.Clock >= len(vals) {
			return false
		}
		x := vals[g.Clock]
		c := float64(g.Const)
		switch g.Op {
		case LE:
			if x > c+eps {
				return false
			}
		case LT:
			if x >= c {
				return false
			}
		case GE:
			if x < c-eps {
				return false
			}
		case GT:
			if x <= c {
				return false
			}
		case EQ:
			if abs(x-c) > eps {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// maxConsts 返回每只时钟在守卫与不变量中出现的最大常数，缺省为 0。
func (m *Machine) maxConsts() []int {
	k := make([]int, m.Clocks)
	note := func(gs []Guard) {
		for _, g := range gs {
			if g.Const > k[g.Clock] {
				k[g.Clock] = g.Const
			}
		}
	}
	for _, gs := range m.Invariants {
		note(gs)
	}
	for _, e := range m.Edges {
		note(e.Guards)
	}
	return k
}
func (m *Machine) invariant(loc int) []Guard {
	if loc < len(m.Invariants) {
		return m.Invariants[loc]
	}
	return nil
}

func (m *Machine) isError(loc int) bool {
	for _, e := range m.Errors {
		if e == loc {
			return true
		}
	}
	return false
}
