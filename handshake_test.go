package netsec_test

import (
	"testing"

	netsec "example.org/netsec/q0463"
)

func TestHandshakeWitnessNonStrict(t *testing.T) {
	m := netsec.HandshakeMachine(false)
	w, err := netsec.Explore(m)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !w.Reachable || w.Target != netsec.OverrunTarget() {
		t.Fatalf("expected overrun reachable, got %+v", w)
	}
	if err := netsec.Replay(m, w); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// BFS 最短见证：请求 -> 进入授权（在重传点 r==5）-> 越过窗口边界。
	// 非严格策略 z>7 的最小网格点是 7+ε，因此最后一跳为微小偏移延迟。
	wantDelays := []float64{0, 5, float64(netsec.PolicyWindow) + netsec.Eps}
	if len(w.Steps) != len(wantDelays) {
		t.Fatalf("witness length = %d, want %d (%+v)", len(w.Steps), len(wantDelays), w.Steps)
	}
	for i, want := range wantDelays {
		if diff := w.Steps[i].Delay - want; diff > netsec.Eps || diff < -netsec.Eps {
			t.Fatalf("step %d delay = %v, want %v", i, w.Steps[i].Delay, want)
		}
	}

	// 进入授权时 z 归零，到达 overrun 时授权年龄必须严格越过窗口。
	finalZ := w.Final[netsec.ClockPolicy]
	if finalZ <= float64(netsec.PolicyWindow) {
		t.Fatalf("final policy clock = %v, must exceed %d", finalZ, netsec.PolicyWindow)
	}
}

func TestHandshakeStrictAndReferenceAgree(t *testing.T) {
	for _, strict := range []bool{false, true} {
		m := netsec.HandshakeMachine(strict)

		sym, err := netsec.Explore(m)
		if err != nil {
			t.Fatalf("Explore strict=%v: %v", strict, err)
		}
		ref, err := netsec.ReferenceExplore(m, 0)
		if err != nil {
			t.Fatalf("ReferenceExplore strict=%v: %v", strict, err)
		}
		if ref.Truncated {
			t.Fatalf("reference exploration truncated at %d states", ref.Visited)
		}
		if sym.Reachable != ref.Reachable {
			t.Fatalf("strict=%v verdict mismatch: symbolic=%v reference=%v", strict, sym.Reachable, ref.Reachable)
		}
		if sym.Reachable {
			if err := netsec.Replay(m, sym); err != nil {
				t.Fatalf("symbolic Replay strict=%v: %v", strict, err)
			}
			if err := replayReference(m, ref); err != nil {
				t.Fatalf("reference Replay strict=%v: %v", strict, err)
			}
		}
	}
}

func TestBoundaryProbeStrictVsNonStrict(t *testing.T) {
	m := netsec.HandshakeMachine(false)
	// 在 authorized 位置、r 已到重传点且 z 归零的起点，探针边界 z=7。
	start := []float64{5, 10, 0}
	rep, err := netsec.ProbeBoundaries(m, netsec.LocAuthorized, netsec.EventUseExpired, start, netsec.PolicyWindow)
	if err != nil {
		t.Fatalf("ProbeBoundaries: %v", err)
	}

	holds := func(ps []netsec.BoundaryPoint, label string) bool {
		for _, p := range ps {
			if p.Label == label {
				return p.Hold
			}
		}
		return false
	}

	// 非严格机器的超期守卫是 z > 7：仅外侧 c+eps 成立，边界 c 不成立。
	if holds(rep.NonStrict, "c-eps") || holds(rep.NonStrict, "c") || !holds(rep.NonStrict, "c+eps") {
		t.Fatalf("non-strict boundary probe unexpected: %+v", rep.NonStrict)
	}
	if holds(rep.Strict, "c") || !holds(rep.Strict, "c+eps") {
		t.Fatalf("strict boundary probe unexpected: %+v", rep.Strict)
	}

	// 再对严格策略机器探 use_resource 的窗口边界：z < 7 仅在 c-eps 成立。
	sm := netsec.HandshakeMachine(true)
	srep, err := netsec.ProbeBoundaries(sm, netsec.LocAuthorized, netsec.EventUseGood, start, netsec.PolicyWindow)
	if err != nil {
		t.Fatalf("ProbeBoundaries strict machine: %v", err)
	}
	if !holds(srep.Strict, "c-eps") || holds(srep.Strict, "c") || holds(srep.Strict, "c+eps") {
		t.Fatalf("strict-policy window probe unexpected: %+v", srep.Strict)
	}

	// 严格机器的超期守卫是 z >= 7：边界 c 与外侧 c+eps 成立，内侧 c-eps 不成立。
	erep, err := netsec.ProbeBoundaries(sm, netsec.LocAuthorized, netsec.EventUseExpired, start, netsec.PolicyWindow)
	if err != nil {
		t.Fatalf("ProbeBoundaries strict expired: %v", err)
	}
	if holds(erep.Strict, "c-eps") || !holds(erep.Strict, "c") || !holds(erep.Strict, "c+eps") {
		t.Fatalf("strict-policy expired probe unexpected: %+v", erep.Strict)
	}
}

func replayReference(m *netsec.Machine, rep *netsec.ReferenceReport) error {
	w := &netsec.Witness{
		Reachable: rep.Reachable,
		Target:    rep.Target,
		Steps:     rep.Steps,
		Final:     rep.Final,
	}
	return netsec.Replay(m, w)
}
