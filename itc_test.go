package itc

import "testing"

// 验收边界 1: 初始单节点持有完整时间区间 [0, 1]。
func TestSeedHoldsFullInterval(t *testing.T) {
	s := Seed()
	if got := s.ID(); got != "1" {
		t.Fatalf("seed id = %s, want 1 (full interval [0,1])", got)
	}
	if got := s.EventTree(); got != "0" {
		t.Fatalf("seed event = %s, want 0", got)
	}
}

// 验收边界 2: Fork 裂变为两个各持一半的子时钟。
func TestForkSplitsIntervalInHalf(t *testing.T) {
	a, b := Seed().Fork()
	if got := a.ID(); got != "(1, 0)" {
		t.Fatalf("left fork id = %s, want (1, 0)", got)
	}
	if got := b.ID(); got != "(0, 1)" {
		t.Fatalf("right fork id = %s, want (0, 1)", got)
	}
	// 两个子区间合并应还原完整区间。
	joined, err := Join(a, b)
	if err != nil {
		t.Fatalf("join of forked halves failed: %v", err)
	}
	if got := joined.ID(); got != "1" {
		t.Fatalf("joined id = %s, want 1", got)
	}
}

// 验收边界 3: 异地并发更新后 Join，精确保留因果先后。
func TestConcurrentForksThenJoin(t *testing.T) {
	a, b := Seed().Fork()

	// 异地并发演进：A 记录 2 个事件，B 记录 1 个事件。
	a.Event()
	a.Event()
	b.Event()

	// 并发期间两者不可比较（无因果先后）。
	if ord := Compare(a, b); ord != Concurrent {
		t.Fatalf("Compare(a,b) = %v, want Concurrent", ord)
	}
	if a.Leq(b) || b.Leq(a) {
		t.Fatal("concurrent clocks must not be <= each other")
	}

	// Join 合并区间。
	joined := MustJoin(a, b)

	// 合并结果严格晚于两个并发时钟（保留两者的因果历史）。
	if !a.Leq(joined) || !b.Leq(joined) {
		t.Fatal("joined clock must dominate both concurrent clocks")
	}
	if ord := Compare(a, joined); ord != Less {
		t.Fatalf("Compare(a,joined) = %v, want Less", ord)
	}
	if ord := Compare(b, joined); ord != Less {
		t.Fatalf("Compare(b,joined) = %v, want Less", ord)
	}
	// 精确的事件计数：左半区间 2 次，右半区间 1 次。
	if got, want := joined.EventTree(), "(1, 1, 0)"; got != want {
		t.Fatalf("joined event = %s, want %s", got, want)
	}
	// 合并后区间恢复完整。
	if got := joined.ID(); got != "1" {
		t.Fatalf("joined id = %s, want 1", got)
	}
}

// 因果链：fork 前发生的事件，对两个子时钟都是"过去"。
func TestCausalHistoryInheritedByFork(t *testing.T) {
	seed := Seed()
	seed.Event()
	before := seed.Peek()

	a, b := seed.Fork()
	a.Event()

	if !before.Leq(a) || !before.Leq(b) {
		t.Fatal("both forks must inherit the pre-fork causal history")
	}
	// B 自 fork 后没有新事件，其时钟因果上早于 A。
	if Compare(a, b) != Greater {
		t.Fatal("B has no post-fork event, so B must happened-before A")
	}
	// B 也记录事件后，两者转为并发。
	b.Event()
	if Compare(a, b) != Concurrent {
		t.Fatal("post-fork events on both sides must be concurrent")
	}
}

// 消息传递场景：B 收到 A 的状态后，B 的后续事件因果晚于 A 的事件。
func TestMessagePassingEstablishesCausality(t *testing.T) {
	a, b := Seed().Fork()
	a.Event()
	msg := a.Peek() // A 发出消息，附上只读时间戳

	b.Event() // B 收到消息前的本地事件，与 A 的事件并发
	if Compare(msg, b) != Concurrent {
		t.Fatal("before merge, A's message and B's event must be concurrent")
	}

	merged := MustJoin(b, msg) // B 合并收到的时间戳
	merged.Event()             // B 的后续事件
	if Compare(msg, merged) != Less {
		t.Fatal("after merge, B's new event must causally follow A's message")
	}
}

// 动态拓扑：无需预设节点列表，运行中任意裂变/加入。
func TestDynamicTopologyJoinAndLeave(t *testing.T) {
	a, b := Seed().Fork()
	a.Event()

	// 新节点 C 在运行中加入：由 B 再次裂变而来，无需任何全局配置。
	b1, c := b.Fork()
	if got := c.ID(); got != "(0, (0, 1))" {
		t.Fatalf("c id = %s, want (0, (0, 1))", got)
	}
	c.Event()
	c.Event()

	// 三方合并，区间恢复完整，因果全部保留。
	joined := MustJoin(MustJoin(a, b1), c)
	if got := joined.ID(); got != "1" {
		t.Fatalf("joined id = %s, want 1", got)
	}
	for _, s := range []*Stamp{a, b1, c} {
		if !s.Leq(joined) {
			t.Fatalf("joined clock must dominate %v", s)
		}
	}
}

// 节点退出：区间可交还，剩余节点继续工作。
func TestNodeLeaveReturnsInterval(t *testing.T) {
	a, b := Seed().Fork()
	a.Event()

	// B 退出，把区间交还给 A。
	a = MustJoin(a, b)
	if got := a.ID(); got != "1" {
		t.Fatalf("after rejoin id = %s, want 1", got)
	}
	a.Event() // 单节点继续持有完整区间并演进
	// 左半区间累计 2 次事件，右半区间 1 次（合并时继承自 A 的 1 次）。
	if got := a.EventTree(); got != "(1, 1, 0)" {
		t.Fatalf("event = %s, want (1, 1, 0)", got)
	}
}

// Fork/Join 往返：未发生新事件时合并应还原原时钟。
func TestForkJoinRoundTrip(t *testing.T) {
	seed := Seed()
	seed.Event()
	a, b := seed.Fork()
	joined := MustJoin(a, b)
	if !joined.Equal(seed) {
		t.Fatalf("join(fork(s)) = %v, want %v", joined, seed)
	}
}

// Join 的幂等性、交换律、结合律。
func TestJoinAlgebraicProperties(t *testing.T) {
	a, b := Seed().Fork()
	a.Event()
	b.Event()
	b.Event()

	// 幂等：join(x, x) == x（匿名快照上，事件合并幂等）
	if j := MustJoin(a.Peek(), a.Peek()); !j.Equal(a.Peek()) {
		t.Fatalf("join(peek,peek) = %v, want %v", j, a.Peek())
	}
	// 交换律
	ab, ba := MustJoin(a, b), MustJoin(b, a)
	if !ab.Equal(ba) {
		t.Fatalf("join not commutative: %v vs %v", ab, ba)
	}
	// 结合律（三路裂变）
	x, y := Seed().Fork()
	y1, y2 := y.Fork()
	x.Event()
	y1.Event()
	y2.Event()
	l := MustJoin(MustJoin(x, y1), y2)
	r := MustJoin(x, MustJoin(y1, y2))
	if !l.Equal(r) {
		t.Fatalf("join not associative: %v vs %v", l, r)
	}
	// 吸收：合并结果再与旧时钟的匿名快照合并不变
	if j := MustJoin(ab, a.Peek()); !j.Equal(ab) {
		t.Fatalf("join(join(a,b),peek(a)) = %v, want %v", j, ab)
	}
}

// 重叠区间禁止合并。
func TestJoinRejectsOverlappingIntervals(t *testing.T) {
	s := Seed()
	if _, err := Join(s, s); err != ErrOverlappingIntervals {
		t.Fatalf("join(seed,seed) err = %v, want ErrOverlappingIntervals", err)
	}
	a, b := Seed().Fork()
	c, _ := a.Fork()
	if _, err := Join(c, b.Clone()); err != nil {
		t.Fatalf("join of disjoint forks should succeed: %v", err)
	}
	if _, err := Join(a, c); err != ErrOverlappingIntervals {
		t.Fatalf("join of overlapping forks err = %v, want ErrOverlappingIntervals", err)
	}
}

// Peek 生成匿名只读快照：可比较、不可记录事件。
func TestPeekIsAnonymousSnapshot(t *testing.T) {
	s := Seed()
	s.Event()
	p := s.Peek()
	if got := p.ID(); got != "0" {
		t.Fatalf("peek id = %s, want 0 (anonymous)", got)
	}
	if !p.Leq(s) || !s.Leq(p) {
		t.Fatal("peek must carry the same causal history")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Event() on a peek stamp must panic")
		}
	}()
	p.Event()
}

// 深层裂变：区间可递归细分，支持大量节点。
func TestDeepForks(t *testing.T) {
	stamps := []*Stamp{Seed()}
	for i := 0; i < 5; i++ { // 逐次把最后一个时钟再裂变，共 6 个节点
		last := stamps[len(stamps)-1]
		a, b := last.Fork()
		stamps[len(stamps)-1] = a
		stamps = append(stamps, b)
	}
	for i, s := range stamps {
		s.Event()
		if i%2 == 0 {
			s.Event()
		}
	}
	joined := stamps[0]
	for _, s := range stamps[1:] {
		joined = MustJoin(joined, s)
	}
	if got := joined.ID(); got != "1" {
		t.Fatalf("joined id = %s, want 1", got)
	}
	for _, s := range stamps {
		if Compare(s, joined) != Less {
			t.Fatalf("%v must be strictly before joined clock", s)
		}
	}
}
