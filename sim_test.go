package membersim

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func countWhere(ts []Transition, pred func(Transition) bool) int {
	n := 0
	for _, t := range ts {
		if pred(t) {
			n++
		}
	}
	return n
}

func logsContain(logs []string, sub string) bool {
	for _, l := range logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// --- independent state model ------------------------------------------------

// specMerge is an independent, deliberately different formulation of the
// merge rule: the winner is simply the maximum of the (incarnation, rank)
// tuples.
func specMerge(a, b Member) Member {
	key := func(m Member) [2]uint64 { return [2]uint64{m.Incarnation, uint64(stateRank(m.State))} }
	ka, kb := key(a), key(b)
	if kb[0] > ka[0] || (kb[0] == ka[0] && kb[1] > ka[1]) {
		return b
	}
	return a
}

func TestMergeMatchesIndependentModel(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	states := []State{Alive, Suspect, Dead}
	for i := 0; i < 20000; i++ {
		a := Member{State: states[rng.Intn(3)], Incarnation: uint64(rng.Intn(6))}
		b := Member{State: states[rng.Intn(3)], Incarnation: uint64(rng.Intn(6))}

		merged, changed := mergeMembers(a, b)
		if want := specMerge(a, b); merged != want {
			t.Fatalf("merge(%v,%v) = %v, model says %v", a, b, merged, want)
		}
		if changed != (merged != a) {
			t.Fatalf("merge(%v,%v) changed=%v but merged=%v", a, b, changed, merged)
		}
		// commutativity
		mergedBA, _ := mergeMembers(b, a)
		if mergedBA != merged {
			t.Fatalf("merge not commutative: (%v,%v)->%v vs %v", a, b, merged, mergedBA)
		}
		// idempotence
		if _, ch := mergeMembers(a, a); ch {
			t.Fatalf("merge(%v,%v) reported change", a, a)
		}
	}
}

// --- late ack ---------------------------------------------------------------

// Acks arrive after the direct-probe deadline but well before the indirect
// deadline: indirect probes must start, yet nobody may be suspected.
func TestLateAckDoesNotSuspect(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 21
	cfg.NumNodes = 5
	cfg.ProbeTimeout = 3
	cfg.IndirectTimeout = 30
	cfg.SuspicionTimeout = 60
	cfg.MinDelay, cfg.MaxDelay = 6, 8 // every ack is "late" for the direct probe

	s := NewSim(cfg, nil)
	s.Run(600)

	if !logsContain(s.Logs(), "indirect probes") {
		t.Fatalf("expected indirect probes to be exercised")
	}
	if n := countWhere(s.Transitions(), func(tr Transition) bool { return tr.To.State == Suspect }); n > 0 {
		t.Fatalf("late acks caused %d suspect transitions", n)
	}
	if n := countWhere(s.Transitions(), func(tr Transition) bool { return tr.To.State == Dead }); n > 0 {
		t.Fatalf("late acks caused %d dead transitions", n)
	}
}

// --- refutation during suspicion -------------------------------------------

// The network is slower than the probe deadlines, so nodes get suspected;
// but the suspicion window is long enough for the suspected node to learn
// of the rumor and refute it with a higher incarnation before going dead.
func TestRefutationDuringSuspicion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 3
	cfg.NumNodes = 5
	cfg.Period = 10
	cfg.ProbeTimeout = 3
	cfg.IndirectTimeout = 5
	cfg.SuspicionTimeout = 80
	cfg.MinDelay, cfg.MaxDelay = 10, 10

	s := NewSim(cfg, nil)
	s.Run(800)

	ts := s.Transitions()
	if n := countWhere(ts, func(tr Transition) bool { return tr.To.State == Suspect }); n == 0 {
		t.Fatalf("expected some suspicions with a slow network")
	}
	if n := countWhere(ts, func(tr Transition) bool {
		return tr.From.State == Suspect && tr.To.State == Alive
	}); n == 0 {
		t.Fatalf("expected suspicions to be cleared by refutations")
	}
	if !logsContain(s.Logs(), "refuted") && countWhere(ts, func(tr Transition) bool {
		return strings.Contains(tr.Reason, "refuted")
	}) == 0 {
		t.Fatalf("expected self-refutation transitions")
	}
	if n := countWhere(ts, func(tr Transition) bool { return tr.To.State == Dead }); n > 0 {
		t.Fatalf("%d nodes died despite refuting inside the suspicion window", n)
	}
}

// --- stale timers ------------------------------------------------------------

// A suspicion timer tokened by an old incarnation must not kill a member
// that has since been refreshed by a higher-incarnation alive.
func TestStaleSuspicionTimerIgnored(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 5
	cfg.NumNodes = 3

	s := NewSim(cfg, nil)
	n0 := s.nodes[0]

	// node0 suspects node1 at inc=2 (timer queued), then a fresher
	// alive(inc=3) arrives before the timer fires.
	n0.applyUpdate(Update{Node: 1, State: Suspect, Incarnation: 2}, "injected suspect")
	n0.applyUpdate(Update{Node: 1, State: Alive, Incarnation: 3}, "injected refutation")
	s.Run(cfg.SuspicionTimeout + 200)

	if got := s.View(0)[1]; got != (Member{State: Alive, Incarnation: 3}) {
		t.Fatalf("stale timer corrupted view: got %v", got)
	}
	if n := countWhere(s.Transitions(), func(tr Transition) bool {
		return tr.Node == 0 && tr.Subject == 1 && tr.To.State == Dead
	}); n > 0 {
		t.Fatalf("stale suspicion timer marked refreshed member dead")
	}
	if !logsContain(s.Logs(), "stale suspicion timer") {
		t.Fatalf("expected the stale timer to fire and be ignored")
	}

	// Direct token check: wrong incarnation is ignored, matching one kills.
	n0.members[1] = Member{State: Suspect, Incarnation: 5}
	n0.onSuspicionTimeout(1, 4)
	if got := n0.members[1]; got != (Member{State: Suspect, Incarnation: 5}) {
		t.Fatalf("timer with stale token changed view to %v", got)
	}
	n0.onSuspicionTimeout(1, 5)
	if got := n0.members[1]; got != (Member{State: Dead, Incarnation: 5}) {
		t.Fatalf("valid suspicion timer did not declare dead: %v", got)
	}
}

// --- partition recovery ------------------------------------------------------

func TestPartitionRecovery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 11
	cfg.NumNodes = 6
	faults := []Fault{
		{Tick: 200, Kind: FaultPartition, Nodes: []int{0, 1, 2}},
		{Tick: 800, Kind: FaultHeal},
	}
	s := NewSim(cfg, faults)
	s.Run(3000)

	ts := s.Transitions()
	if n := countWhere(ts, func(tr Transition) bool {
		return tr.To.State == Dead && tr.Tick >= 200 && tr.Tick < 800
	}); n == 0 {
		t.Fatalf("expected cross-partition deaths during the partition")
	}
	// After healing, every node must see everyone alive and all views agree.
	for i := 0; i < cfg.NumNodes; i++ {
		view := s.View(i)
		if len(view) != cfg.NumNodes {
			t.Fatalf("node %d sees %d members, want %d", i, len(view), cfg.NumNodes)
		}
		for id, m := range view {
			if m.State != Alive {
				t.Fatalf("node %d still sees %d as %s(inc=%d) after heal", i, id, m.State, m.Incarnation)
			}
		}
		if i > 0 && !reflect.DeepEqual(s.View(0), view) {
			t.Fatalf("views diverged after heal:\nnode0: %v\nnode%d: %v", s.View(0), i, view)
		}
	}
}

// --- convergence -------------------------------------------------------------

func TestConvergenceWithoutLoss(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 7
	cfg.NumNodes = 5
	s := NewSim(cfg, nil)
	s.Run(2000)

	if len(s.Transitions()) != 0 {
		t.Fatalf("unexpected transitions in a quiet healthy cluster: %v", s.Transitions()[0])
	}
	want := map[int]Member{}
	for i := 0; i < cfg.NumNodes; i++ {
		want[i] = Member{State: Alive}
	}
	for i := 0; i < cfg.NumNodes; i++ {
		if !reflect.DeepEqual(s.View(i), want) {
			t.Fatalf("node %d view = %v, want %v", i, s.View(i), want)
		}
	}
}

// A crashed node is declared dead; after restart it rejoins with a bumped
// persistent incarnation and the cluster reconverges.
func TestRestartRejoins(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 13
	cfg.NumNodes = 5
	faults := []Fault{
		{Tick: 100, Kind: FaultCrash, Nodes: []int{2}},
		{Tick: 400, Kind: FaultRestart, Nodes: []int{2}},
	}
	s := NewSim(cfg, faults)
	s.Run(2000)

	ts := s.Transitions()
	if n := countWhere(ts, func(tr Transition) bool {
		return tr.Subject == 2 && tr.To.State == Dead
	}); n == 0 {
		t.Fatalf("crashed node was never declared dead")
	}
	for i := 0; i < cfg.NumNodes; i++ {
		view := s.View(i)
		for id, m := range view {
			if m.State != Alive {
				t.Fatalf("node %d sees %d as %s after restart", i, id, m.State)
			}
		}
		// A probe in flight across the restart boundary may legitimately
		// suspect the restarted node once, which it then refutes; the
		// cluster must still agree on whatever incarnation it reached.
		if view[2].Incarnation < 1 {
			t.Fatalf("node %d sees restarted node at inc=%d, want >= 1", i, view[2].Incarnation)
		}
		if i > 0 && !reflect.DeepEqual(s.View(0), view) {
			t.Fatalf("views diverged after restart:\nnode0: %v\nnode%d: %v", s.View(0), i, view)
		}
	}
	if got := s.View(0)[2].Incarnation; got != s.nodes[2].incarnation {
		t.Fatalf("cluster sees node 2 at inc=%d but node 2 is at inc=%d", got, s.nodes[2].incarnation)
	}
}

// --- incarnation ownership ---------------------------------------------------

// Scan a rich run (crashes, restarts, partition) and verify that every
// incarnation ever observed for a subject was raised by the subject itself.
func TestOnlySelfRaisesIncarnation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Seed = 17
	cfg.NumNodes = 6
	cfg.DropProb = 0.1
	cfg.DupProb = 0.05
	faults := []Fault{
		{Tick: 150, Kind: FaultCrash, Nodes: []int{1}},
		{Tick: 350, Kind: FaultRestart, Nodes: []int{1}},
		{Tick: 500, Kind: FaultPartition, Nodes: []int{0, 1, 2}},
		{Tick: 900, Kind: FaultHeal},
	}
	s := NewSim(cfg, faults)
	s.Run(2500)

	allowed := map[int]map[uint64]bool{}
	for id, bumps := range s.selfBumps {
		allowed[id] = map[uint64]bool{0: true}
		for _, inc := range bumps {
			allowed[id][inc] = true
		}
	}
	for id := 0; id < cfg.NumNodes; id++ {
		if allowed[id] == nil {
			allowed[id] = map[uint64]bool{0: true}
		}
	}
	for _, tr := range s.Transitions() {
		if !allowed[tr.Subject][tr.To.Incarnation] {
			t.Fatalf("node %d adopted incarnation %d for %d which the subject never raised: %s",
				tr.Node, tr.To.Incarnation, tr.Subject, tr)
		}
	}

	// A dead declaration at incarnation i must never be followed by an
	// alive at the same incarnation in the same observer's view.
	type pair struct{ obs, sub int }
	lastDead := map[pair]uint64{}
	for _, tr := range s.Transitions() {
		p := pair{tr.Node, tr.Subject}
		if tr.To.State == Dead {
			lastDead[p] = tr.To.Incarnation
		}
		if tr.To.State == Alive {
			if inc, ok := lastDead[p]; ok && tr.To.Incarnation == inc {
				t.Fatalf("dead(%d,inc=%d) overridden by alive at same incarnation in node %d's view",
					tr.Subject, inc, tr.Node)
			}
		}
	}
}

// --- deterministic replay ----------------------------------------------------

func TestDeterministicReplay(t *testing.T) {
	run := func() (*sim, *sim) {
		cfg := DefaultConfig()
		cfg.Seed = 42
		cfg.NumNodes = 6
		cfg.DropProb = 0.15
		cfg.DupProb = 0.1
		cfg.MaxDelay = 4
		faults := []Fault{
			{Tick: 120, Kind: FaultCrash, Nodes: []int{3}},
			{Tick: 300, Kind: FaultRestart, Nodes: []int{3}},
			{Tick: 450, Kind: FaultPartition, Nodes: []int{0, 1}},
			{Tick: 700, Kind: FaultHeal},
		}
		a := NewSim(cfg, faults)
		a.Run(1500)
		b := NewSim(cfg, faults)
		b.Run(1500)
		return a, b
	}
	a, b := run()
	if !reflect.DeepEqual(a.Transitions(), b.Transitions()) {
		t.Fatalf("same seed+plan produced different transition logs")
	}
	if !reflect.DeepEqual(a.Logs(), b.Logs()) {
		t.Fatalf("same seed+plan produced different event logs")
	}
	for i := 0; i < a.cfg.NumNodes; i++ {
		if !reflect.DeepEqual(a.View(i), b.View(i)) {
			t.Fatalf("replay diverged in node %d's view", i)
		}
	}
}
