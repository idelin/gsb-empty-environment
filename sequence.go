package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MaxSegments 是单次判定允许的最大时间段数量。
const MaxSegments = 12

// DefaultMaxCompletions 限制单次判定穷举的合法补全数量，防止目标域过大时
// 组合爆炸。调用方可通过 DecideOptions 调整该上限。
const DefaultMaxCompletions = 1_000_000

var (
	// ErrEmptyDomain 表示目标域为空。
	ErrEmptyDomain = errors.New("netsec: empty target domain")
	// ErrNoSegments 表示没有提供任何时间段。
	ErrNoSegments = errors.New("netsec: no observation segments")
	// ErrTooManySegments 表示时间段数量超过 MaxSegments。
	ErrTooManySegments = errors.New("netsec: more than MaxSegments segments")
	// ErrNegativeMissBudget 表示遗漏预算为负数。
	ErrNegativeMissBudget = errors.New("netsec: negative miss budget")
	// ErrObservedOutsideDomain 表示某时间段观测到目标域之外的目标。
	ErrObservedOutsideDomain = errors.New("netsec: observed target outside domain")
	// ErrNilAutomaton 表示扫描模式自动机为空。
	ErrNilAutomaton = errors.New("netsec: nil automaton")
	// ErrEmptyStart 表示自动机没有初始状态。
	ErrEmptyStart = errors.New("netsec: automaton has no start state")
	// ErrEmptyAccept 表示自动机没有接受状态。
	ErrEmptyAccept = errors.New("netsec: automaton has no accept state")
	// ErrTargetOutsideDomain 表示自动机迁移引用了目标域之外的目标。
	ErrTargetOutsideDomain = errors.New("netsec: automaton transition targets outside domain")
	// ErrCompletionLimitExceeded 表示合法补全数量超过穷举上限。
	ErrCompletionLimitExceeded = errors.New("netsec: legal completion count exceeds enumeration limit")
)

// Verdict 是对扫描模式是否发生的三值判定。
type Verdict int

const (
	// VerdictUnknown 是未进行判定时的零值。
	VerdictUnknown Verdict = iota
	// VerdictImpossible 表示所有合法补全都不产生该模式。
	VerdictImpossible
	// VerdictPossible 表示部分补全产生模式、部分不产生，证据不足。
	VerdictPossible
	// VerdictCertain 表示所有合法补全都产生该模式。
	VerdictCertain
)

// String 返回判定的中文名称。
func (v Verdict) String() string {
	switch v {
	case VerdictImpossible:
		return "不可能发生"
	case VerdictPossible:
		return "可能发生"
	case VerdictCertain:
		return "必然发生"
	default:
		return "未判定"
	}
}

// Witness 是一份具体的遗漏补全见证。
type Witness struct {
	// Segments[i] 是第 i 个时间段补全后的完整尝试集合（已排序，含观测值）。
	Segments [][]Target
	// Added[i] 是第 i 个时间段补入的遗漏目标（已排序）。
	Added [][]Target
	// TotalMisses 是全序列补入的遗漏目标总数。
	TotalMisses int
	// Accepted 表示该补全是否被扫描模式自动机接受。
	Accepted bool
	// Path 是自动机逐段实际选择的目标，长度等于时间段数量。
	// 拒绝见证中自动机走死后的剩余段为空字符串。
	Path []Target
	// StartState 与 EndState 是见证路径在自动机中的起止状态。
	StartState int
	EndState   int
}

// Evidence 汇总穷举结果与支撑三种结论的补全见证。
type Evidence struct {
	// TotalCompletions 是穷举到的合法遗漏补全总数。
	TotalCompletions int
	// AcceptingCompletions 是其中被自动机接受的补全数量。
	AcceptingCompletions int
	// RejectingCompletions 是其中被自动机拒绝的补全数量。
	RejectingCompletions int
	// Accepting 是一份接受见证（VerdictPossible 时取遗漏最少者）；不可能发生时为空。
	Accepting *Witness
	// Rejecting 是一份拒绝见证（VerdictPossible 时取遗漏最多者）；必然发生时为空。
	Rejecting *Witness
}

// Decision 是序列判定的完整结果。
type Decision struct {
	// Verdict 是三值结论。
	Verdict Verdict
	// Alarm 仅在 Verdict 为 VerdictCertain 时为真；证据不足不输出确定告警。
	Alarm bool
	// Evidence 携带穷举统计与补全见证。
	Evidence Evidence
}

// DecideOptions 控制穷举过程的可选项。
type DecideOptions struct {
	// MaxCompletions 为 0 时使用 DefaultMaxCompletions。
	MaxCompletions int
}

// segmentModel 保存单个时间段归一化后的观测集合与可补入候选集合。
type segmentModel struct {
	observed   []Target
	candidates []Target
}

// Decide 穷举全部合法遗漏补全并给出扫描模式的三值判定。
//
// domain 是有限目标域；observed[i] 是第 i 个时间段已观察到的目标集合；
// maxMisses 是全序列允许补入的遗漏目标总数上限；automaton 是扫描模式
// 自动机。时间段数量不得超过 MaxSegments。
func Decide(domain []Target, observed [][]Target, maxMisses int, automaton *NFA, opts *DecideOptions) (*Decision, error) {
	_, segments, err := prepare(domain, observed, maxMisses, automaton)
	if err != nil {
		return nil, err
	}
	limit := DefaultMaxCompletions
	if opts != nil && opts.MaxCompletions > 0 {
		limit = opts.MaxCompletions
	}

	decision := &Decision{}
	addedSets := make([][]Target, len(segments))

	// 按遗漏数量从小到大枚举，保证接受见证遗漏最少；拒绝见证在枚举中
	// 持续覆盖，最终保留遗漏最多者。
	var enumerate func(segmentIndex, missesLeft int) error
	enumerate = func(segmentIndex, missesLeft int) error {
		if segmentIndex == len(segments) {
			return decision.record(segments, addedSets, automaton, limit)
		}
		segment := segments[segmentIndex]
		maxTake := len(segment.candidates)
		if maxTake > missesLeft {
			maxTake = missesLeft
		}
		for take := 0; take <= maxTake; take++ {
			if err := enumerateCombinations(segment.candidates, take, func(chosen []Target) error {
				addedSets[segmentIndex] = chosen
				nextLeft := missesLeft - take
				return enumerate(segmentIndex+1, nextLeft)
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if err := enumerate(0, maxMisses); err != nil {
		return nil, err
	}

	decision.finalize()
	return decision, nil
}

// record 运行自动机并更新统计与对应见证。
func (d *Decision) record(segments []segmentModel, addedSets [][]Target, automaton *NFA, limit int) error {
	completed := make([][]Target, len(segments))
	added := make([][]Target, len(segments))
	totalMisses := 0
	for index, segment := range segments {
		added[index] = append([]Target(nil), addedSets[index]...)
		totalMisses += len(addedSets[index])
		completed[index] = mergeSorted(segment.observed, addedSets[index])
	}

	result := automaton.evaluate(completed)
	witness := buildWitness(completed, added, totalMisses, result)

	if result.accepted {
		d.Evidence.AcceptingCompletions++
		if d.Evidence.Accepting == nil || witness.TotalMisses < d.Evidence.Accepting.TotalMisses {
			d.Evidence.Accepting = witness
		}
	} else {
		d.Evidence.RejectingCompletions++
		if d.Evidence.Rejecting == nil || witness.TotalMisses > d.Evidence.Rejecting.TotalMisses {
			d.Evidence.Rejecting = witness
		}
	}
	d.Evidence.TotalCompletions++
	if d.Evidence.TotalCompletions > limit {
		return ErrCompletionLimitExceeded
	}
	return nil
}

// finalize 依据穷举统计确定三值结论与告警位。
func (d *Decision) finalize() {
	switch {
	case d.Evidence.AcceptingCompletions == 0:
		d.Verdict = VerdictImpossible
	case d.Evidence.RejectingCompletions == 0:
		d.Verdict = VerdictCertain
		d.Alarm = true
	default:
		d.Verdict = VerdictPossible
	}
}

// prepare 校验输入并把目标域、观测集合归一化为可枚举的内部模型。
func prepare(domain []Target, observed [][]Target, maxMisses int, automaton *NFA) ([]Target, []segmentModel, error) {
	if len(domain) == 0 {
		return nil, nil, ErrEmptyDomain
	}
	if len(observed) == 0 {
		return nil, nil, ErrNoSegments
	}
	if len(observed) > MaxSegments {
		return nil, nil, fmt.Errorf("%w: got %d", ErrTooManySegments, len(observed))
	}
	if maxMisses < 0 {
		return nil, nil, ErrNegativeMissBudget
	}

	normDomain := append([]Target(nil), domain...)
	sort.Strings(normDomain)
	normDomain = compactStrings(normDomain)
	inDomain := make(map[Target]bool, len(normDomain))
	for _, target := range normDomain {
		inDomain[target] = true
	}
	if err := automaton.validate(normDomain); err != nil {
		return nil, nil, err
	}

	segments := make([]segmentModel, len(observed))
	for index, raw := range observed {
		values := append([]Target(nil), raw...)
		sort.Strings(values)
		values = compactStrings(values)
		for _, target := range values {
			if !inDomain[target] {
				return nil, nil, fmt.Errorf("%w: %q", ErrObservedOutsideDomain, target)
			}
		}
		segments[index] = segmentModel{
			observed:   values,
			candidates: subtractSorted(normDomain, values),
		}
	}
	return normDomain, segments, nil
}

// buildWitness 把一次自动机运行结果包装成补全见证。
func buildWitness(completed, added [][]Target, totalMisses int, result runResult) *Witness {
	return &Witness{
		Segments:    completed,
		Added:       added,
		TotalMisses: totalMisses,
		Accepted:    result.accepted,
		Path:        reconstructPath(result),
		StartState:  result.startState,
		EndState:    result.endState,
	}
}

// enumerateCombinations 枚举大小为 take 的全部下标组合，对每组调用 fn。
// fn 返回错误时立即中止并向上传递。
func enumerateCombinations(items []Target, take int, fn func([]Target) error) error {
	if take == 0 {
		return fn([]Target{})
	}
	if take > len(items) {
		return nil
	}
	indices := make([]int, take)
	for index := range indices {
		indices[index] = index
	}
	for {
		chosen := make([]Target, take)
		for index, position := range indices {
			chosen[index] = items[position]
		}
		if err := fn(chosen); err != nil {
			return err
		}

		// 从右向左寻找可递增的下标。
		cursor := take - 1
		for cursor >= 0 && indices[cursor] == len(items)-take+cursor {
			cursor--
		}
		if cursor < 0 {
			return nil
		}
		indices[cursor]++
		for next := cursor + 1; next < take; next++ {
			indices[next] = indices[next-1] + 1
		}
	}
}

// compactStrings 对已排序的字符串切片去重。
func compactStrings(values []Target) []Target {
	if len(values) <= 1 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// mergeSorted 合并两个已排序、各自去重的字符串切片。
func mergeSorted(left, right []Target) []Target {
	merged := make([]Target, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i] < right[j]:
			merged = append(merged, left[i])
			i++
		case left[i] > right[j]:
			merged = append(merged, right[j])
			j++
		default:
			merged = append(merged, left[i])
			i++
			j++
		}
	}
	merged = append(merged, left[i:]...)
	merged = append(merged, right[j:]...)
	return merged
}

// subtractSorted 返回 all 中不在 keep 里的元素，两者均已排序去重。
func subtractSorted(all, keep []Target) []Target {
	out := make([]Target, 0, len(all))
	j := 0
	for _, target := range all {
		for j < len(keep) && keep[j] < target {
			j++
		}
		if j < len(keep) && keep[j] == target {
			continue
		}
		out = append(out, target)
	}
	return out
}
