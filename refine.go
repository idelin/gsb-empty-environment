package netsec

import "fmt"

// Verdict 是三值验证结论。
type Verdict int

const (
	// Unknown 表示预算耗尽或抽象精化未能收敛，结论不可判定。
	Unknown Verdict = iota
	// Safe 表示错误节点在具体语义下不可达。
	Safe
	// Unsafe 表示找到一条具体可行、能触发断言失败的反例。
	Unsafe
)

func (v Verdict) String() string {
	switch v {
	case Safe:
		return "safe"
	case Unsafe:
		return "unsafe"
	default:
		return "unknown"
	}
}

// EventType 标记一次 CEGAR 迭代中发生的事件。
type EventType int

const (
	// EventAbstractCE 一次抽象可达分析找到了通往错误节点的候选反例。
	EventAbstractCE EventType = iota
	// EventSpuriousCE 具体枚举判定该候选反例不可行（伪反例）。
	EventSpuriousCE
	// EventRealCE 具体枚举确认该候选反例可行（真实反例）。
	EventRealCE
	// EventRefinement 已加入新谓词并重建受影响摘要。
	EventRefinement
)

// Event 记录 CEGAR 循环中的一步，便于宿主观察伪反例与真实反例的交替过程。
type Event struct {
	Type       EventType
	Node       int    // 相关控制流节点，-1 表示不适用
	Predicate  Expr   // Refinement 事件中新加入的谓词
	Predicates []Expr // 该轮使用的完整谓词集合快照
	Path       []int  // 抽象反例路径（节点下标序列）
}

// TraceStep 是具体反例上的一步：节点、进入取值与离开取值。
type TraceStep struct {
	Node int
	In   Valuation
	Out  Valuation
}

// Result 是 Verify 的完整输出。
type Result struct {
	Verdict    Verdict
	Reason     string
	Events     []Event
	Trace      []TraceStep
	Predicates []Expr
	// Enumerations 统计被枚举的具体取值数量（含抽象变换与可行性检查）。
	Enumerations int
	// Refinements 统计成功加入谓词的精化次数。
	Refinements int
	// SummariesRebuilt 是精化时被丢弃重算的受影响摘要条数。
	SummariesRebuilt int
	// SummariesReencoded 是精化时在新谓词下无损重编码的摘要条数。
	SummariesReencoded int
}

// Options 控制精化预算；零值字段将采用默认值。
type Options struct {
	// MaxEnumerations 限制具体取值枚举总量；0 表示使用默认预算。
	MaxEnumerations int
	// MaxPredicates 限制谓词集合大小；0 表示使用默认预算。
	MaxPredicates int
	// MaxRefinements 限制精化轮数；0 表示使用默认预算。
	MaxRefinements int
}

const (
	defaultMaxEnumerations = 1 << 20
	defaultMaxPredicates   = 24
	defaultMaxRefinements  = 32
)

// Verify 对有限布尔程序运行 CEGAR 精化验证。
// 候选反例先做抽象可达，再用具体枚举判可行；伪反例触发最小区分谓词
// 学习与受影响摘要重建。任何预算耗尽都返回 Unknown。
func Verify(prog *Program, initialPredicates []Expr, opts Options) *Result {
	res := &Result{Predicates: append([]Expr(nil), initialPredicates...)}
	if err := prog.Validate(); err != nil {
		res.Verdict = Unknown
		res.Reason = err.Error()
		return res
	}
	if opts.MaxEnumerations <= 0 {
		opts.MaxEnumerations = defaultMaxEnumerations
	}
	if opts.MaxPredicates <= 0 {
		opts.MaxPredicates = defaultMaxPredicates
	}
	if opts.MaxRefinements <= 0 {
		opts.MaxRefinements = defaultMaxRefinements
	}

	e := newEngine(prog, initialPredicates, opts)
	if len(e.predicates) > opts.MaxPredicates {
		res.Verdict = Unknown
		res.Reason = fmt.Sprintf("refine: %d initial predicates exceed budget %d",
			len(e.predicates), opts.MaxPredicates)
		e.fillStats(res)
		return res
	}

	for {
		errPred, _, path := e.abstractReachability()
		e.events = append(e.events, Event{
			Type:       EventAbstractCE,
			Node:       errPred,
			Predicates: append([]Expr(nil), e.predicates...),
			Path:       path,
		})
		if errPred == -1 {
			res.Verdict = Safe
			e.fillStats(res)
			return res
		}

		trace, feasible, pivot, reachSet, badSet := e.checkConcrete(path)
		if feasible {
			e.events = append(e.events, Event{Type: EventRealCE, Node: errPred, Path: path})
			res.Verdict = Unsafe
			res.Trace = trace
			e.fillStats(res)
			return res
		}
		e.events = append(e.events, Event{Type: EventSpuriousCE, Node: pivot, Path: path})

		if reason, stop := e.budgetExceeded(opts); stop {
			res.Verdict = Unknown
			res.Reason = reason
			e.fillStats(res)
			return res
		}

		learned := learnMinimalSeparator(reachSet, badSet, e.nvars)
		if learned == nil {
			res.Verdict = Unknown
			res.Reason = "refine: no boolean separator found for conflicting abstract states"
			e.fillStats(res)
			return res
		}
		if e.hasPredicate(learned) {
			res.Verdict = Unknown
			res.Reason = "refine: learned predicate already present, refinement cannot make progress"
			e.fillStats(res)
			return res
		}
		if len(e.predicates)+1 > opts.MaxPredicates {
			res.Verdict = Unknown
			res.Reason = "refine: predicate budget exhausted"
			e.fillStats(res)
			return res
		}

		e.predicates = append(e.predicates, learned)
		e.refines++
		e.rebuildSummaries(pivot)
		e.events = append(e.events, Event{
			Type:       EventRefinement,
			Node:       pivot,
			Predicate:  learned,
			Predicates: append([]Expr(nil), e.predicates...),
		})
	}
}

func (e *engine) hasPredicate(p Expr) bool {
	key := p.String()
	for _, existing := range e.predicates {
		if existing.String() == key {
			return true
		}
	}
	return false
}

func (e *engine) budgetExceeded(opts Options) (string, bool) {
	switch {
	case e.enumUsed > opts.MaxEnumerations:
		return "refine: enumeration budget exhausted", true
	case e.refines >= opts.MaxRefinements:
		return "refine: refinement round budget exhausted", true
	default:
		return "", false
	}
}

func (e *engine) fillStats(res *Result) {
	res.Events = e.events
	res.Predicates = append([]Expr(nil), e.predicates...)
	res.Enumerations = e.enumUsed
	res.Refinements = e.refines
	res.SummariesRebuilt = e.rebuilt
	res.SummariesReencoded = e.reencoded
}
