package main

import "testing"

func reqs(group string, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		ev[i] = Event{Kind: EventRequest, Group: group}
	}
	return ev
}

func cancels(group string, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		ev[i] = Event{Kind: EventCancel, Group: group}
	}
	return ev
}

func mustSim(t *testing.T, total int, reserves map[string]int) *Simulator {
	t.Helper()
	s, err := NewSimulator(total, reserves)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	return s
}

func mustApply(t *testing.T, s *Simulator, events []Event) {
	t.Helper()
	if err := s.Apply(events); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// 保底 + 借出 + 恢复活跃后下一轮收回，且不撤回已发许可。
func TestGuaranteeLendAndReclaim(t *testing.T) {
	// total=5, a 保底 2, b 保底 2, 1 份未保留余量。
	s := mustSim(t, 5, map[string]int{"a": 2, "b": 2})

	// 第 1 轮：a 空闲，b 提交 5 个请求，借到 a 的份额与余量，拿满 5。
	mustApply(t, s, reqs("b", 5))
	grants := s.Allocate()
	if len(grants) != 5 {
		t.Fatalf("round 1: want 5 grants, got %d", len(grants))
	}
	for i, g := range grants {
		if g.Group != "b" || g.Req != i+1 || g.Round != 1 {
			t.Fatalf("round 1: unexpected grant %+v at %d", g, i)
		}
	}
	if got := s.TotalActive(); got != 5 {
		t.Fatalf("round 1: total active = %d, want 5", got)
	}

	// 第 2 轮：a 恢复活跃，但容量被 b 的借额占满；已发许可不撤回，
	// 本轮 a 只能排队，b 持有的 5 个许可保持不变。
	mustApply(t, s, reqs("a", 2))
	if grants := s.Allocate(); len(grants) != 0 {
		t.Fatalf("round 2: want no grants, got %v", grants)
	}
	if got := s.Active("b"); got != 5 {
		t.Fatalf("round 2: b active = %d, want 5 (no revocation)", got)
	}
	if got := s.Pending("a"); got != 2 {
		t.Fatalf("round 2: a pending = %d, want 2", got)
	}

	// 第 3 轮：b 取消 2 个许可，释放的容量优先满足 a 的保底（收回借额）。
	mustApply(t, s, cancels("b", 2))
	grants = s.Allocate()
	if len(grants) != 2 || grants[0].Group != "a" || grants[1].Group != "a" {
		t.Fatalf("round 3: want 2 grants for a, got %v", grants)
	}
	if got := s.Active("a"); got != 2 {
		t.Fatalf("round 3: a active = %d, want 2 (guarantee met)", got)
	}
	if got := s.TotalActive(); got != 5 {
		t.Fatalf("round 3: total active = %d, want 5", got)
	}
}

// 总量：任何一轮结束后，持有许可总数不得超过总容量。
func TestTotalCapacityNeverExceeded(t *testing.T) {
	s := mustSim(t, 4, map[string]int{"a": 1, "b": 1, "c": 1})
	rounds := [][]Event{
		append(append(reqs("a", 3), reqs("b", 3)...), reqs("c", 3)...),
		cancels("b", 1),
		append(reqs("a", 2), reqs("c", 2)...),
		cancels("a", 2),
		reqs("b", 5),
	}
	for i, ev := range rounds {
		mustApply(t, s, ev)
		s.Allocate()
		if got := s.TotalActive(); got > 4 {
			t.Fatalf("round %d: total active %d exceeds capacity 4", i+1, got)
		}
	}
}

// 组内先来先服务：同组许可严格按请求到达顺序发放。
func TestPerGroupFCFS(t *testing.T) {
	s := mustSim(t, 1, map[string]int{"a": 1})
	mustApply(t, s, reqs("a", 3))
	g1 := s.Allocate()
	mustApply(t, s, cancels("a", 1))
	g2 := s.Allocate()
	mustApply(t, s, cancels("a", 1))
	g3 := s.Allocate()

	var seq []int
	for _, gs := range [][]Grant{g1, g2, g3} {
		for _, g := range gs {
			seq = append(seq, g.Req)
		}
	}
	if len(seq) != 3 || seq[0] != 1 || seq[1] != 2 || seq[2] != 3 {
		t.Fatalf("grant order = %v, want [1 2 3]", seq)
	}
}

// 跨组事件交错时，各组各自的请求序列仍然按序满足。
func TestFCFSWithInterleavedGroups(t *testing.T) {
	s := mustSim(t, 2, map[string]int{"a": 1, "b": 1})
	mustApply(t, s, []Event{
		{EventRequest, "a"}, {EventRequest, "b"}, {EventRequest, "a"}, {EventRequest, "b"},
	})
	s.Allocate()
	mustApply(t, s, cancels("a", 1))
	mustApply(t, s, cancels("b", 1))
	s.Allocate()

	last := map[string]int{}
	for _, g := range s.Grants() {
		if g.Req != last[g.Group]+1 {
			t.Fatalf("group %s: granted req %d after req %d, FCFS violated", g.Group, g.Req, last[g.Group])
		}
		last[g.Group] = g.Req
	}
	if len(s.Grants()) != 4 {
		t.Fatalf("want 4 grants total, got %d", len(s.Grants()))
	}
}

// 保底不被借出挤占：忙组不能把空闲组的保底永久拿走。
func TestReserveNotStarvedByBorrower(t *testing.T) {
	s := mustSim(t, 3, map[string]int{"a": 2, "b": 1})
	mustApply(t, s, reqs("b", 3)) // b 借满全部容量
	s.Allocate()
	if got := s.Active("b"); got != 3 {
		t.Fatalf("b active = %d, want 3", got)
	}
	// a 活跃后，b 每释放一个许可，a 优先补到保底。
	mustApply(t, s, reqs("a", 2))
	mustApply(t, s, cancels("b", 1))
	grants := s.Allocate()
	if len(grants) != 1 || grants[0].Group != "a" {
		t.Fatalf("want 1 grant for a, got %v", grants)
	}
	mustApply(t, s, cancels("b", 1))
	grants = s.Allocate()
	if len(grants) != 1 || grants[0].Group != "a" {
		t.Fatalf("want 1 grant for a, got %v", grants)
	}
	if got := s.Active("a"); got != 2 {
		t.Fatalf("a active = %d, want 2 (full reserve reclaimed)", got)
	}
}

// 取消语义：无持有许可时取消队首待处理请求。
func TestCancelDropsPendingWhenIdle(t *testing.T) {
	s := mustSim(t, 1, map[string]int{"a": 1})
	mustApply(t, s, reqs("a", 2))
	mustApply(t, s, cancels("a", 1)) // 无持有许可，丢弃队首请求 1
	grants := s.Allocate()
	if len(grants) != 1 || grants[0].Req != 2 {
		t.Fatalf("want only req 2 granted, got %v", grants)
	}
}

// 参数校验：保底总和不得超过总容量。
func TestReserveSumValidation(t *testing.T) {
	if _, err := NewSimulator(2, map[string]int{"a": 2, "b": 1}); err == nil {
		t.Fatal("want error when sum of reserves exceeds total")
	}
	if _, err := NewSimulator(-1, nil); err == nil {
		t.Fatal("want error for negative total")
	}
	if _, err := NewSimulator(1, map[string]int{"a": -1}); err == nil {
		t.Fatal("want error for negative reserve")
	}
}

// 借出公平性：两个需求组轮转分享空闲容量。
func TestLendingRoundRobin(t *testing.T) {
	s := mustSim(t, 4, map[string]int{"a": 0, "b": 0, "c": 0})
	mustApply(t, s, append(reqs("a", 2), reqs("b", 2)...))
	s.Allocate()
	if got := s.Active("a"); got != 2 {
		t.Fatalf("a active = %d, want 2", got)
	}
	if got := s.Active("b"); got != 2 {
		t.Fatalf("b active = %d, want 2", got)
	}
}
