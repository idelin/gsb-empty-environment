package main

import (
	"fmt"
	"strings"
	"testing"
)

func mustLTS(t *testing.T, name string, states []string, initial string, trs ...Transition) *LTS {
	t.Helper()
	l, err := NewLTS(name, states, initial, trs)
	if err != nil {
		t.Fatalf("构造 %s 失败: %v", name, err)
	}
	return l
}

func tr(from, label, to string) Transition { return Transition{from, label, to} }

// 核心需求：直接转移 s0 -a-> s1 与被内部步前后包围的转移
// t0 -tau-> t1 -a-> t2 -tau-> t3 必须被判定为弱互模拟等价。
func TestDirectTransitionEqualsTauWrapped(t *testing.T) {
	l1 := mustLTS(t, "直接", []string{"s0", "s1"}, "s0",
		tr("s0", "a", "s1"))
	l2 := mustLTS(t, "包围", []string{"t0", "t1", "t2", "t3"}, "t0",
		tr("t0", Tau, "t1"),
		tr("t1", "a", "t2"),
		tr("t2", Tau, "t3"))

	res := WeakBisimulation(l1, l2)
	if !res.Related("s0", "t0") {
		t.Fatalf("直接转移与 tau 包围的转移应弱互模拟等价")
	}
	for _, want := range []Pair{{"s0", "t0"}, {"s0", "t1"}, {"s1", "t2"}, {"s1", "t3"}} {
		if !res.Related(want.P, want.Q) {
			t.Errorf("最大关系应包含 %v", want)
		}
	}
	if res.Related("s0", "t2") || res.Related("s1", "t0") {
		t.Errorf("最大关系不应包含跨阶段的配对")
	}
}

// 内部循环不能被当作可见进展：纯 tau 自环与死锁状态弱互模拟等价；
// 带 tau 环的 a 动作与裸 a 动作弱互模拟等价。
func TestTauLoopIsNotVisibleProgress(t *testing.T) {
	loop := mustLTS(t, "tau环", []string{"s0"}, "s0",
		tr("s0", Tau, "s0"))
	dead := mustLTS(t, "死锁", []string{"t0"}, "t0")
	if !WeakBisimulation(loop, dead).Related("s0", "t0") {
		t.Fatalf("纯 tau 自环与死锁应弱互模拟等价")
	}

	l1 := mustLTS(t, "环+a", []string{"s0", "s1"}, "s0",
		tr("s0", Tau, "s0"),
		tr("s0", "a", "s1"))
	l2 := mustLTS(t, "裸a", []string{"t0", "t1"}, "t0",
		tr("t0", "a", "t1"))
	res := WeakBisimulation(l1, l2)
	if !res.Related("s0", "t0") || !res.Related("s1", "t1") {
		t.Fatalf("带 tau 环的系统应与裸 a 系统弱互模拟等价")
	}
}

// 不等价时给出区分公式，且公式真值方向正确、可重放。
// 系统一: a.b + a.c（先做 a 再选 b/c）；系统二: a.(b+c)（a 之后同时保留 b、c）。
func TestDistinguishingFormulaAndReplay(t *testing.T) {
	l1 := mustLTS(t, "分支早", []string{"s0", "s1", "s2", "s3", "s4"}, "s0",
		tr("s0", "a", "s1"), tr("s1", "b", "s2"),
		tr("s0", "a", "s3"), tr("s3", "c", "s4"))
	l2 := mustLTS(t, "分支晚", []string{"t0", "t1", "t2", "t3"}, "t0",
		tr("t0", "a", "t1"),
		tr("t1", "b", "t2"),
		tr("t1", "c", "t3"))

	res := WeakBisimulation(l1, l2)
	if res.Related("s0", "t0") {
		t.Fatalf("a.b+a.c 与 a.(b+c) 不应弱互模拟等价")
	}

	f, err := res.Distinguish("s0", "t0")
	if err != nil {
		t.Fatalf("应能构造区分公式: %v", err)
	}
	if !f.Eval(l1, "s0") {
		t.Errorf("区分公式 %s 应在 s0 上为真", f)
	}
	if f.Eval(l2, "t0") {
		t.Errorf("区分公式 %s 应在 t0 上为假", f)
	}

	lines := Replay(f, l1, "s0", l2, "t0")
	if len(lines) == 0 {
		t.Fatalf("应输出可重放的区分过程")
	}
	var finished bool
	for _, ln := range lines {
		t.Log(ln)
		if strings.Contains(ln, "区分完成") {
			finished = true
		}
		if strings.Contains(ln, "内部错误") {
			t.Errorf("重放过程出现内部错误: %s", ln)
		}
	}
	if !finished {
		t.Errorf("重放过程应以防守方无法响应告终")
	}
}

// 区分公式在重放中攻守互换时仍然方向正确（tau 动作引发的否定）。
func TestDistinguishWithTauAttack(t *testing.T) {
	// 系统一只能静默；系统二可以先静默再做 a。
	l1 := mustLTS(t, "静默", []string{"s0"}, "s0")
	l2 := mustLTS(t, "静默后a", []string{"t0", "t1"}, "t0",
		tr("t0", Tau, "t1"),
		tr("t1", "a", "t1"))
	res := WeakBisimulation(l1, l2)
	if res.Related("s0", "t0") {
		t.Fatalf("静默与 tau.a 不应弱互模拟等价")
	}
	f, err := res.Distinguish("s0", "t0")
	if err != nil {
		t.Fatalf("应能构造区分公式: %v", err)
	}
	if !f.Eval(l1, "s0") || f.Eval(l2, "t0") {
		t.Errorf("区分公式 %s 真值方向错误", f)
	}
	for _, ln := range Replay(f, l1, "s0", l2, "t0") {
		t.Log(ln)
	}
}

// 规模上限：每系统 30 状态可正常处理；31 状态被拒绝。
func TestStateLimit(t *testing.T) {
	mkChain := func(name string, n int) *LTS {
		states := make([]string, n)
		for i := 0; i < n; i++ {
			states[i] = fmt.Sprintf("%s%d", name, i)
		}
		var trs []Transition
		for i := 0; i+1 < n; i++ {
			trs = append(trs, tr(states[i], "a", states[i+1]))
		}
		return mustLTS(t, name, states, states[0], trs...)
	}
	l1 := mkChain("p", 30)
	l2 := mkChain("q", 30)
	if !WeakBisimulation(l1, l2).Related("p0", "q0") {
		t.Fatalf("两条 30 状态全 a 链应弱互模拟等价")
	}

	states := make([]string, 31)
	for i := range states {
		states[i] = fmt.Sprintf("x%d", i)
	}
	if _, err := NewLTS("超限", states, "x0", nil); err == nil {
		t.Fatalf("31 个状态应被拒绝")
	}
}

// 30 状态规模下，可见动作被 tau 前后包围的系统与直接链等价。
func TestThirtyStatesWrappedChain(t *testing.T) {
	// 系统一：10 个可见 a 的链（11 状态）。
	var s1States []string
	var trs1 []Transition
	for i := 0; i <= 10; i++ {
		s1States = append(s1States, fmt.Sprintf("s%d", i))
		if i > 0 {
			trs1 = append(trs1, tr(fmt.Sprintf("s%d", i-1), "a", fmt.Sprintf("s%d", i)))
		}
	}
	l1 := mustLTS(t, "直接链", s1States, "s0", trs1...)

	// 系统二：前 9 个 a 被 tau 前后包围，最后 1 个 a 带 tau 前缀，
	// 共 1 + 9*3 + 2 = 30 状态。
	var s2States []string
	var trs2 []Transition
	prev := "u0"
	s2States = append(s2States, prev)
	for i := 0; i < 9; i++ {
		mid := fmt.Sprintf("m%d", i)
		post := fmt.Sprintf("n%d", i)
		next := fmt.Sprintf("u%d", i+1)
		s2States = append(s2States, mid, post, next)
		trs2 = append(trs2,
			tr(prev, Tau, mid),
			tr(mid, "a", post),
			tr(post, Tau, next))
		prev = next
	}
	s2States = append(s2States, "m9", "n9")
	trs2 = append(trs2,
		tr(prev, Tau, "m9"),
		tr("m9", "a", "n9"))
	l2 := mustLTS(t, "包围链", s2States, "u0", trs2...)

	res := WeakBisimulation(l1, l2)
	if !res.Related("s0", "u0") {
		t.Fatalf("30 状态 tau 包围链应与直接链弱互模拟等价")
	}
}

// 最大关系本身必须满足弱互模拟的转移匹配条件（抽查全部保留对）。
func TestMaxRelationIsFixedPoint(t *testing.T) {
	l1 := mustLTS(t, "左", []string{"s0", "s1", "s2"}, "s0",
		tr("s0", Tau, "s1"), tr("s1", "a", "s2"), tr("s2", Tau, "s0"))
	l2 := mustLTS(t, "右", []string{"t0", "t1"}, "t0",
		tr("t0", "a", "t1"), tr("t1", Tau, "t0"))
	res := WeakBisimulation(l1, l2)
	inRel := make(map[Pair]bool)
	for _, pr := range res.Relation {
		inRel[pr] = true
	}
	for _, pr := range res.Relation {
		if rem, bad := checkPair(l1, l2, pr, inRel, 0); bad {
			t.Errorf("保留对 %v 仍违反匹配条件: %+v", pr, rem)
		}
	}
}
