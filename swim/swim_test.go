package swim_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"swimdsim/sim"
	"swimdsim/swim"
)

const totalSteps = 10000

// harness wires a deterministic simulator, a virtual switch and a set
// of SWIM nodes, and captures every protocol log line.
type harness struct {
	sim   *sim.Simulator
	sw    *sim.Switch
	nodes map[string]*swim.Node
	logs  []string
}

func newHarness(seed int64, loss float64, minDelay, maxDelay uint64, cfg swim.Config, ids ...string) *harness {
	s := sim.New()
	sw := sim.NewSwitch(s, seed, loss, minDelay, maxDelay)
	h := &harness{sim: s, sw: sw, nodes: make(map[string]*swim.Node)}
	for _, id := range ids {
		n := swim.NewNode(s, sw, cfg, id, ids)
		n.Logf = func(format string, args ...any) {
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
		}
		h.nodes[id] = n
	}
	return h
}

func (h *harness) run() {
	for i := 0; i < totalSteps; i++ {
		h.sim.Step()
	}
}

// findLog returns the tick of the first log line containing substr.
func findLog(h *harness, substr string) (uint64, bool) {
	for _, l := range h.logs {
		if strings.Contains(l, substr) {
			var tick uint64
			if _, err := fmt.Sscanf(l, "tick=%d", &tick); err == nil {
				return tick, true
			}
		}
	}
	return 0, false
}

// TestMutualDeathRaceDeterministic reproduces the extreme race where
// two nodes declare each other dead at the very same tick, then heals
// the partition and verifies that the incarnation-based arbitration
// resolves the conflict deterministically. The whole scenario runs
// for exactly 10000 discrete clock steps and is executed twice with
// the same seed to prove bit-for-bit reproducibility.
func TestMutualDeathRaceDeterministic(t *testing.T) {
	run := func() *harness {
		cfg := swim.DefaultConfig()
		cfg.AckTimeout = 8
		cfg.IndirectTimeout = 8
		h := newHarness(42, 0, 1, 3, cfg, "A", "B")
		h.sim.After(100, func() { h.sw.Partition("A", "B") })
		h.sim.After(400, func() { h.sw.Heal("A", "B") })
		h.run()
		return h
	}

	h1 := run()
	h2 := run()

	if h1.sim.Now() != totalSteps {
		t.Fatalf("clock = %d, want exactly %d steps", h1.sim.Now(), totalSteps)
	}

	// 100% reproducibility: identical seeds must produce identical logs.
	if !slices.Equal(h1.logs, h2.logs) {
		t.Fatalf("non-deterministic run: %d vs %d log lines", len(h1.logs), len(h2.logs))
	}

	// The race: A and B declare each other dead at the same tick.
	tickA, okA := findLog(h1, "node=A declare-dead B")
	tickB, okB := findLog(h1, "node=B declare-dead A")
	if !okA || !okB {
		t.Fatalf("mutual death declaration not reproduced (A:%v B:%v)", okA, okB)
	}
	if tickA != tickB {
		t.Fatalf("death declarations not simultaneous: A@%d B@%d", tickA, tickB)
	}
	if tickA <= 100 || tickA >= 400 {
		t.Fatalf("death declaration at tick %d, want inside partition window (100,400)", tickA)
	}
	t.Logf("race reproduced: A and B declared each other dead at tick %d", tickA)

	// Both nodes must refute the death rumor about themselves.
	if _, ok := findLog(h1, "node=A refute"); !ok {
		t.Fatal("node A never refuted the rumor about itself")
	}
	if _, ok := findLog(h1, "node=B refute"); !ok {
		t.Fatal("node B never refuted the rumor about itself")
	}

	// Arbitration converges deterministically: both nodes alive again,
	// incarnation bumped exactly once, views fully consistent.
	want := swim.Member{ID: "", State: swim.Alive, Incarnation: 1}
	for id, n := range h1.nodes {
		self := n.View(id)
		want.ID = id
		if self != want {
			t.Fatalf("node %s self view = %+v, want %+v", id, self, want)
		}
	}
	if got := h1.nodes["A"].View("B"); got != (swim.Member{ID: "B", State: swim.Alive, Incarnation: 1}) {
		t.Fatalf("A's view of B = %+v, want ALIVE inc=1", got)
	}
	if got := h1.nodes["B"].View("A"); got != (swim.Member{ID: "A", State: swim.Alive, Incarnation: 1}) {
		t.Fatalf("B's view of A = %+v, want ALIVE inc=1", got)
	}

	// Second run must land on the exact same final state.
	for id, n := range h2.nodes {
		if n.Members()[0] != h1.nodes[id].Members()[0] || !slices.Equal(n.Members(), h1.nodes[id].Members()) {
			t.Fatalf("run 2 diverged for node %s", id)
		}
	}

	if h1.sw.Dropped == 0 {
		t.Fatal("expected packet drops during the partition")
	}
	t.Logf("switch stats: sent=%d dropped=%d delivered=%d", h1.sw.Sent, h1.sw.Dropped, h1.sw.Delivered)
}

// TestConvergenceUnderLossAndJitter runs a 4-node cluster for 10000
// steps with 15% packet loss and 1-5 ticks of latency jitter, and
// verifies that the failure detector produces no false death
// declarations and that the run is fully reproducible.
func TestConvergenceUnderLossAndJitter(t *testing.T) {
	run := func() *harness {
		cfg := swim.DefaultConfig()
		cfg.AckTimeout = 12
		cfg.IndirectTimeout = 12
		cfg.SuspectTimeout = 400
		h := newHarness(7, 0.15, 1, 5, cfg, "A", "B", "C", "D")
		h.run()
		return h
	}

	h1 := run()
	h2 := run()

	if !slices.Equal(h1.logs, h2.logs) {
		t.Fatal("non-deterministic run under loss/jitter")
	}
	for _, l := range h1.logs {
		if strings.Contains(l, "declare-dead") {
			t.Fatalf("false positive death declaration: %s", l)
		}
	}
	for id, n := range h1.nodes {
		for _, m := range n.Members() {
			if m.State == swim.Dead {
				t.Fatalf("node %s sees %s as DEAD: %+v", id, m.ID, m)
			}
		}
	}
	t.Logf("no false positives over %d steps; switch: sent=%d dropped=%d delivered=%d",
		totalSteps, h1.sw.Sent, h1.sw.Dropped, h1.sw.Delivered)
}
