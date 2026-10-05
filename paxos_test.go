package paxos

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

func deliverWhere(t *testing.T, sim *Sim, pred func(Envelope) bool) {
	t.Helper()
	if sim.DeliverWhere(pred) == 0 {
		t.Fatalf("no pending message matched")
	}
}

func checkSafety(t *testing.T, sim *Sim) {
	t.Helper()
	if err := sim.Checker.Check(sim.Nodes); err != nil {
		t.Fatalf("%v\ntrace:\n%s", err, strings.Join(sim.Trace, "\n"))
	}
}

// Stable majority + fair delivery must make progress, in order.
func TestLivenessStableMajorityFairDelivery(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(2)
	sim.DeliverAll()
	for _, v := range []string{"a", "b", "c"} {
		sim.Propose(2, Value(v))
	}
	sim.DeliverAll()
	want := []Value{"a", "b", "c"}
	if got := sim.Nodes[2].Applied(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	chosen := sim.Checker.Chosen()
	for i, v := range want {
		if chosen[i].Value != v {
			t.Fatalf("slot %d chosen %q, want %q", i, chosen[i].Value, v)
		}
	}
	checkSafety(t, sim)
}

// Reordered (LIFO) delivery must still terminate with the same result.
func TestLivenessReorderedDelivery(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAllReverse()
	sim.Propose(0, "x")
	sim.DeliverAllReverse()
	sim.Propose(0, "y")
	sim.DeliverAllReverse()
	want := []Value{"x", "y"}
	if got := sim.Nodes[0].Applied(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	checkSafety(t, sim)
}

// A minority old leader cannot choose anything; its accepted value is
// still adopted by the next quorum leader, and its stale ballots are then
// rejected.
func TestMinorityOldLeader(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAll() // leader 0 finishes phase 1
	sim.Propose(0, "old")
	// Only a minority (nodes 0,1) accepts "old".
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.To <= 1
	})
	sim.DropAll()
	if _, ok := sim.Checker.Chosen()[0]; ok {
		t.Fatalf("minority accept must not be chosen")
	}
	// New leader with a higher ballot: quorum promises include the
	// minority-accepted value, so it must be preserved.
	sim.StartLeader(3)
	sim.DeliverAll()
	sim.Propose(3, "new")
	sim.DeliverAll()
	if got := sim.Checker.Chosen()[0].Value; got != "old" {
		t.Fatalf("slot 0 chosen %q, want adopted %q", got, "old")
	}
	if got := sim.Checker.Chosen()[1].Value; got != "new" {
		t.Fatalf("slot 1 chosen %q, want %q", got, "new")
	}
	// The old leader's stale ballot is now rejected silently.
	stale := sim.Inject(0, 2, Message{Kind: MsgAccept, Ballot: Ballot{Round: 1, ID: 0}, Slot: 0, Value: "old"})
	before := sim.CountSent(2)
	if !sim.Deliver(stale) {
		t.Fatalf("stale message missing")
	}
	if sim.CountSent(2) != before {
		t.Fatalf("node 2 answered a stale-ballot Accept")
	}
	checkSafety(t, sim)
}

// A value chosen by a majority whose "Accepted" notifications were lost
// must survive the old leader's crash: the next leader re-adopts it.
func TestChosenButNotificationLost(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAll()
	sim.Propose(0, "x")
	// Acceptors 0,1,2 durably accept x at slot 0, but every reply and the
	// remaining Accepts are dropped: leader 0 never learns it is chosen.
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.Msg.Slot == 0 && e.To <= 2
	})
	sim.DropAll()
	if ev, ok := sim.Checker.Chosen()[0]; !ok || ev.Value != "x" {
		t.Fatalf("checker: slot 0 should be chosen as x, got %v", sim.Checker.Chosen())
	}
	if n := len(sim.Nodes[0].Applied()); n != 0 {
		t.Fatalf("leader 0 applied %d entries despite lost notifications", n)
	}
	sim.Crash(0)
	// New leader must re-adopt x at slot 0 rather than overwriting it.
	sim.StartLeader(3)
	sim.DeliverAll()
	sim.Propose(3, "y")
	sim.DeliverAll()
	want := []Value{"x", "y"}
	if got := sim.Nodes[3].Applied(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	checkSafety(t, sim)
}

// A hole before an accepted value: the recovering leader fills the holes
// with no-op and preserves the later accepted value, so the applied
// prefix advances through it.
func TestHoleThenValue(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAll()
	sim.Propose(0, "a")
	sim.Propose(0, "b")
	sim.Propose(0, "c")
	// Only slot 2 ("c") reaches a majority; slots 0 and 1 stay holes.
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.Msg.Slot == 2 && e.To <= 2
	})
	sim.DropAll()
	sim.Crash(0)
	sim.StartLeader(3)
	sim.DeliverAll()
	want := []Value{Noop, Noop, "c"}
	if got := sim.Nodes[3].Applied(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	if got := sim.Checker.Chosen()[2].Value; got != "c" {
		t.Fatalf("slot 2 chosen %q, want c", got)
	}
	checkSafety(t, sim)
}

// Repeated recovery: two leaders crash mid-recovery; each new leader must
// re-adopt the highest accepted value instead of overwriting it.
func TestRepeatedRecovery(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAll()
	sim.Propose(0, "x")
	// x accepted only by node 2 at ballot (1,0); leader 0 crashes.
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.Msg.Slot == 0 && e.To == 2
	})
	sim.DropAll()
	sim.Crash(0)
	// Leader 1 recovers, re-proposes x at ballot (2,1); only node 3
	// accepts before leader 1 crashes too.
	sim.StartLeader(1)
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgPrepare && (e.To == 1 || e.To == 2 || e.To == 3)
	})
	deliverWhere(t, sim, func(e Envelope) bool { return e.Msg.Kind == MsgPromise })
	if !sim.Nodes[1].Phase1Done() {
		t.Fatalf("leader 1 did not finish phase 1")
	}
	deliverWhere(t, sim, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.Msg.Slot == 0 && e.To == 3
	})
	sim.DropAll()
	sim.Crash(1)
	// Leader 4 recovers again and must still land on x.
	sim.StartLeader(4)
	sim.DeliverAll()
	want := []Value{"x"}
	if got := sim.Nodes[4].Applied(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	checkSafety(t, sim)
}

// Persistence failure: a node whose store rejects writes must stay silent
// (never promise, never accept). With too many failing stores no quorum
// exists and nothing gets chosen; healing restores progress.
func TestPersistenceFailure(t *testing.T) {
	sim, stores := NewSimMem(5)
	stores[4].SetFail(true)
	sim.StartLeader(0)
	sim.DeliverAll()
	if got := sim.CountSent(4); got != 0 {
		t.Fatalf("node 4 emitted %d messages despite persistence failure", got)
	}
	if !sim.Nodes[0].Phase1Done() {
		t.Fatalf("leader 0 should still reach a quorum without node 4")
	}
	sim.Propose(0, "a")
	sim.DeliverAll()
	if got := sim.Checker.Chosen()[0].Value; got != "a" {
		t.Fatalf("slot 0 chosen %q, want a", got)
	}
	// Now three stores fail: no quorum can form.
	stores[1].SetFail(true)
	stores[2].SetFail(true)
	sim.Crash(1)
	sim.Restart(1)
	sim.Crash(2)
	sim.Restart(2)
	sim.StartLeader(3)
	sim.DeliverAll()
	if sim.Nodes[3].Phase1Done() {
		t.Fatalf("leader 3 finished phase 1 without a quorum of durable acceptors")
	}
	sim.Propose(3, "v")
	sim.DeliverAll()
	if _, ok := sim.Checker.Chosen()[1]; ok {
		t.Fatalf("slot 1 chosen without a durable quorum")
	}
	checkSafety(t, sim)
	// Healing the stores lets the same leader make progress again.
	for _, st := range stores {
		st.SetFail(false)
	}
	sim.Crash(1)
	sim.Restart(1)
	sim.Crash(2)
	sim.Restart(2)
	sim.Crash(4)
	sim.Restart(4)
	sim.StartLeader(3)
	sim.DeliverAll()
	sim.Propose(3, "v")
	sim.DeliverAll()
	if got := sim.Nodes[3].Applied(); len(got) < 2 || got[1] != "v" {
		t.Fatalf("applied = %v, want slot 1 = v", got)
	}
	checkSafety(t, sim)
}

// Restart preserves promised and accepted state; old-ballot messages are
// rejected after the restart.
func TestRestartDurabilityAndStaleBallot(t *testing.T) {
	sim, _ := NewSimMem(5)
	sim.StartLeader(0)
	sim.DeliverAll()
	sim.Propose(0, "x")
	sim.DeliverAll()
	promised := sim.Nodes[2].Promised()
	accepted := sim.Nodes[2].AcceptedMap()
	if len(accepted) == 0 {
		t.Fatalf("node 2 accepted nothing")
	}
	sim.Crash(2)
	sim.Restart(2)
	if sim.Nodes[2].Promised() != promised {
		t.Fatalf("promised = %v after restart, want %v", sim.Nodes[2].Promised(), promised)
	}
	if !reflect.DeepEqual(sim.Nodes[2].AcceptedMap(), accepted) {
		t.Fatalf("accepted = %v after restart, want %v", sim.Nodes[2].AcceptedMap(), accepted)
	}
	// A prepare with an older ballot must be rejected (no reply).
	sim.Inject(4, 2, Message{Kind: MsgPrepare, Ballot: Ballot{Round: promised.Round - 1, ID: 4}})
	before := sim.CountSent(2)
	sim.DeliverAll()
	if sim.CountSent(2) != before {
		t.Fatalf("node 2 answered a stale-ballot Prepare after restart")
	}
	checkSafety(t, sim)
}

// Independent safety check under randomized loss, duplication, reorder,
// crash and restart: no slot may ever be chosen with two values.
func TestSafetyChaos(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sim, _ := NewSimMem(5)
		for step := 0; step < 600; step++ {
			switch rng.Intn(12) {
			case 0, 1:
				sim.StartLeader(rng.Intn(5))
			case 2, 3:
				sim.Propose(rng.Intn(5), Value(fmt.Sprintf("v%d", rng.Intn(8))))
			case 4, 5, 6, 7:
				if p := sim.Pending(); len(p) > 0 {
					sim.Deliver(p[rng.Intn(len(p))].ID)
				}
			case 8:
				if p := sim.Pending(); len(p) > 0 {
					sim.Drop(p[rng.Intn(len(p))].ID)
				}
			case 9:
				if p := sim.Pending(); len(p) > 0 {
					sim.Dup(p[rng.Intn(len(p))].ID)
				}
			case 10:
				sim.Crash(rng.Intn(5))
			case 11:
				sim.Restart(rng.Intn(5))
			}
		}
		sim.DeliverAll()
		if err := sim.Checker.Check(sim.Nodes); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// The shipped demo script must run cleanly end to end.
func TestDemoScript(t *testing.T) {
	f, err := os.Open("examples/demo.script")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sim, _ := NewSimMem(5)
	var out bytes.Buffer
	if err := RunScript(sim, f, &out); err != nil {
		t.Fatalf("%v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "check OK") {
		t.Fatalf("demo did not pass the safety check:\n%s", out.String())
	}
}
