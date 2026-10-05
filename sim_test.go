package membership

import (
	"reflect"
	"strings"
	"testing"
)

func baseConfig(seed int64) Config {
	return Config{
		NumNodes:        4,
		ProtocolPeriod:  10,
		DirectTimeout:   4,
		IndirectTimeout: 6,
		SuspectTimeout:  40,
		NumIndirect:     2,
		MaxDelay:        2,
		Seed:            seed,
	}
}

// modelView is an independent, from-spec reimplementation of the merge
// rules. It folds the exact input stream a node observed (updates plus
// restarts) and must produce the same final view as the protocol code.
func modelView(self, numNodes int, inputs []modelInput) map[int]MemberMeta {
	var inc uint64
	view := map[int]MemberMeta{}
	for i := 0; i < numNodes; i++ {
		view[i] = MemberMeta{State: Alive}
	}
	for _, in := range inputs {
		if in.restart {
			inc++
			view = map[int]MemberMeta{}
			for i := 0; i < numNodes; i++ {
				view[i] = MemberMeta{State: Alive}
			}
			view[self] = MemberMeta{State: Alive, Incarnation: inc}
			continue
		}
		u := in.update
		if u.Member == self {
			if u.State != Alive && u.Incarnation >= inc {
				inc = u.Incarnation + 1
				view[self] = MemberMeta{State: Alive, Incarnation: inc}
			}
			continue
		}
		cur := view[u.Member]
		switch {
		case u.Incarnation > cur.Incarnation:
			view[u.Member] = MemberMeta{State: u.State, Incarnation: u.Incarnation}
		case u.Incarnation == cur.Incarnation && u.State > cur.State:
			view[u.Member] = MemberMeta{State: u.State, Incarnation: u.Incarnation}
		}
	}
	return view
}

func assertConvergedAllAlive(t *testing.T, s *Sim) {
	t.Helper()
	ref := s.View(0)
	for i := 1; i < s.Cfg.NumNodes; i++ {
		if got := s.View(i); !reflect.DeepEqual(ref, got) {
			t.Fatalf("views diverged:\nnode 0: %v\nnode %d: %v", ref, i, got)
		}
	}
	for id, m := range ref {
		if m.State != Alive {
			t.Fatalf("member %d not alive after convergence: %v", id, m)
		}
	}
}

func hasTransition(s *Sim, node, member int, to State) bool {
	for _, tr := range s.Transitions(node) {
		if tr.Member == member && tr.To == to {
			return true
		}
	}
	return false
}

// A slow but lossless network makes acks arrive after the direct timeout:
// indirect probes must kick in, but the late ack still prevents suspicion.
func TestLateAckDoesNotSuspect(t *testing.T) {
	cfg := baseConfig(7)
	cfg.NumNodes = 3
	cfg.DirectTimeout = 2
	cfg.IndirectTimeout = 30
	cfg.MaxDelay = 6 // round trip up to 12 ticks: exceeds direct, not indirect
	s := NewSim(cfg, nil)
	s.Run(300)
	if s.Stats.PingReqs == 0 {
		t.Fatal("expected indirect probes to be used")
	}
	for i := 0; i < cfg.NumNodes; i++ {
		for _, tr := range s.Transitions(i) {
			if tr.To == Suspect || tr.To == Dead {
				t.Fatalf("node %d transitioned member %d to %s despite late-but-valid acks: %s",
					i, tr.Member, tr.To, tr.Reason)
			}
		}
	}
}

// While a node is partitioned away it gets suspected; after the cut heals
// it hears the rumor, raises its own incarnation and re-declares alive
// before the suspect timeout condemns it.
func TestRefuteDuringSuspectPeriod(t *testing.T) {
	cfg := baseConfig(3)
	cfg.NumNodes = 3
	cfg.SuspectTimeout = 60
	faults := []Fault{
		{Tick: 20, Kind: FaultPartitionStart, Group: []int{2}},
		{Tick: 50, Kind: FaultPartitionEnd},
	}
	s := NewSim(cfg, faults)
	s.Run(300)

	if !hasTransition(s, 0, 2, Suspect) && !hasTransition(s, 1, 2, Suspect) {
		t.Fatal("expected node 2 to be suspected while partitioned")
	}
	if s.Incarnation(2) == 0 {
		t.Fatal("node 2 never refuted the suspicion with a higher incarnation")
	}
	for i := 0; i < cfg.NumNodes; i++ {
		if hasTransition(s, i, 2, Dead) {
			t.Fatalf("node %d condemned node 2 although it refuted in time", i)
		}
	}
	assertConvergedAllAlive(t, s)
}

// Stale timers must never override a fresher state.
func TestStaleTimersIgnored(t *testing.T) {
	cfg := baseConfig(1)
	cfg.NumNodes = 3
	s := NewSim(cfg, nil)
	a := s.nodes[0]

	// Suspect member 1 at inc 0, then a refutation at inc 1 arrives.
	a.applyUpdate(s, Update{Member: 1, State: Suspect, Incarnation: 0}, "test")
	staleGen := a.suspectGen[1]
	a.applyUpdate(s, Update{Member: 1, State: Alive, Incarnation: 1}, "test")
	a.onSuspectTimeout(s, 1, 0, staleGen)
	if got := a.view[1]; got.State != Alive || got.Incarnation != 1 {
		t.Fatalf("stale suspect timer overrode refuted alive: %v", got)
	}

	// Suspect again at inc 1; the old generation token must not kill.
	a.applyUpdate(s, Update{Member: 1, State: Suspect, Incarnation: 1}, "test")
	a.onSuspectTimeout(s, 1, 1, staleGen)
	if a.view[1].State != Suspect {
		t.Fatalf("stale generation token condemned member: %v", a.view[1])
	}
	// The current token does fire.
	a.onSuspectTimeout(s, 1, 1, a.suspectGen[1])
	if a.view[1].State != Dead {
		t.Fatalf("valid suspect timer did not condemn: %v", a.view[1])
	}

	// Stale probe timers must not advance or clear a fresh probe.
	a.probes[2] = pendingProbe{gen: 9, stage: stageDirect}
	a.onDirectTimeout(s, 2, 8)
	if a.probes[2].stage != stageDirect {
		t.Fatal("stale direct timeout advanced a fresh probe")
	}
	a.onIndirectTimeout(s, 2, 8)
	if _, ok := a.probes[2]; !ok {
		t.Fatal("stale indirect timeout cleared a fresh probe")
	}
}

// Merge ordering: same incarnation escalates dead > suspect > alive; only a
// higher incarnation de-escalates.
func TestMergeRules(t *testing.T) {
	cfg := baseConfig(1)
	s := NewSim(cfg, nil)
	a := s.nodes[0]

	a.applyUpdate(s, Update{Member: 1, State: Suspect, Incarnation: 0}, "test")
	a.applyUpdate(s, Update{Member: 1, State: Alive, Incarnation: 0}, "test")
	if a.view[1].State != Suspect {
		t.Fatal("alive at same incarnation cleared suspect")
	}
	a.applyUpdate(s, Update{Member: 1, State: Dead, Incarnation: 0}, "test")
	if a.view[1].State != Dead {
		t.Fatal("dead did not override suspect at same incarnation")
	}
	a.applyUpdate(s, Update{Member: 1, State: Suspect, Incarnation: 0}, "test")
	a.applyUpdate(s, Update{Member: 1, State: Alive, Incarnation: 0}, "test")
	if a.view[1].State != Dead {
		t.Fatal("de-escalation at same incarnation")
	}
	a.applyUpdate(s, Update{Member: 1, State: Alive, Incarnation: 1}, "test")
	if got := a.view[1]; got.State != Alive || got.Incarnation != 1 {
		t.Fatalf("higher incarnation did not revive: %v", got)
	}
	a.applyUpdate(s, Update{Member: 1, State: Dead, Incarnation: 0}, "test")
	if a.view[1].State != Alive {
		t.Fatal("stale incarnation overrode fresh state")
	}
}

// A long partition splits the cluster; both sides condemn each other. After
// healing, every falsely condemned node refutes and the cluster converges.
func TestPartitionHeal(t *testing.T) {
	cfg := baseConfig(11)
	cfg.NumNodes = 4
	cfg.SuspectTimeout = 25
	faults := []Fault{
		{Tick: 20, Kind: FaultPartitionStart, Group: []int{0, 1}},
		{Tick: 90, Kind: FaultPartitionEnd},
	}
	s := NewSim(cfg, faults)
	s.Run(400)

	if !hasTransition(s, 0, 2, Dead) && !hasTransition(s, 1, 3, Dead) {
		t.Fatal("expected cross-partition condemnations during the cut")
	}
	for i := 0; i < cfg.NumNodes; i++ {
		if s.Incarnation(i) == 0 {
			t.Fatalf("node %d never refuted its false condemnation", i)
		}
	}
	assertConvergedAllAlive(t, s)
}

// With ongoing loss the cluster may disagree, but once loss stops the
// periodic full-view gossip plus refutations must converge it.
func TestConvergesAfterLossStops(t *testing.T) {
	for _, seed := range []int64{23, 24, 25} {
		cfg := baseConfig(seed)
		cfg.NumNodes = 5
		cfg.DropRate = 0.25
		cfg.DupRate = 0.10
		cfg.MaxDelay = 4
		s := NewSim(cfg, nil)
		s.Run(400)
		s.Cfg.DropRate = 0
		s.Cfg.DupRate = 0
		s.Run(1500)
		assertConvergedAllAlive(t, s)
	}
}

// A crashed node restarts with its persisted incarnation incremented; the
// cluster first condemns it, then accepts its reincarnation and converges.
func TestRestartBumpsPersistedIncarnation(t *testing.T) {
	cfg := baseConfig(5)
	cfg.NumNodes = 3
	cfg.SuspectTimeout = 20
	faults := []Fault{
		{Tick: 30, Kind: FaultCrash, Node: 2},
		{Tick: 120, Kind: FaultRestart, Node: 2},
	}
	s := NewSim(cfg, faults)
	s.Run(400)

	if !hasTransition(s, 0, 2, Dead) && !hasTransition(s, 1, 2, Dead) {
		t.Fatal("expected node 2 to be condemned while down")
	}
	if got := s.Incarnation(2); got != 1 {
		t.Fatalf("restart did not bump persisted incarnation: got %d, want 1", got)
	}
	assertConvergedAllAlive(t, s)
	for i := 0; i < cfg.NumNodes; i++ {
		if m := s.View(i)[2]; m.Incarnation != 1 || m.State != Alive {
			t.Fatalf("node %d view of restarted node: %v", i, m)
		}
	}
}

// Randomized run with loss, duplication, crashes, restarts and a partition.
// Every node's final view must match the independent state model, and no
// node may ever carry an incarnation for a member higher than the one that
// member raised for itself.
func TestModelCrossCheck(t *testing.T) {
	for _, seed := range []int64{42, 43, 44, 45} {
		cfg := baseConfig(seed)
		cfg.NumNodes = 5
		cfg.DropRate = 0.15
		cfg.DupRate = 0.05
		cfg.MaxDelay = 4
		faults := []Fault{
			{Tick: 150, Kind: FaultCrash, Node: 1},
			{Tick: 260, Kind: FaultRestart, Node: 1},
			{Tick: 320, Kind: FaultPartitionStart, Group: []int{0, 1}},
			{Tick: 420, Kind: FaultPartitionEnd},
			{Tick: 500, Kind: FaultCrash, Node: 3},
			{Tick: 560, Kind: FaultRestart, Node: 3},
		}
		s := NewSim(cfg, faults)
		s.Run(900)

		for _, n := range s.nodes {
			want := modelView(n.id, cfg.NumNodes, n.received)
			if !reflect.DeepEqual(want, n.view) {
				t.Fatalf("seed %d: node %d view diverges from independent model:\n got: %v\nwant: %v",
					seed, n.id, n.view, want)
			}
		}
		for m := 0; m < cfg.NumNodes; m++ {
			for _, n := range s.nodes {
				if got := n.view[m].Incarnation; got > s.nodes[m].incarnation {
					t.Fatalf("seed %d: node %d believes member %d has incarnation %d, but it only ever raised %d",
						seed, n.id, m, got, s.nodes[m].incarnation)
				}
			}
		}
	}
}

// Identical seed and fault plan must reproduce the run event by event.
func TestDeterministicReplay(t *testing.T) {
	run := func() (string, string) {
		cfg := baseConfig(99)
		cfg.NumNodes = 5
		cfg.DropRate = 0.2
		cfg.DupRate = 0.1
		cfg.MaxDelay = 4
		faults := []Fault{
			{Tick: 50, Kind: FaultCrash, Node: 2},
			{Tick: 120, Kind: FaultRestart, Node: 2},
			{Tick: 200, Kind: FaultPartitionStart, Group: []int{0, 3}},
			{Tick: 260, Kind: FaultPartitionEnd},
		}
		s := NewSim(cfg, faults)
		s.Run(500)
		return strings.Join(s.Log, "\n"), s.Report()
	}
	log1, report1 := run()
	log2, report2 := run()
	if log1 != log2 {
		t.Fatal("event logs differ between identical runs")
	}
	if report1 != report2 {
		t.Fatal("membership reports differ between identical runs")
	}
}
