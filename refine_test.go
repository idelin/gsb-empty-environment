package netsec

import "testing"

// 程序 1：初始取值满足 v0=true，但无谓词时抽象把 true/false 合并，
// 产生伪反例；加入谓词 v0 后证明安全。
func spuriousThenSafeProgram() *Program {
	return &Program{
		Vars:  []string{"x"},
		Entry: 0,
		Init:  []Valuation{1},
		Nodes: []Node{
			{Stmt: Assert{Cond: V(0)}, Next: []int{1}, ErrTarget: true},
			{},
		},
	}
}

// 程序 2：havoc v0 后 assert(v0)，存在具体取值使断言失败，是真实反例。
func realCounterexampleProgram() *Program {
	return &Program{
		Vars:  []string{"x"},
		Entry: 0,
		Nodes: []Node{
			{Stmt: Havoc{N: 0}, Next: []int{1}},
			{Stmt: Assert{Cond: V(0)}, Next: []int{2}, ErrTarget: true},
			{},
		},
	}
}

// 程序 3：交替程序。初始 v0=true,v1=true。节点 1 断言 v1，具体恒成立
// （Init 固定且 v1 未被改写），但无谓词时与 v1=false 合并，先出伪反例；
// 精化加入 v1 后，节点 3 的 havoc(v0) 让 assert(v0) 可真实失败，
// 事件序列为伪反例-精化-真实反例交替。
func alternatingProgram() *Program {
	return &Program{
		Vars:  []string{"x", "y"},
		Entry: 0,
		Init:  []Valuation{0b11},
		Nodes: []Node{
			{Next: []int{1}},
			{Stmt: Assert{Cond: V(1)}, Next: []int{2}, ErrTarget: true},
			{Stmt: Havoc{N: 0}, Next: []int{3}},
			{Stmt: Assert{Cond: V(0)}, Next: []int{4}, ErrTarget: true},
			{},
		},
	}
}

// 程序 4：初始 v0=v1=true，两次赋值保持为真，但无谓词时抽象无法识别；
// 学习器给出区分冲突取值的最小合取谓词后证明安全。
func twoRefinementsProgram() *Program {
	return &Program{
		Vars:  []string{"x", "y"},
		Entry: 0,
		Init:  []Valuation{0b11},
		Nodes: []Node{
			{Stmt: Assign{N: 0, RHS: TRUE}, Next: []int{1}},
			{Stmt: Assign{N: 1, RHS: TRUE}, Next: []int{2}},
			{Stmt: Assert{Cond: AND(V(0), V(1))}, Next: []int{3}, ErrTarget: true},
			{},
		},
	}
}

// 程序 5：带循环的程序。v0 保持为真时循环任意次后断言仍成立。
func loopProgram() *Program {
	return &Program{
		Vars:  []string{"x"},
		Entry: 0,
		Init:  []Valuation{1},
		Nodes: []Node{
			{Stmt: Assert{Cond: V(0)}, Next: []int{1}, ErrTarget: true},
			{Next: []int{0}},
		},
	}
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func containsEvent(seq []EventType, want EventType) bool {
	for _, t := range seq {
		if t == want {
			return true
		}
	}
	return false
}

func TestSpuriousCounterexampleRefinedToSafe(t *testing.T) {
	res := Verify(spuriousThenSafeProgram(), nil, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	seq := eventTypes(res.Events)
	if !containsEvent(seq, EventSpuriousCE) {
		t.Fatalf("events %v contain no spurious counterexample", seq)
	}
	if res.Refinements != 1 {
		t.Fatalf("refinements = %d, want 1", res.Refinements)
	}
	if got := res.Predicates[0].String(); got != "v0" {
		t.Fatalf("learned predicate = %s, want v0", got)
	}
	if res.Enumerations == 0 {
		t.Fatal("expected concrete enumerations to be counted")
	}
}

func TestRealCounterexampleReturnsWitness(t *testing.T) {
	res := Verify(realCounterexampleProgram(), nil, Options{})
	if res.Verdict != Unsafe {
		t.Fatalf("verdict = %s (%s), want unsafe", res.Verdict, res.Reason)
	}
	if len(res.Trace) == 0 {
		t.Fatal("expected concrete witness trace")
	}
	last := res.Trace[len(res.Trace)-1]
	if last.In&1 != 0 {
		t.Fatalf("witness enters failing assert with v0=true: %+v", last)
	}
	seq := eventTypes(res.Events)
	if containsEvent(seq, EventSpuriousCE) {
		t.Fatalf("real counterexample run must not report spurious: %v", seq)
	}
	if res.Refinements != 0 {
		t.Fatalf("refinements = %d, want 0", res.Refinements)
	}
}

func TestAlternatingSpuriousAndRealCounterexamples(t *testing.T) {
	res := Verify(alternatingProgram(), nil, Options{})
	if res.Verdict != Unsafe {
		t.Fatalf("verdict = %s (%s), want unsafe", res.Verdict, res.Reason)
	}
	var spuriousIdx, realIdx int = -1, -1
	for i, ev := range res.Events {
		switch ev.Type {
		case EventSpuriousCE:
			spuriousIdx = i
		case EventRealCE:
			realIdx = i
		}
	}
	if spuriousIdx == -1 || realIdx == -1 {
		t.Fatalf("want both spurious and real counterexample events, got %v", eventTypes(res.Events))
	}
	if !(spuriousIdx < realIdx) {
		t.Fatalf("want spurious CE before real CE, got %v", eventTypes(res.Events))
	}
	if res.Refinements != 1 {
		t.Fatalf("refinements = %d, want 1", res.Refinements)
	}
	if got := res.Predicates[0].String(); got != "v1" {
		t.Fatalf("learned predicate = %s, want v1", got)
	}
}

func TestTwoSuccessiveRefinementsThenSafe(t *testing.T) {
	res := Verify(twoRefinementsProgram(), nil, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	if res.Refinements < 1 {
		t.Fatalf("refinements = %d, want at least 1", res.Refinements)
	}
	spuriousCount := 0
	for _, ev := range res.Events {
		if ev.Type == EventSpuriousCE {
			spuriousCount++
		}
	}
	if spuriousCount != res.Refinements {
		t.Fatalf("spurious events = %d, want one per refinement (%d)", spuriousCount, res.Refinements)
	}
}

func TestBudgetExhaustionReturnsUnknown(t *testing.T) {
	res := Verify(spuriousThenSafeProgram(), nil, Options{
		MaxEnumerations: 1,
		MaxPredicates:   1,
		MaxRefinements:  1,
	})
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %s, want unknown", res.Verdict)
	}
	if res.Reason == "" {
		t.Fatal("expected a budget exhaustion reason")
	}
}

func TestMoreThanTwelveVariablesRejected(t *testing.T) {
	prog := &Program{
		Vars:  make([]string, MaxVariables+1),
		Entry: 0,
		Nodes: []Node{{Stmt: Assert{Cond: V(0)}, Next: []int{1}, ErrTarget: true}, {}},
	}
	res := Verify(prog, nil, Options{})
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %s, want unknown", res.Verdict)
	}
}

func TestTwelveVariablesEnumeratedFully(t *testing.T) {
	vars := make([]string, MaxVariables)
	for i := range vars {
		vars[i] = "v" + string(rune('a'+i))
	}
	prog := &Program{
		Vars:  vars,
		Entry: 0,
		Init:  []Valuation{Valuation(1 << uint(MaxVariables-1))}, // 最高位为真
		Nodes: []Node{
			{Stmt: Assert{Cond: V(MaxVariables - 1)}, Next: []int{1}, ErrTarget: true},
			{},
		},
	}
	res := Verify(prog, nil, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	if res.Enumerations == 0 {
		t.Fatal("expected full concrete enumeration over 12-variable state space")
	}
}

func TestLoopReachesFixpoint(t *testing.T) {
	res := Verify(loopProgram(), nil, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	if res.Refinements != 1 {
		t.Fatalf("refinements = %d, want 1", res.Refinements)
	}
}

func TestInitialPredicateAvoidsRefinement(t *testing.T) {
	res := Verify(spuriousThenSafeProgram(), []Expr{V(0)}, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	if res.Refinements != 0 {
		t.Fatalf("refinements = %d, want 0 with precise initial predicate", res.Refinements)
	}
}

func TestSummariesAreRebuiltAndReencoded(t *testing.T) {
	res := Verify(twoRefinementsProgram(), nil, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %s (%s), want safe", res.Verdict, res.Reason)
	}
	if res.SummariesRebuilt == 0 {
		t.Fatal("expected affected summaries to be rebuilt")
	}
	if res.SummariesReencoded == 0 {
		t.Fatal("expected unaffected summaries to be re-encoded")
	}
}

func TestMinimalSeparatorUsesSingleLiteral(t *testing.T) {
	good := valSet{Valuation(0): {}} // v0=false
	bad := valSet{Valuation(1): {}}  // v0=true
	got := learnMinimalSeparator(good, bad, 1)
	if got == nil {
		t.Fatal("expected a separator")
	}
	if got.String() != "!(v0)" {
		t.Fatalf("separator = %s, want !(v0)", got.String())
	}
}

func TestExpressionEvaluation(t *testing.T) {
	cases := []struct {
		expr Expr
		v    Valuation
		want bool
	}{
		{V(0), 1, true},
		{V(0), 0, false},
		{NOT(V(0)), 0, true},
		{AND(V(0), V(1)), 3, true},
		{AND(V(0), V(1)), 1, false},
		{OR(V(0), V(1)), 1, true},
		{OR(V(0), V(1)), 0, false},
		{TRUE, 0, true},
		{FALSE, 0, false},
	}
	for _, tc := range cases {
		if got := tc.expr.eval(tc.v); got != tc.want {
			t.Errorf("%s eval(%d) = %v, want %v", tc.expr, tc.v, got, tc.want)
		}
	}
}
