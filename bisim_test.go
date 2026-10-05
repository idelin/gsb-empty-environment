package main

import "testing"

// 直接转换与"内部步前后包围的转换"弱互模拟等价：单条边情形。
func TestDirectTransitionVsTauWrapped(t *testing.T) {
	direct := NewLTS(2, 0)
	direct.Add(0, "a", 1)

	wrapped := NewLTS(4, 0)
	wrapped.Add(0, "tau", 2)
	wrapped.Add(2, "a", 3)
	wrapped.Add(3, "tau", 1)

	res := Check(direct, wrapped)
	if !res.Equivalent {
		t.Fatal("direct transition and tau-wrapped transition must be weakly bisimilar")
	}
	assertRelated(t, res, 0, 0)
	assertRelated(t, res, 1, 1)
	assertRelated(t, res, 1, 3)
}

// 多条可见边组成的链，每条边前后都插入内部步，仍应与原系统等价。
func TestWrappedChainEquivalent(t *testing.T) {
	direct := NewLTS(3, 0)
	direct.Add(0, "a", 1)
	direct.Add(1, "b", 2)

	wrapped := NewLTS(7, 0)
	wrapped.Add(0, "tau", 3)
	wrapped.Add(3, "a", 4)
	wrapped.Add(4, "tau", 5)
	wrapped.Add(5, "tau", 1)
	wrapped.Add(1, "b", 6)
	wrapped.Add(6, "tau", 2)

	res := Check(direct, wrapped)
	if !res.Equivalent {
		t.Fatal("chain with tau-wrapped transitions must be weakly bisimilar to the direct chain")
	}
	assertRelated(t, res, 1, 5) // after weak 'a', wrapped side may sit mid-tau-chain
	assertRelated(t, res, 2, 6)
}

// 内部循环不产生可见进展：带 tau 自环的系统与无环系统等价。
func TestTauLoopIsNotVisibleProgress(t *testing.T) {
	loop := NewLTS(2, 0)
	loop.Add(0, "tau", 0)
	loop.Add(0, "a", 1)

	plain := NewLTS(2, 0)
	plain.Add(0, "a", 1)

	if res := Check(loop, plain); !res.Equivalent {
		t.Fatal("tau self-loop must not count as visible progress")
	}
}

// 纯内部循环（发散）与死锁弱等价：可见行为都为空。
func TestDivergenceEqualsDeadlock(t *testing.T) {
	div := NewLTS(1, 0)
	div.Add(0, "tau", 0)
	dead := NewLTS(1, 0)
	if res := Check(div, dead); !res.Equivalent {
		t.Fatal("internal loop alone must be weakly bisimilar to deadlock")
	}
}

// 内部循环两边互相环绕也不影响等价性。
func TestTauCycleBetweenStates(t *testing.T) {
	cyc := NewLTS(3, 0)
	cyc.Add(0, "tau", 1)
	cyc.Add(1, "tau", 0)
	cyc.Add(1, "a", 2)

	plain := NewLTS(2, 0)
	plain.Add(0, "a", 1)

	if res := Check(cyc, plain); !res.Equivalent {
		t.Fatal("tau cycle must collapse under weak bisimulation")
	}
}

// a.b + a.c 与 a.(b+c) 不弱互模拟等价；区分过程须可重放。
func TestDistinguishingProcess(t *testing.T) {
	a := NewLTS(5, 0) // a.b + a.c
	a.Add(0, "a", 1)
	a.Add(1, "b", 2)
	a.Add(0, "a", 3)
	a.Add(3, "c", 4)

	b := NewLTS(4, 0) // a.(b + c)
	b.Add(0, "a", 1)
	b.Add(1, "b", 2)
	b.Add(1, "c", 3)

	res := Check(a, b)
	if res.Equivalent {
		t.Fatal("a.b + a.c and a.(b+c) must not be weakly bisimilar")
	}
	rounds, err := Extract(a, b, res)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("expected 2 rounds, got %d: %+v", len(rounds), rounds)
	}
	if rounds[0].Label != "a" {
		t.Fatalf("first round should be an 'a' move, got %q", rounds[0].Label)
	}
	last := rounds[len(rounds)-1]
	if !last.Stuck {
		t.Fatal("distinguisher must end with the defender stuck")
	}
	if last.Label != "b" && last.Label != "c" {
		t.Fatalf("final visible mismatch should be b or c, got %q", last.Label)
	}
	if err := Replay(a, b, rounds); err != nil {
		t.Fatalf("extracted distinguisher must replay cleanly: %v", err)
	}
}

// 重放器必须拒绝被篡改的区分过程。
func TestReplayRejectsTampering(t *testing.T) {
	a := NewLTS(5, 0)
	a.Add(0, "a", 1)
	a.Add(1, "b", 2)
	a.Add(0, "a", 3)
	a.Add(3, "c", 4)

	b := NewLTS(4, 0)
	b.Add(0, "a", 1)
	b.Add(1, "b", 2)
	b.Add(1, "c", 3)

	res := Check(a, b)
	rounds, err := Extract(a, b, res)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := Replay(a, b, rounds); err != nil {
		t.Fatalf("genuine distinguisher must replay: %v", err)
	}

	// 篡改：声称防守方在可见动作前卡死。
	fake := []Round{{Side: 0, From: 0, To: 1, Label: "a", DefenderFrom: 0, Stuck: true}}
	if err := Replay(a, b, fake); err == nil {
		t.Fatal("replay must reject a false stuck claim")
	}

	// 篡改：防守方路径含不存在的边。
	bad := make([]Round, len(rounds))
	copy(bad, rounds)
	bad[0].DefenderPath = []Step{{From: 0, Label: "a", To: 99}}
	if err := Replay(a, b, bad); err == nil {
		t.Fatal("replay must reject an illegal defender path")
	}
}

// 规模限制：每个系统至多 30 个状态。
func TestStateLimit(t *testing.T) {
	if _, err := Parse("31 0\n0 a 1\n"); err == nil {
		t.Fatal("31 states must be rejected")
	}
	lts, err := Parse("30 0\n0 a 1\n")
	if err != nil {
		t.Fatalf("30 states must be accepted: %v", err)
	}
	if lts.N != 30 || lts.Start != 0 {
		t.Fatalf("bad parse: %+v", lts)
	}
}

// 弱迁移目标集：tau* . a . tau*，且内部环不会导致遗漏或死循环。
func TestWeakTargets(t *testing.T) {
	l := NewLTS(5, 0)
	l.Add(0, "tau", 1)
	l.Add(1, "tau", 0) // 内部环
	l.Add(1, "a", 2)
	l.Add(2, "tau", 3)
	l.Add(3, "tau", 4)
	got := l.WeakTargets(0, "a")
	want := map[int]bool{2: true, 3: true, 4: true}
	if len(got) != len(want) {
		t.Fatalf("WeakTargets = %v, want keys of %v", got, want)
	}
	for _, s := range got {
		if !want[s] {
			t.Fatalf("unexpected weak target %d", s)
		}
	}
}

func assertRelated(t *testing.T, res *Result, s, tt int) {
	t.Helper()
	for _, p := range res.Pairs {
		if p.S == s && p.T == tt {
			return
		}
	}
	t.Fatalf("pair (%d,%d) missing from maximal relation", s, tt)
}
