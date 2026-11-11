package netsec

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// exactWordNFA 构造只接受按顺序出现完整目标词的线性自动机：
// 0 --a1--> 1 --a2--> ... --ak--> k（k 为接受状态）。
func exactWordNFA(word []Target) *NFA {
	nfa := &NFA{Trans: make(map[TransKey][]int)}
	nfa.Start = []int{0}
	for index, target := range word {
		nfa.AddTransition(index, target, index+1)
	}
	nfa.Accept = []int{len(word)}
	return nfa
}

func assertWitness(t *testing.T, witness *Witness, accepted bool, observed [][]Target) {
	t.Helper()
	if witness == nil {
		t.Fatalf("缺少见证（期望 accepted=%v）", accepted)
	}
	if witness.Accepted != accepted {
		t.Fatalf("见证 Accepted=%v，期望 %v", witness.Accepted, accepted)
	}
	if len(witness.Segments) != len(observed) {
		t.Fatalf("见证段数=%d，期望 %d", len(witness.Segments), len(observed))
	}
	for index, segment := range witness.Segments {
		if !slices.IsSorted(segment) {
			t.Fatalf("第 %d 段见证未排序: %v", index, segment)
		}
		for _, target := range observed[index] {
			if !slices.Contains(segment, target) {
				t.Fatalf("第 %d 段见证 %v 缺少观测目标 %q", index, segment, target)
			}
		}
	}
	if len(witness.Path) != len(observed) {
		t.Fatalf("见证路径长度=%d，期望 %d", len(witness.Path), len(observed))
	}
	for index, target := range witness.Path {
		if target == "" {
			// 拒绝见证中自动机走死的段没有可选目标。
			continue
		}
		if !slices.Contains(witness.Segments[index], target) {
			t.Fatalf("第 %d 段路径目标 %q 不在补全集合 %v 中", index, target, witness.Segments[index])
		}
	}
	misses := 0
	for _, added := range witness.Added {
		misses += len(added)
	}
	if misses != witness.TotalMisses {
		t.Fatalf("见证遗漏统计=%d，实际 %d", witness.TotalMisses, misses)
	}
}

func TestDecideCertain(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"a"}, {"b"}, {"c"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	decision, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if decision.Verdict != VerdictCertain {
		t.Fatalf("判定=%s，期望 必然发生", decision.Verdict)
	}
	if !decision.Alarm {
		t.Fatal("必然发生时必须输出确定告警")
	}
	if decision.Evidence.Rejecting != nil {
		t.Fatal("必然发生时不应存在拒绝见证")
	}
	// 每段候选 3 个（域 4 个、观测 1 个）；预算 1：1 + 3*C(3,1) = 10。
	if decision.Evidence.TotalCompletions != 10 {
		t.Fatalf("补全总数=%d，期望 10", decision.Evidence.TotalCompletions)
	}
	if decision.Evidence.AcceptingCompletions != 10 || decision.Evidence.RejectingCompletions != 0 {
		t.Fatalf("接受/拒绝计数=%d/%d，期望 10/0",
			decision.Evidence.AcceptingCompletions, decision.Evidence.RejectingCompletions)
	}
	assertWitness(t, decision.Evidence.Accepting, true, observed)
}

func TestEnumerationCountsAndBudget(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"a"}, {"b"}, {"c"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	cases := []struct {
		budget int
		want   int
	}{
		{0, 1},
		{1, 10},  // 1 + 3*C(3,1)
		{2, 46},  // 10 + C(3,1)*C(3,2)*3 段对... 即 10 + 36
		{3, 130}, // 1 + 9 + 36 + 84
		{9, 512}, // 预算超过可补总数 => 每段 2^3 种选择，共 8^3
	}
	for _, tc := range cases {
		decision, err := Decide(domain, observed, tc.budget, nfa, nil)
		if err != nil {
			t.Fatalf("预算 %d 返回错误: %v", tc.budget, err)
		}
		if decision.Evidence.TotalCompletions != tc.want {
			t.Fatalf("预算 %d 补全总数=%d，期望 %d",
				tc.budget, decision.Evidence.TotalCompletions, tc.want)
		}
	}
}

func TestWitnessReplayAndMonotonicity(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"a"}, {"b"}, {"d"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	decision, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}

	// 用见证里的完整补全重新跑自动机，结论必须与见证标记一致。
	for _, witness := range []*Witness{decision.Evidence.Accepting, decision.Evidence.Rejecting} {
		if witness == nil {
			continue
		}
		run := nfa.evaluate(witness.Segments)
		if run.accepted != witness.Accepted {
			t.Fatalf("见证复算 accepted=%v，期望 %v", run.accepted, witness.Accepted)
		}
		path := reconstructPath(run)
		if !reflect.DeepEqual(path, witness.Path) {
			t.Fatalf("见证路径复算=%v，期望 %v", path, witness.Path)
		}
	}

	// 存在性语义的单调性：独立重新枚举全部合法补全，一旦某补全被接受，
	// 它的任意逐段超集补全（在预算内）也必须被接受。
	_, segments, err := prepare(domain, observed, 1, nfa)
	if err != nil {
		t.Fatalf("prepare 返回错误: %v", err)
	}
	acceptedSets := make(map[string]bool)
	allCompletions := enumerateForTest(t, segments, 1)
	for _, completion := range allCompletions {
		key := completionKey(completion)
		if nfa.evaluate(completion).accepted {
			acceptedSets[key] = true
		}
	}
	for _, completion := range allCompletions {
		if !acceptedSets[completionKey(completion)] {
			continue
		}
		for _, superset := range allCompletions {
			if !componentwiseSuperset(superset, completion) {
				continue
			}
			if !nfa.evaluate(superset).accepted {
				t.Fatalf("单调性被破坏：接受补全 %v 的超集 %v 被拒绝", completion, superset)
			}
		}
	}
}

// enumerateForTest 重新枚举给定预算下的全部合法补全，用于独立交叉验证。
func enumerateForTest(t *testing.T, segments []segmentModel, budget int) [][][]Target {
	t.Helper()
	var out [][][]Target
	current := make([][]Target, len(segments))
	var visit func(index, left int)
	visit = func(index, left int) {
		if index == len(segments) {
			snapshot := make([][]Target, len(segments))
			for i, segment := range current {
				snapshot[i] = append([]Target(nil), segment...)
			}
			out = append(out, snapshot)
			return
		}
		segment := segments[index]
		maxTake := len(segment.candidates)
		if maxTake > left {
			maxTake = left
		}
		for take := 0; take <= maxTake; take++ {
			err := enumerateCombinations(segment.candidates, take, func(chosen []Target) error {
				current[index] = mergeSorted(segment.observed, chosen)
				visit(index+1, left-take)
				return nil
			})
			if err != nil {
				t.Fatalf("枚举失败: %v", err)
			}
		}
	}
	visit(0, budget)
	return out
}

func completionKey(completion [][]Target) string {
	key := ""
	for _, segment := range completion {
		key += "|" + joinTargets(segment)
	}
	return key
}

func joinTargets(targets []Target) string {
	out := ""
	for i, target := range targets {
		if i > 0 {
			out += ","
		}
		out += target
	}
	return out
}

func componentwiseSuperset(superset, subset [][]Target) bool {
	if len(superset) != len(subset) {
		return false
	}
	for index := range subset {
		for _, target := range subset[index] {
			if !slices.Contains(superset[index], target) {
				return false
			}
		}
	}
	return true
}

func TestTwelveSegmentsAndTwoTargets(t *testing.T) {
	domain := []Target{"x", "y"}
	word := []Target{"x", "y", "x", "y", "x", "y", "x", "y", "x", "y", "x", "y"}
	observed := make([][]Target, MaxSegments)
	for index := range observed {
		observed[index] = []Target{word[index]}
	}
	nfa := exactWordNFA(word)

	// 每段观测已占域内一个目标，候选只有另一个；预算 0 时仅 1 份补全。
	zero, err := Decide(domain, observed, 0, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if zero.Verdict != VerdictCertain {
		t.Fatalf("零遗漏判定=%s，期望 必然发生", zero.Verdict)
	}

	// 预算 1：1 + 12 = 13 份补全；任一单段补入另一目标都不会破坏存在性接受。
	one, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if one.Evidence.TotalCompletions != 13 {
		t.Fatalf("补全总数=%d，期望 13", one.Evidence.TotalCompletions)
	}
	if one.Verdict != VerdictCertain {
		t.Fatalf("预算 1 判定=%s，期望 必然发生", one.Verdict)
	}
}

func TestTooManySegments(t *testing.T) {
	domain := []Target{"a"}
	observed := make([][]Target, MaxSegments+1)
	for index := range observed {
		observed[index] = []Target{"a"}
	}
	nfa := exactWordNFA(make([]Target, MaxSegments+1))

	_, err := Decide(domain, observed, 0, nfa, nil)
	if !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("错误=%v，期望 ErrTooManySegments", err)
	}
}

func TestInputValidation(t *testing.T) {
	domain := []Target{"a", "b"}
	observed := [][]Target{{"a"}}
	nfa := exactWordNFA([]Target{"a"})

	if _, err := Decide(nil, observed, 0, nfa, nil); !errors.Is(err, ErrEmptyDomain) {
		t.Fatalf("空目标域错误=%v", err)
	}
	if _, err := Decide(domain, nil, 0, nfa, nil); !errors.Is(err, ErrNoSegments) {
		t.Fatalf("空时间段错误=%v", err)
	}
	if _, err := Decide(domain, observed, -1, nfa, nil); !errors.Is(err, ErrNegativeMissBudget) {
		t.Fatalf("负预算错误=%v", err)
	}
	if _, err := Decide(domain, [][]Target{{"z"}}, 0, nfa, nil); !errors.Is(err, ErrObservedOutsideDomain) {
		t.Fatalf("越界观测错误=%v", err)
	}

	badTrans := &NFA{
		Start:  []int{0},
		Accept: []int{1},
		Trans:  map[TransKey][]int{{State: 0, Target: "z"}: {1}},
	}
	if _, err := Decide(domain, observed, 0, badTrans, nil); !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("自动机越界迁移错误=%v", err)
	}
	noStart := &NFA{Accept: []int{1}, Trans: map[TransKey][]int{}}
	if _, err := Decide(domain, observed, 0, noStart, nil); !errors.Is(err, ErrEmptyStart) {
		t.Fatalf("空初始状态错误=%v", err)
	}
	noAccept := &NFA{Start: []int{0}, Trans: map[TransKey][]int{}}
	if _, err := Decide(domain, observed, 0, noAccept, nil); !errors.Is(err, ErrEmptyAccept) {
		t.Fatalf("空接受状态错误=%v", err)
	}
}

func TestCompletionLimit(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"a"}, {"b"}, {"c"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	// 预算 9 共 512 份补全；把上限定为 3 时必须报错而不是静默截断。
	_, err := Decide(domain, observed, 9, nfa, &DecideOptions{MaxCompletions: 3})
	if !errors.Is(err, ErrCompletionLimitExceeded) {
		t.Fatalf("错误=%v，期望 ErrCompletionLimitExceeded", err)
	}
}

func TestDedupAndVerdictString(t *testing.T) {
	domain := []Target{"a", "b"}
	observed := [][]Target{{"a", "a", "b"}, {}}
	nfa := exactWordNFA([]Target{"a", "b"})

	decision, err := Decide(domain, observed, 0, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	// 第一段观测已含 a,b；第二段为空且无预算，自动机在第二段无迁移 => 拒绝。
	if decision.Verdict != VerdictImpossible {
		t.Fatalf("判定=%s，期望 不可能发生", decision.Verdict)
	}
	if got := VerdictCertain.String(); got != "必然发生" {
		t.Fatalf("VerdictCertain.String()=%q", got)
	}
	if got := VerdictUnknown.String(); got != "未判定" {
		t.Fatalf("VerdictUnknown.String()=%q", got)
	}
}

func TestDecidePossible(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"a"}, {"b"}, {"d"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	decision, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if decision.Verdict != VerdictPossible {
		t.Fatalf("判定=%s，期望 可能发生", decision.Verdict)
	}
	if decision.Alarm {
		t.Fatal("证据不足时不得输出确定告警")
	}
	if decision.Evidence.TotalCompletions != 10 {
		t.Fatalf("补全总数=%d，期望 10", decision.Evidence.TotalCompletions)
	}
	// 接受补全：第三段补入 c（3 种位置中仅该段有效，预算 1 下恰好 1 种）。
	if decision.Evidence.AcceptingCompletions != 1 {
		t.Fatalf("接受补全数=%d，期望 1", decision.Evidence.AcceptingCompletions)
	}
	if decision.Evidence.RejectingCompletions != 9 {
		t.Fatalf("拒绝补全数=%d，期望 9", decision.Evidence.RejectingCompletions)
	}
	assertWitness(t, decision.Evidence.Accepting, true, observed)
	assertWitness(t, decision.Evidence.Rejecting, false, observed)
	// 接受见证必须通过在第三段补入 c 实现，恰好 1 次遗漏。
	if decision.Evidence.Accepting.TotalMisses != 1 {
		t.Fatalf("接受见证遗漏数=%d，期望 1", decision.Evidence.Accepting.TotalMisses)
	}
	if !reflect.DeepEqual(decision.Evidence.Accepting.Added[2], []Target{"c"}) {
		t.Fatalf("接受见证第三段补入=%v，期望 [c]", decision.Evidence.Accepting.Added[2])
	}
}

func TestDecideImpossible(t *testing.T) {
	domain := []Target{"a", "b", "c", "d"}
	observed := [][]Target{{"d"}, {"d"}, {"d"}}
	nfa := exactWordNFA([]Target{"a", "b", "c"})

	// 零遗漏：观测本身就不含模式所需目标。
	zero, err := Decide(domain, observed, 0, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if zero.Verdict != VerdictImpossible {
		t.Fatalf("零遗漏判定=%s，期望 不可能发生", zero.Verdict)
	}
	if zero.Alarm {
		t.Fatal("不可能发生时不得告警")
	}
	if zero.Evidence.Accepting != nil || zero.Evidence.Rejecting == nil {
		t.Fatal("不可能发生时应只有拒绝见证")
	}

	// 预算 1：每段最多补 1 个目标，仍无法凑齐分布在三个不同段的 a,b,c。
	one, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if one.Verdict != VerdictImpossible {
		t.Fatalf("预算 1 判定=%s，期望 不可能发生", one.Verdict)
	}
	if one.Evidence.TotalCompletions != 10 {
		t.Fatalf("补全总数=%d，期望 10", one.Evidence.TotalCompletions)
	}
	assertWitness(t, one.Evidence.Rejecting, false, observed)
}

func TestDecideBranchingNFA(t *testing.T) {
	// 状态 0 上 a 和 b 都可迁移到接受状态 1，验证非确定性迁移。
	nfa := &NFA{
		Start:  []int{0},
		Accept: []int{1},
		Trans: map[TransKey][]int{
			{State: 0, Target: "a"}: {1},
			{State: 0, Target: "b"}: {1},
		},
	}
	domain := []Target{"a", "b", "c"}
	observed := [][]Target{{"c"}}

	decision, err := Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if decision.Verdict != VerdictPossible {
		t.Fatalf("判定=%s，期望 可能发生", decision.Verdict)
	}
	if decision.Evidence.AcceptingCompletions != 2 || decision.Evidence.RejectingCompletions != 1 {
		t.Fatalf("接受/拒绝计数=%d/%d，期望 2/1",
			decision.Evidence.AcceptingCompletions, decision.Evidence.RejectingCompletions)
	}
	assertWitness(t, decision.Evidence.Accepting, true, observed)
}

func TestWitnessMissSelection(t *testing.T) {
	// 域 3 个目标、两段均只观测 a；模式要求 (a, b)。
	domain := []Target{"a", "b", "c"}
	observed := [][]Target{{"a"}, {"a"}}
	nfa := NewNFA(
		[]int{0},
		[]int{2},
		map[TransKey][]int{
			{State: 0, Target: "a"}: {1},
			{State: 1, Target: "b"}: {2},
		},
	)

	decision, err := Decide(domain, observed, 2, nfa, nil)
	if err != nil {
		t.Fatalf("Decide 返回错误: %v", err)
	}
	if decision.Verdict != VerdictPossible {
		t.Fatalf("判定=%s，期望 可能发生", decision.Verdict)
	}
	// 接受只可能来自第二段补 b：最少遗漏 1。
	if decision.Evidence.Accepting.TotalMisses != 1 {
		t.Fatalf("接受见证遗漏=%d，期望 1", decision.Evidence.Accepting.TotalMisses)
	}
	if !slices.Contains(decision.Evidence.Accepting.Segments[1], "b") {
		t.Fatalf("接受见证第二段=%v，应包含 b", decision.Evidence.Accepting.Segments[1])
	}
	// 拒绝见证取遗漏最多者：两段候选均为 {b,c}，预算 2 下最多补 2 个。
	if decision.Evidence.Rejecting.TotalMisses != 2 {
		t.Fatalf("拒绝见证遗漏=%d，期望 2", decision.Evidence.Rejecting.TotalMisses)
	}
	if got := (VerdictPossible).String(); got != "可能发生" {
		t.Fatalf("VerdictPossible.String()=%q", got)
	}
	if got := (VerdictImpossible).String(); got != "不可能发生" {
		t.Fatalf("VerdictImpossible.String()=%q", got)
	}
}
