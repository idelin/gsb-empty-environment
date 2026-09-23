package jointpaxos

import (
	"math/rand"
	"testing"
)

func stdSets() (oldSet, newSet []string) {
	return []string{"a", "b", "c"}, []string{"x", "y", "z"}
}

func stdPropose(joint, final *Config) func(view int) Value {
	return func(view int) Value {
		switch view {
		case 0:
			return Value{Cmd: "op-1", Next: joint}
		case 1:
			return Value{Cmd: "op-2", Next: final}
		default:
			return Value{Cmd: "op-3"}
		}
	}
}

func newCluster(oldSet, newSet []string, propose func(view int) Value) (*Sim, *Network, map[string]*Node) {
	sim := &Sim{}
	net := NewNetwork(sim)
	nodes := map[string]*Node{}
	all := append(append([]string{}, oldSet...), newSet...)
	for _, id := range all {
		nodes[id] = NewNode(id, NewStore(Config{Old: oldSet}), sim, net, propose)
	}
	return sim, net, nodes
}

func bootAll(nodes map[string]*Node) {
	for _, nd := range nodes {
		nd.Boot()
	}
}

func runUntil(sim *Sim, maxTime int, cond func() bool) bool {
	for sim.Now < maxTime {
		if cond() {
			return true
		}
		if !sim.Step() {
			return cond()
		}
	}
	return cond()
}

func allCommitted(nodes map[string]*Node, view int) bool {
	for _, nd := range nodes {
		if _, ok := nd.Committed(view); !ok {
			return false
		}
	}
	return true
}

func assertChainDone(t *testing.T, nodes map[string]*Node) {
	t.Helper()
	for id, nd := range nodes {
		for v := 0; v <= 2; v++ {
			if _, ok := nd.Committed(v); !ok {
				t.Fatalf("%s: missing commit in view %d", id, v)
			}
		}
		if nd.CurrentView() != 2 {
			t.Fatalf("%s: stuck in view %d", id, nd.CurrentView())
		}
		if len(nd.ViolationList()) != 0 {
			t.Fatalf("%s: safety violations %v", id, nd.ViolationList())
		}
	}
}

// --- safety evidence ---

// Joint quorums always intersect; an old-set majority and a disjoint
// new-set majority need not. This is exactly why the direct switch is
// unsafe and the joint stage is required.
func TestQuorumIntersection(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	members := joint.Members()
	var quorums []map[string]bool
	for mask := 0; mask < 1<<uint(len(members)); mask++ {
		votes := map[string]bool{}
		for i, m := range members {
			if mask&(1<<uint(i)) != 0 {
				votes[m] = true
			}
		}
		if joint.Quorum(votes) {
			quorums = append(quorums, votes)
		}
	}
	if len(quorums) == 0 {
		t.Fatal("no joint quorums found")
	}
	for _, q1 := range quorums {
		for _, q2 := range quorums {
			inter := false
			for m := range q1 {
				if q2[m] {
					inter = true
				}
			}
			if !inter {
				t.Fatalf("disjoint joint-quorums %v and %v", q1, q2)
			}
		}
	}
	// Disjoint plain majorities: possible, hence direct switch is unsafe.
	oldQ := map[string]bool{"a": true, "b": true}
	newQ := map[string]bool{"x": true, "y": true}
	if !(Config{Old: oldSet}).Quorum(oldQ) || !(Config{Old: newSet}).Quorum(newQ) {
		t.Fatal("expected both to be plain majorities")
	}
	for m := range oldQ {
		if newQ[m] {
			t.Fatal("expected disjoint plain majorities")
		}
	}
}

// The transition guard rejects a direct old->new disjoint switch and
// accepts only the staged path plain -> joint -> new.
func TestTransitionGuard(t *testing.T) {
	oldSet, newSet := stdSets()
	plain := Config{Old: oldSet}
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	if validTransition(plain, final) {
		t.Fatal("direct disjoint switch must be rejected")
	}
	if !validTransition(plain, joint) {
		t.Fatal("plain -> joint must be allowed")
	}
	if !validTransition(joint, final) {
		t.Fatal("joint -> new must be allowed")
	}
	if validTransition(joint, plain) {
		t.Fatal("joint -> old must be rejected")
	}
	overlap := Config{Old: []string{"a", "b", "x"}}
	if !validTransition(plain, overlap) {
		t.Fatal("intersecting plain -> plain must be allowed")
	}
}

// Each set reaches its own majority (old accepts X, new accepts Y), yet
// the joint condition is not met, so nothing may commit.
func TestSeparateMajoritiesCannotCommit(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	sim, _, nodes := newCluster(oldSet, newSet, nil)
	for _, nd := range nodes {
		nd.store.Confs[1] = joint
		nd.store.CurrentView = 1
	}
	bal := Ballot{View: 1, Num: 0}
	for _, id := range oldSet {
		nodes[id].Handle(Msg{Kind: MAccept, View: 1, Bal: bal, Val: Value{Cmd: "X"}, From: "L1", To: id})
	}
	for _, id := range newSet {
		nodes[id].Handle(Msg{Kind: MAccept, View: 1, Bal: bal, Val: Value{Cmd: "Y"}, From: "L2", To: id})
	}
	sim.Run(50)
	oldVotes := map[string]bool{}
	newVotes := map[string]bool{}
	for _, id := range oldSet {
		vs := nodes[id].store.Views[1]
		if !vs.Accepted || vs.AccVal.Cmd != "X" {
			t.Fatalf("%s: expected to have accepted X", id)
		}
		oldVotes[id] = true
	}
	for _, id := range newSet {
		vs := nodes[id].store.Views[1]
		if !vs.Accepted || vs.AccVal.Cmd != "Y" {
			t.Fatalf("%s: expected to have accepted Y", id)
		}
		newVotes[id] = true
	}
	// Each side alone is a majority of its own set...
	if !majorityOf(oldSet, oldVotes) || !majorityOf(newSet, newVotes) {
		t.Fatal("expected each set to hold its own majority")
	}
	// ...but neither side's votes satisfy the joint condition.
	if joint.Quorum(oldVotes) || joint.Quorum(newVotes) {
		t.Fatal("joint quorum must require both sets")
	}
	for _, nd := range nodes {
		if _, ok := nd.Committed(1); ok {
			t.Fatal("commit happened without joint quorum")
		}
	}
}

// A network partition between the old and new sets during the joint
// stage blocks the switch; healing lets it complete.
func TestJointSwitchBlockedByPartition(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	sim, net, nodes := newCluster(oldSet, newSet, stdPropose(&joint, &final))
	bootAll(nodes)
	inOld := map[string]bool{}
	for _, id := range oldSet {
		inOld[id] = true
	}
	if !runUntil(sim, 100, func() bool {
		for _, nd := range nodes {
			if nd.CurrentView() < 1 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("cluster never reached the joint view")
	}
	// Partition: old and new cannot exchange view-1 messages.
	net.Drop = func(from, to string, m Msg) bool {
		return m.View == 1 && inOld[from] != inOld[to]
	}
	sim.Run(sim.Now + 120)
	for _, nd := range nodes {
		if _, ok := nd.Committed(1); ok {
			t.Fatal("joint view committed under old/new partition")
		}
	}
	// Heal: the switch must go through.
	net.Drop = nil
	sim.Run(sim.Now + 300)
	assertChainDone(t, nodes)
}

// Late votes stamped with an old view version never count in a newer
// view: neither a naturally delayed vote nor an injected stale Accept.
func TestLateVotesFromOldViewIgnored(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	sim, net, nodes := newCluster(oldSet, newSet, stdPropose(&joint, &final))
	// Hold back one view-0 vote so it arrives after the leader moved on.
	net.Delay = func(from, to string, m Msg) int {
		if m.Kind == MAccepted && m.View == 0 && from == "c" {
			return 40
		}
		return 1
	}
	bootAll(nodes)
	sim.Run(200)
	assertChainDone(t, nodes)
	if nodes["a"].StaleIgnoredCount() == 0 {
		t.Fatal("expected the delayed view-0 vote to be ignored as stale")
	}
	// Injected stale Accept must not alter acceptor state.
	nd := nodes["a"]
	before := nd.StaleIgnoredCount()
	nd.Handle(Msg{Kind: MAccept, View: 1, Bal: Ballot{View: 1, Num: 99},
		Val: Value{Cmd: "late"}, From: "b", To: "a"})
	if nd.StaleIgnoredCount() != before+1 {
		t.Fatal("stale Accept was not counted as ignored")
	}
	if vs := nd.store.Views[1]; vs.Accepted && vs.AccVal.Cmd == "late" {
		t.Fatal("stale Accept mutated view-1 acceptor state")
	}
	if v, _ := nd.Committed(1); v.Cmd != "op-2" {
		t.Fatal("committed value changed after stale message")
	}
}

// A leader proposing a direct jump to the disjoint new set is refused by
// every acceptor, nothing commits, and a forged Commit is refused too.
func TestDirectDisjointSwitchRejected(t *testing.T) {
	oldSet, newSet := stdSets()
	evil := Config{Old: newSet}
	propose := func(view int) Value {
		return Value{Cmd: "op-1", Next: &evil}
	}
	sim, _, nodes := newCluster(oldSet, newSet, propose)
	bootAll(nodes)
	sim.Run(120)
	rejected := 0
	for _, nd := range nodes {
		if _, ok := nd.Committed(0); ok {
			t.Fatal("unsafe direct switch committed")
		}
		if nd.CurrentView() != 0 {
			t.Fatal("node advanced to an unsafe config")
		}
		rejected += nd.RejectedReconfigs()
	}
	if rejected == 0 {
		t.Fatal("expected acceptors to reject the unsafe switch")
	}
	nd := nodes["a"]
	nd.Handle(Msg{Kind: MCommit, View: 0, Val: Value{Cmd: "op-1", Next: &evil}, From: "x", To: "a"})
	if _, ok := nd.Committed(0); ok {
		t.Fatal("forged Commit for unsafe switch was accepted")
	}
	if nd.CurrentView() != 0 {
		t.Fatal("forged Commit moved the node to a new view")
	}
}

// Randomized fault injection: drops, delays, crashes and restarts.
// In every run, all committed values per view must agree, the config
// chain must follow old -> joint -> new, and no violation is recorded.
func TestSafetyUnderRandomFaults(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	completed := 0
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sim, net, nodes := newCluster(oldSet, newSet, stdPropose(&joint, &final))
		bootAll(nodes)
		net.Drop = func(from, to string, m Msg) bool { return rng.Float64() < 0.15 }
		net.Delay = func(from, to string, m Msg) int { return 1 + rng.Intn(4) }
		ids := append(append([]string{}, oldSet...), newSet...)
		for i := 0; i < 6; i++ {
			at := rng.Intn(120)
			id := ids[rng.Intn(len(ids))]
			back := at + 20 + rng.Intn(30)
			sim.After(at, func() { nodes[id].Crash() })
			sim.After(back, func() { nodes[id].Restart() })
		}
		sim.Run(600)
		for v := 0; v <= 2; v++ {
			var seen *Value
			for _, nd := range nodes {
				val, ok := nd.Committed(v)
				if !ok {
					continue
				}
				if seen == nil {
					v := val
					seen = &v
				} else if !equalValue(*seen, val) {
					t.Fatalf("seed %d: conflicting commits in view %d: %s vs %s",
						seed, v, *seen, val)
				}
			}
		}
		if val, ok := nodes["a"].Committed(0); ok && (val.Next == nil || !sameSet(val.Next.Old, oldSet) || !val.Next.Joint) {
			t.Fatalf("seed %d: view 0 decided an invalid next config", seed)
		}
		if val, ok := nodes["a"].Committed(1); ok && (val.Next == nil || !sameSet(val.Next.Old, newSet) || val.Next.Joint) {
			t.Fatalf("seed %d: joint view decided an invalid next config", seed)
		}
		for _, nd := range nodes {
			if len(nd.ViolationList()) > 0 {
				t.Fatalf("seed %d: violations %v", seed, nd.ViolationList())
			}
		}
		if allCommitted(nodes, 2) {
			completed++
		}
	}
	t.Logf("%d/50 random-fault runs completed the full switch", completed)
	if completed == 0 {
		t.Fatal("no run completed; test would be vacuous")
	}
}

// --- liveness evidence ---

// Without faults, the whole reconfiguration finishes in bounded virtual
// time and every node commits the same command sequence.
func TestFaultFreeLiveness(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	sim, _, nodes := newCluster(oldSet, newSet, stdPropose(&joint, &final))
	bootAll(nodes)
	sim.Run(60) // bounded: well before any leader-retry timeout storm
	assertChainDone(t, nodes)
	for id, nd := range nodes {
		cmds := nd.CommittedCmds()
		if len(cmds) != 3 || cmds[0] != "op-1" || cmds[1] != "op-2" || cmds[2] != "op-3" {
			t.Fatalf("%s: unexpected commit sequence %v", id, cmds)
		}
	}
	t.Logf("full old->joint->new switch committed everywhere by t=%d", sim.Now)
}

// The switch is interrupted mid-flight by crashes that destroy the joint
// quorum; after restart (from persisted state) it resumes and completes.
func TestRecoveryFromInterruptedSwitch(t *testing.T) {
	oldSet, newSet := stdSets()
	joint := Config{Old: oldSet, New: newSet, Joint: true}
	final := Config{Old: newSet}
	sim, _, nodes := newCluster(oldSet, newSet, stdPropose(&joint, &final))
	bootAll(nodes)
	// Run until the joint-stage switch is in flight (some acceptor has
	// accepted in view 1) but not yet committed.
	started := runUntil(sim, 100, func() bool {
		if allCommitted(nodes, 1) {
			return false
		}
		for _, nd := range nodes {
			if vs := nd.store.Views[1]; vs != nil && vs.Accepted {
				return true
			}
		}
		return false
	})
	if !started {
		t.Fatal("joint-stage switch never got in flight")
	}
	// Interrupt: crash enough nodes that no joint quorum survives.
	nodes["a"].Crash()
	nodes["b"].Crash()
	nodes["x"].Crash()
	sim.Run(sim.Now + 60)
	for _, nd := range nodes {
		if _, ok := nd.Committed(1); ok {
			t.Fatal("joint view committed while its quorum was crashed")
		}
	}
	// Recover: persisted promises/accepts are reloaded, switch resumes.
	nodes["a"].Restart()
	nodes["b"].Restart()
	nodes["x"].Restart()
	sim.Run(sim.Now + 300)
	assertChainDone(t, nodes)
}
