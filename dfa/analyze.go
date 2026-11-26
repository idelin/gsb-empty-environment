package dfa

import "sort"

// 默认故障预算与题目一致：K=2。
const DefaultBudgetK = 2

// EquivalenceMismatch 描述有效验签输入下修补前后的可观察行为分歧。
type EquivalenceMismatch struct {
	Input          Input
	OrigUnlocks    int
	PatchedUnlocks int
	OrigEmits      []int
	PatchedEmits   []int
}

// InherentVuln 表示原程序在“零故障 + 验签失败”下就能 unlock。
type InherentVuln struct {
	Input Input
	Trace *Trace
}

// Evaluation 记录单个候选子集的完整评估结果。
type Evaluation struct {
	Candidates     []Candidate
	TotalCost      int
	Patched        *Program
	InstrCount     int
	Secure         bool
	EquivalenceOK  bool
	Mismatch       *EquivalenceMismatch
	CrossCheckOK   bool
	CrossCheckNote string
	AttackWitness  []Fault // 若不安全，保留一条攻击见证供宿主审计
	Rejected       bool
	RejectReason   string
}

// Report 是 Analyze 的完整报告。
type Report struct {
	BudgetK          int
	InherentVuln     *InherentVuln
	Excluded         []ExcludedCandidate
	Evaluations      []Evaluation
	Feasible         *Evaluation
	FeasibleNotFound bool
}

// Analyze 是宿主回归调用的主入口：
//
//  1. 排除无故障原漏洞（零故障、验签失败却 unlock 的程序直接拒绝修补）；
//  2. 排除落在只读 ROM 区间内的候选插入点；
//  3. 按总代价升序枚举候选子集，拼接、链接并检查 32 条指令上限；
//  4. 对每个候选方案，在全部无效验签输入上验证 K<=2 故障轨迹无法 unlock；
//  5. 对全部有效验签输入验证 unlock 次数与完整 emit 序列完全一致；
//  6. 用独立全枚举预言机交叉复核 BFS 检查器的结论。
func Analyze(base *Program, candidates []Candidate, rom []ROMRange, budget int) (*Report, error) {
	if budget < 0 {
		budget = DefaultBudgetK
	}
	rep := &Report{BudgetK: budget}

	// 步骤 1：无故障原漏洞。
	if iv, err := findInherentVuln(base); err != nil {
		return nil, err
	} else if iv != nil {
		rep.InherentVuln = iv
		return rep, nil
	}

	// 步骤 2：ROM 禁区排除。
	usable, excluded := partitionCandidates(candidates, rom)
	rep.Excluded = excluded

	// 步骤 3：按代价升序枚举所有子集（候选数 <=8，最多 256 个）。
	subsets := enumerateSubsets(usable)
	sort.SliceStable(subsets, func(i, j int) bool {
		ci := subsetCost(subsets[i])
		cj := subsetCost(subsets[j])
		if ci != cj {
			return ci < cj
		}
		return subsetKey(subsets[i]) < subsetKey(subsets[j])
	})

	for _, sub := range subsets {
		ev := evaluateSubset(base, sub, budget)
		rep.Evaluations = append(rep.Evaluations, ev)
		if ev.Rejected {
			continue
		}
		if ev.Secure && ev.EquivalenceOK && ev.CrossCheckOK {
			rep.Feasible = &rep.Evaluations[len(rep.Evaluations)-1]
			return rep, nil
		}
	}
	rep.FeasibleNotFound = true
	return rep, nil
}

func findInherentVuln(base *Program) (*InherentVuln, error) {
	for _, in := range AllInputs(base.Width, false) {
		tr, err := Run(base, in)
		if err != nil {
			return nil, err
		}
		if tr.UnlockCount > 0 {
			return &InherentVuln{Input: in, Trace: tr}, nil
		}
	}
	return nil, nil
}

func partitionCandidates(candidates []Candidate, rom []ROMRange) ([]Candidate, []ExcludedCandidate) {
	var usable []Candidate
	var excluded []ExcludedCandidate
	for _, c := range candidates {
		if InROM(c.Index, rom) {
			excluded = append(excluded, ExcludedCandidate{
				Candidate: c,
				Reason:    "插入点位于只读 ROM 区间",
			})
			continue
		}
		if c.Index < 0 || c.Index > MaxInstrs {
			excluded = append(excluded, ExcludedCandidate{
				Candidate: c,
				Reason:    "插入下标越界",
			})
			continue
		}
		usable = append(usable, c)
	}
	return usable, excluded
}

func enumerateSubsets(items []Candidate) [][]Candidate {
	n := len(items)
	total := 1 << n
	out := make([][]Candidate, 0, total)
	for mask := 0; mask < total; mask++ {
		var sub []Candidate
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sub = append(sub, items[i])
			}
		}
		out = append(out, sub)
	}
	return out
}

func subsetCost(sub []Candidate) int {
	total := 0
	for _, c := range sub {
		total += c.Cost
	}
	return total
}

func subsetKey(sub []Candidate) string {
	key := ""
	for _, c := range sub {
		key += c.ID + "|"
	}
	return key
}

func evaluateSubset(base *Program, sub []Candidate, budget int) Evaluation {
	ev := Evaluation{Candidates: append([]Candidate(nil), sub...), TotalCost: subsetCost(sub)}
	patched, err := Splice(base, sub)
	if err != nil {
		ev.Rejected = true
		ev.RejectReason = err.Error()
		return ev
	}
	ev.Patched = patched
	ev.InstrCount = len(patched.Instrs)

	// 安全性：所有无效验签输入上，K<=budget 故障轨迹均无法 unlock。
	secure := true
	var witness []Fault
	for _, in := range AllInputs(base.Width, false) {
		ok, err := SecureAgainstK(patched, in, budget)
		if err != nil {
			ev.Rejected = true
			ev.RejectReason = err.Error()
			return ev
		}
		if !ok {
			secure = false
			att, err := Attack(patched, in, budget)
			if err == nil && att != nil {
				witness = att.Witness
			}
			break
		}
	}
	ev.Secure = secure
	ev.AttackWitness = witness

	// 功能等价：所有有效验签输入上 unlock 次数与 emit 序列完全一致。
	equivOK := true
	for _, in := range AllInputs(base.Width, true) {
		origTr, err := Run(base, in)
		if err != nil {
			ev.Rejected = true
			ev.RejectReason = err.Error()
			return ev
		}
		patchTr, err := Run(patched, in)
		if err != nil {
			ev.Rejected = true
			ev.RejectReason = err.Error()
			return ev
		}
		if !tracesEquivalent(origTr, patchTr) {
			equivOK = false
			ev.Mismatch = &EquivalenceMismatch{
				Input:          in,
				OrigUnlocks:    origTr.UnlockCount,
				PatchedUnlocks: patchTr.UnlockCount,
				OrigEmits:      append([]int(nil), origTr.Emits...),
				PatchedEmits:   append([]int(nil), patchTr.Emits...),
			}
			break
		}
	}
	ev.EquivalenceOK = equivOK

	// 交叉对照：独立全枚举预言机 vs BFS 检查器。
	crossOK := true
	note := ""
	for _, sigValid := range []bool{false, true} {
		for _, in := range AllInputs(base.Width, sigValid) {
			dis, err := CrossCheck(patched, in, budget)
			if err != nil {
				ev.Rejected = true
				ev.RejectReason = err.Error()
				return ev
			}
			if dis != nil {
				crossOK = false
				note = dis.Reason
				break
			}
		}
		if !crossOK {
			break
		}
	}
	ev.CrossCheckOK = crossOK
	ev.CrossCheckNote = note
	return ev
}

func tracesEquivalent(a, b *Trace) bool {
	if a.UnlockCount != b.UnlockCount {
		return false
	}
	if len(a.Emits) != len(b.Emits) {
		return false
	}
	for i := range a.Emits {
		if a.Emits[i] != b.Emits[i] {
			return false
		}
	}
	return true
}
