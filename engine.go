package taskpkg

import "errors"

// ReasonUncovered 是绕过报告使用的固定原因：后端执行入口依赖区间存在未审核字节。
const ReasonUncovered = "backend execution dependency not fully covered by audited bytes"

// SideEvidence 是一侧解析器在某份样本上的区间证据。
type SideEvidence struct {
	// Fields 是该侧回报的全部字段出现（含被丢弃的重复项），已做防御性拷贝。
	Fields []FieldOccurrence
	// Entries 是该侧实际使用的入口及其重复项取舍。
	Entries []Entry
	// RawAudited 是回调给出的原始审核区间（仅网关侧使用）。
	RawAudited []Interval
	// AuditCover 是独立展开/归并后的审核覆盖区间。
	AuditCover []Interval
	// Dependency 是独立展开/归并后的入口依赖区间。
	Dependency []Interval
	// Rejected 表示该侧是否按 fail-closed 拒绝样本。
	Rejected bool
}

// Verdict 是单份样本的评估结论。
type Verdict struct {
	// Bypassed 仅在后端存在执行入口、网关未拒绝且覆盖存在缺口时为 true。
	Bypassed bool
	// Rejected 为 true 表示至少一侧 fail-closed 拒绝样本，不构成绕过。
	Rejected bool
	// Reason 在 Bypassed 为 true 时等于 ReasonUncovered，否则为空。
	Reason string
	// Uncovered 是后端依赖区间中未被网关审核覆盖的残余区间。
	Uncovered []Interval
	// Gateway 与 Backend 是两侧各自独立的区间证据。
	Gateway SideEvidence
	Backend SideEvidence
}

// Evaluate 对同一份请求字节独立运行网关与后端解析器并给出绕过判定。
//
// 两侧区间各自独立展开，互不共享缓存。任一回调返回 error 会原样上抛；任一侧
// Rejected 时判定为拒绝而非绕过；后端没有执行入口时也不构成绕过。只有后端执行
// 入口依赖区间没有被网关审核区间完整覆盖时才置 Bypassed，字段排序差异不影响判定。
func Evaluate(body []byte, gateway, backend Parser) (*Verdict, error) {
	gwReport, err := gateway(body)
	if err != nil {
		return nil, &ParserError{Side: "gateway", Err: err}
	}
	beReport, err := backend(body)
	if err != nil {
		return nil, &ParserError{Side: "backend", Err: err}
	}
	if err := validateReport(body, gwReport, "gateway"); err != nil {
		return nil, err
	}
	if err := validateReport(body, beReport, "backend"); err != nil {
		return nil, err
	}

	gwCover, err := ExpandIntervals(gwReport.Audited)
	if err != nil {
		return nil, err
	}
	beDeps := collectDependencies(beReport)
	beDepM, err := ExpandIntervals(beDeps)
	if err != nil {
		return nil, err
	}

	v := &Verdict{
		Rejected: gwReport.Rejected || beReport.Rejected,
		Gateway: SideEvidence{
			Fields:     copyFields(gwReport.Fields),
			Entries:    copyEntries(gwReport.Entries),
			RawAudited: copyIntervals(gwReport.Audited),
			AuditCover: gwCover,
			Dependency: mustExpand(collectDependencies(gwReport)),
			Rejected:   gwReport.Rejected,
		},
		Backend: SideEvidence{
			Fields:     copyFields(beReport.Fields),
			Entries:    copyEntries(beReport.Entries),
			RawAudited: copyIntervals(beReport.Audited),
			AuditCover: mustExpand(beReport.Audited),
			Dependency: beDepM,
			Rejected:   beReport.Rejected,
		},
	}

	if v.Rejected || len(beDepM) == 0 {
		return v, nil
	}
	gaps, err := CoverGap(gwCover, beDepM)
	if err != nil {
		return nil, err
	}
	if len(gaps) > 0 {
		v.Bypassed = true
		v.Reason = ReasonUncovered
		v.Uncovered = gaps
	}
	return v, nil
}

// ParserError 包装某一侧解析回调返回的错误。
type ParserError struct {
	Side string
	Err  error
}

func (e *ParserError) Error() string {
	return "taskpkg: " + e.Side + " parser error: " + e.Err.Error()
}

func (e *ParserError) Unwrap() error { return e.Err }

// BypassReport 是一次最少变更搜索命中的绕过样本及双方区间证据。
type BypassReport struct {
	// Picks 是命中样本使用的候选索引（升序）。
	Picks []int
	// MinimumChanges 是命中所需的最少候选数量。
	MinimumChanges int
	// Notes 是命中候选的 Note，按 Picks 顺序给出。
	Notes []string
	// Mutated 是应用变异后的完整请求字节（独立分配，可直接回放）。
	Mutated []byte
	// Uncovered 是未被审核覆盖的后端依赖残余区间（相对 Mutated）。
	Uncovered []Interval
	// Verdict 是该样本的完整评估结论与双方区间证据。
	Verdict *Verdict
}

// Searcher 在给定基线请求与候选集上搜索最少变更的解析分歧绕过样本。
type Searcher struct {
	// Base 是未变异的原始请求字节。
	Base []byte
	// Candidates 至多 12 个重复字段插入或分隔符替换候选。
	Candidates []Candidate
	// Gateway 与 Backend 是两侧解析回调。
	Gateway Parser
	Backend Parser
}

// ErrUnsafeBaseline 表示未施加任何变异的基线样本本身就存在覆盖缺口，
// 此时搜索无意义，宿主应先修正两侧建模或基线请求。
var ErrUnsafeBaseline = errors.New("taskpkg: baseline sample already bypasses; fix the model before searching")

// Search 先用安全样本（零变异基线）验证不绕过，再按变更数量从小到大枚举候选子集，
// 返回首个命中的最少变更绕过样本。
//
// 同一变更数量下按候选索引字典序返回首个命中，保证结果确定。没有任何子集绕过时
// 返回 (nil, nil)。
func (s Searcher) Search() (*BypassReport, error) {
	if len(s.Candidates) > 12 {
		return nil, ErrTooManyCandidates
	}
	if err := ValidateCandidates(s.Base, s.Candidates); err != nil {
		return nil, err
	}

	baseline, err := Evaluate(s.Base, s.Gateway, s.Backend)
	if err != nil {
		return nil, err
	}
	if baseline.Bypassed {
		return nil, ErrUnsafeBaseline
	}

	n := len(s.Candidates)
	for size := 1; size <= n; size++ {
		var hit *BypassReport
		var hitErr error
		enumerateCombinations(n, size, func(picks []int) bool {
			mutated, err := ApplyCandidates(s.Base, s.Candidates, picks)
			if err != nil {
				hitErr = err
				return false
			}
			verdict, err := Evaluate(mutated, s.Gateway, s.Backend)
			if err != nil {
				hitErr = err
				return false
			}
			if verdict.Bypassed {
				notes := make([]string, len(picks))
				for i, idx := range picks {
					notes[i] = s.Candidates[idx].Note
				}
				hit = &BypassReport{
					Picks:          append([]int(nil), picks...),
					MinimumChanges: size,
					Notes:          notes,
					Mutated:        mutated,
					Uncovered:      copyIntervals(verdict.Uncovered),
					Verdict:        verdict,
				}
				return false
			}
			return true
		})
		if hitErr != nil {
			return nil, hitErr
		}
		if hit != nil {
			return hit, nil
		}
	}
	return nil, nil
}

// enumerateCombinations 按字典序枚举 C(n, size) 个索引组合；cb 返回 false 时提前停止。
func enumerateCombinations(n, size int, cb func(picks []int) bool) {
	if size <= 0 || size > n {
		return
	}
	picks := make([]int, size)
	for i := range picks {
		picks[i] = i
	}
	for {
		if !cb(picks) {
			return
		}
		// 找到最右可递增位置。
		i := size - 1
		for i >= 0 && picks[i] == n-size+i {
			i--
		}
		if i < 0 {
			return
		}
		picks[i]++
		for j := i + 1; j < size; j++ {
			picks[j] = picks[j-1] + 1
		}
	}
}

func collectDependencies(r *Report) []Interval {
	var out []Interval
	for _, entry := range r.Entries {
		out = append(out, entry.DependOn...)
	}
	return out
}

func validateReport(body []byte, r *Report, side string) error {
	if r == nil {
		return &ParserError{Side: side, Err: errors.New("nil report")}
	}
	check := func(iv Interval) error {
		if iv.Start < 0 || iv.End < iv.Start || iv.End > len(body) {
			return &ParserError{Side: side, Err: &IntervalError{Bad: iv}}
		}
		return nil
	}
	for _, f := range r.Fields {
		if err := check(f.Raw); err != nil {
			return err
		}
	}
	for _, iv := range r.Audited {
		if err := check(iv); err != nil {
			return err
		}
	}
	for _, e := range r.Entries {
		if err := check(e.Chosen.Raw); err != nil {
			return err
		}
		for _, iv := range e.DependOn {
			if err := check(iv); err != nil {
				return err
			}
		}
	}
	return nil
}

func mustExpand(ivs []Interval) []Interval {
	out, err := ExpandIntervals(ivs)
	if err != nil {
		panic(err)
	}
	return out
}

func copyIntervals(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return nil
	}
	out := make([]Interval, len(ivs))
	copy(out, ivs)
	return out
}

func copyFields(in []FieldOccurrence) []FieldOccurrence {
	if len(in) == 0 {
		return nil
	}
	out := make([]FieldOccurrence, len(in))
	copy(out, in)
	return out
}

func copyEntries(in []Entry) []Entry {
	if len(in) == 0 {
		return nil
	}
	out := make([]Entry, len(in))
	for i, e := range in {
		out[i] = e
		out[i].DependOn = copyIntervals(e.DependOn)
	}
	return out
}
