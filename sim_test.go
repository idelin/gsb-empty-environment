package leasesim

import "testing"

// 验收场景：
//
//	tick 100  节点 0 发起租约协商
//	tick 105  多数派确认，节点 0 成为主
//	tick 140  注入网络黑洞（节点 0 与外界报文延迟 +30 tick）
//	tick 150  旧主租约到期前自动降级为候选者
//	tick 155  节点 1 由多数派选出新主，并拒绝旧主的过期请求
func newAcceptanceSim(writes map[int]bool) *Sim {
	sim := NewSim(DefaultConfig())
	sim.At(100, func(s *Sim) { s.StartElection(0) })
	sim.At(140, func(s *Sim) { s.SetSpike(Spike{Node: 0, Start: 140, End: 156, Extra: 30}) })
	sim.At(145, func(s *Sim) { writes[145] = s.Write(0) }) // 旧主租约仍有效
	sim.At(152, func(s *Sim) { writes[152] = s.Write(0) }) // 旧主已降级
	sim.At(160, func(s *Sim) { writes[160] = s.Write(1) }) // 新主正常服务
	return sim
}

func findEvent(events []Event, pred func(Event) bool) (Event, bool) {
	for _, e := range events {
		if pred(e) {
			return e, true
		}
	}
	return Event{}, false
}

func maxConcurrentPrimaries(sim *Sim, until int) int {
	maxPrim := 0
	sim.AfterTick = func(s *Sim) {
		count := 0
		for _, n := range s.Nodes() {
			if n.Role == Primary {
				count++
			}
		}
		if count > maxPrim {
			maxPrim = count
		}
	}
	sim.Run(until)
	return maxPrim
}

func TestLeaseNegotiationAcceptance(t *testing.T) {
	writes := map[int]bool{}
	sim := newAcceptanceSim(writes)

	role0At150 := Follower
	sim.AfterTick = func(s *Sim) {
		if s.Tick() == 150 {
			role0At150 = s.Node(0).Role
		}
	}
	maxPrim := 0
	prevHook := sim.AfterTick
	sim.AfterTick = func(s *Sim) {
		prevHook(s)
		count := 0
		for _, n := range s.Nodes() {
			if n.Role == Primary {
				count++
			}
		}
		if count > maxPrim {
			maxPrim = count
		}
	}
	sim.Run(200)

	assertEvent := func(kind string, node, tick int) {
		t.Helper()
		if _, ok := findEvent(sim.Events, func(e Event) bool {
			return e.Kind == kind && e.Node == node && e.Tick == tick
		}); !ok {
			t.Errorf("missing event kind=%q node=%d tick=%d", kind, node, tick)
		}
	}

	// 验收边界：tick 100 发起租约，105 多数派确认。
	assertEvent("lease_request", 0, 100)
	assertEvent("primary", 0, 105)
	// 黑洞注入后：旧主在 150 自动降级为候选者。
	assertEvent("spike", 0, 140)
	assertEvent("demoted", 0, 150)
	if role0At150 != Candidate {
		t.Errorf("tick 150: node 0 role = %s, want Candidate", role0At150)
	}
	// 新主在 155 由多数派选出。
	assertEvent("primary", 1, 155)
	// 新主拒绝旧主的请求（过期投票请求或过期心跳）。
	if e, ok := findEvent(sim.Events, func(e Event) bool {
		return (e.Kind == "vote_rejected" || e.Kind == "hb_rejected") &&
			e.Node == 1 && e.From == 0 && e.Tick > 155
	}); !ok {
		t.Error("new primary never rejected a request from the old primary")
	} else {
		t.Logf("old primary rejected at tick %d: %s", e.Tick, e.Detail)
	}
	// 不变式：任意 tick 至多一个主节点（无双主脑裂）。
	if maxPrim > 1 {
		t.Errorf("split brain: %d concurrent primaries", maxPrim)
	}
	// 写入路径：租约内可写，降级后拒写，新主可写。
	if !writes[145] {
		t.Error("tick 145: write to primary with valid lease should succeed")
	}
	if writes[152] {
		t.Error("tick 152: write to demoted old primary should be rejected")
	}
	if !writes[160] {
		t.Error("tick 160: write to new primary should succeed")
	}
	// 旧主最终通过新主心跳回归为从节点。
	if got := sim.Node(0).Role; got != Follower {
		t.Errorf("final node 0 role = %s, want Follower", got)
	}
	if got := sim.Node(1).Role; got != Primary {
		t.Errorf("final node 1 role = %s, want Primary", got)
	}
}

// 时钟漂移达到上界 ε 时，安全性仍然成立。
func TestBoundedClockDrift(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClockOffset = []int{1, -1, 0, 1, -1} // |offset| <= DriftBound
	sim := NewSim(cfg)
	sim.At(100, func(s *Sim) { s.StartElection(0) })
	sim.At(140, func(s *Sim) { s.SetSpike(Spike{Node: 0, Start: 140, End: 156, Extra: 30}) })

	if maxPrim := maxConcurrentPrimaries(sim, 200); maxPrim > 1 {
		t.Fatalf("split brain under clock drift: %d concurrent primaries", maxPrim)
	}
	if _, ok := findEvent(sim.Events, func(e Event) bool {
		return e.Kind == "demoted" && e.Node == 0
	}); !ok {
		t.Error("old primary never demoted under clock drift")
	}
	if _, ok := findEvent(sim.Events, func(e Event) bool {
		return e.Kind == "primary" && e.Node == 1
	}); !ok {
		t.Error("no new primary elected under clock drift")
	}
}

// 无黑洞时主节点靠多数派心跳应答持续续租，永不降级。
func TestStablePrimaryWithoutBlackhole(t *testing.T) {
	sim := NewSim(DefaultConfig())
	sim.At(100, func(s *Sim) { s.StartElection(0) })
	sim.Run(200)

	if _, ok := findEvent(sim.Events, func(e Event) bool { return e.Kind == "demoted" }); ok {
		t.Error("primary demoted despite healthy network")
	}
	if _, ok := findEvent(sim.Events, func(e Event) bool { return e.Kind == "renewed" }); !ok {
		t.Error("lease was never renewed by majority heartbeat acks")
	}
	if got := sim.Node(0).Role; got != Primary {
		t.Errorf("final node 0 role = %s, want Primary", got)
	}
}
