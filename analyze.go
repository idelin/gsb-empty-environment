package netsec

import "fmt"

// Fault 表示一个永久卡滞故障：节点 Node 的值被固定为 StuckAt
// （false=卡 0，true=卡 1）。故障只可能出现在内部门节点
// （AND/XOR/NOT）上，输入与输出节点不参与故障注入。
type Fault struct {
	Node    int
	StuckAt bool
}

func (f Fault) String() string {
	stuck := 0
	if f.StuckAt {
		stuck = 1
	}
	return fmt.Sprintf("node#%d stuck-at-%d", f.Node, stuck)
}

// Status 是分析结论的三态：只有无遗漏地枚举全部故障后才能
// 报告 None/Found；预算耗尽或前提不满足时报告 Unknown。
type Status int

const (
	StatusNone    Status = iota // 完备枚举：不存在目标故障
	StatusFound                 // 完备枚举：存在目标故障
	StatusUnknown               // 未知：预算耗尽或规模前提被破坏
)

func (s Status) String() string {
	switch s {
	case StatusNone:
		return "none"
	case StatusFound:
		return "found"
	case StatusUnknown:
		return "unknown"
	}
	return "invalid"
}

// Options 控制分析预算。Budget 是可执行的故障仿真次数上限
// （每个候选故障消耗 1），<=0 表示不限。预算耗尽时结论为 Unknown。
type Options struct {
	Budget int64
}

// Finding 是一条逃逸证据：某故障未被任何公开测试发现，但存在
// 输入使安全输出由拒绝 0 翻转为放行 1。NormalStates/FaultyStates
// 是逃逸输入下正常/故障电路的逐节点状态，由独立标量回放产生，
// 并与位并行搜索结果逐项交叉校验。
type Finding struct {
	Fault        Fault
	EscapeInput  uint32 // 使输出 0→1 的最小输入（按数值序）
	NormalStates []bool
	FaultyStates []bool
}

// Report 是 Analyze 的完整结论。Dangerous 列出枚举过程中确认的
// 全部危险故障（含已被公开测试发现的）；Status 为 Unknown 时
// Findings 与 Dangerous 只是已分析部分的结果。
type Report struct {
	Status         Status
	Reason         string // Status 为 Unknown 时的原因
	Findings       []Finding
	Dangerous      []Fault
	FaultsTotal    int // 候选故障总数（2 × 内部节点数）
	FaultsAnalyzed int // 实际完成仿真的故障数
}

// AllFaults 以确定顺序（节点下标升序，先卡 0 后卡 1）列出全部候选故障。
func (c *Circuit) AllFaults() []Fault {
	var fs []Fault
	for i, nd := range c.nodes {
		switch nd.Kind {
		case KindAnd, KindXor, KindNot:
			fs = append(fs, Fault{Node: i, StuckAt: false}, Fault{Node: i, StuckAt: true})
		}
	}
	return fs
}

// Analyze 枚举全部候选故障，找出同时满足以下两个条件的故障：
//  1. 未被公开测试集发现（每个测试向量下故障输出与正常输出一致）；
//  2. 危险：存在输入使安全输出由 0 翻转为 1。
//
// 搜索用位并行仿真一次覆盖整个输入空间；每个 Finding 的逐节点
// 状态由独立标量回放生成并交叉校验。只有枚举完全部故障才报告
// None/Found，预算耗尽时报告 Unknown。
func Analyze(c *Circuit, tests []uint32, opts Options) (*Report, error) {
	if err := c.validateTests(tests); err != nil {
		return nil, err
	}
	normal := c.simulate(nil)
	normalOut := normal[c.outputIdx]
	faults := c.AllFaults()
	rep := &Report{FaultsTotal: len(faults)}
	for i, f := range faults {
		if opts.Budget > 0 && int64(i) >= opts.Budget {
			rep.Status = StatusUnknown
			rep.Reason = fmt.Sprintf("预算耗尽：已分析 %d/%d 个故障", rep.FaultsAnalyzed, rep.FaultsTotal)
			return rep, nil
		}
		faulty := c.simulate(&f)
		rep.FaultsAnalyzed++
		faultyOut := faulty[c.outputIdx]
		// 逃逸掩码：正常输出 0 且故障输出 1 的输入集合。
		esc := make([]uint64, c.words)
		dangerous := false
		for w := 0; w < c.words; w++ {
			esc[w] = faultyOut[w] &^ normalOut[w]
			if esc[w] != 0 {
				dangerous = true
			}
		}
		if !dangerous {
			continue
		}
		rep.Dangerous = append(rep.Dangerous, f)
		if detectedByTests(normalOut, faultyOut, tests) {
			continue
		}
		escInput, _ := lowestSet(esc)
		fd := Finding{
			Fault:        f,
			EscapeInput:  escInput,
			NormalStates: c.Eval(escInput),
			FaultyStates: c.EvalWithFault(escInput, f),
		}
		if err := c.verifyReplay(normal, faulty, fd); err != nil {
			return nil, err
		}
		rep.Findings = append(rep.Findings, fd)
	}
	if len(rep.Findings) > 0 {
		rep.Status = StatusFound
	} else {
		rep.Status = StatusNone
	}
	return rep, nil
}

// detectedByTests 判断故障输出是否在某个测试向量下与正常输出不同。
func detectedByTests(normalOut, faultyOut []uint64, tests []uint32) bool {
	for _, t := range tests {
		if bitOf(normalOut, t) != bitOf(faultyOut, t) {
			return true
		}
	}
	return false
}

// verifyReplay 交叉校验独立回放结果与位并行真值表逐位一致，
// 并确认逃逸输入确实使输出 0→1。任何不一致都说明实现存在缺陷。
func (c *Circuit) verifyReplay(normal, faulty [][]uint64, fd Finding) error {
	for i := range c.nodes {
		if bitOf(normal[i], fd.EscapeInput) != fd.NormalStates[i] {
			return fmt.Errorf("netsec: 内部不一致：故障 %v 节点 %d 正常状态回放不符", fd.Fault, i)
		}
		if bitOf(faulty[i], fd.EscapeInput) != fd.FaultyStates[i] {
			return fmt.Errorf("netsec: 内部不一致：故障 %v 节点 %d 故障状态回放不符", fd.Fault, i)
		}
	}
	if c.Output(fd.NormalStates) || !c.Output(fd.FaultyStates) {
		return fmt.Errorf("netsec: 内部不一致：故障 %v 在输入 %d 下未复现 0→1", fd.Fault, fd.EscapeInput)
	}
	return nil
}
