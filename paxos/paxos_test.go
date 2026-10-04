package paxos

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// ---- helpers ----

func deliverWhere(s *Sim, pred func(Envelope) bool) {
	for _, e := range s.Pending() {
		if pred(e) {
			s.Deliver(e.ID)
		}
	}
}

func dropWhere(s *Sim, pred func(Envelope) bool) {
	for _, e := range s.Pending() {
		if pred(e) {
			s.Drop(e.ID)
		}
	}
}

func checkNoViolations(t *testing.T, s *Sim) {
	t.Helper()
	if vs := s.Checker().Violations(); len(vs) > 0 {
		t.Fatalf("safety violations: %v\n%s", vs, s.Report())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wantApplied(t *testing.T, s *Sim, node int, want []string) {
	t.Helper()
	if got := s.Node(node).Applied(); !equalStrings(got, want) {
		t.Fatalf("node %d applied=%v, want %v\n%s", node, got, want, s.Report())
	}
}

func wantChosen(t *testing.T, s *Sim, slot int, want string) {
	t.Helper()
	got, ok := s.Checker().Chosen(slot)
	if !ok {
		t.Fatalf("slot %d not chosen, want %q\n%s", slot, want, s.Report())
	}
	if got != want {
		t.Fatalf("slot %d chosen=%q, want %q\n%s", slot, got, want, s.Report())
	}
}

// ---- basic commit / apply ----

func TestBasicCommitApply(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "a")
	s.Propose(0, "b")
	s.DeliverAll()

	wantChosen(t, s, 1, "a")
	wantChosen(t, s, 2, "b")
	for i := 0; i < NumNodes; i++ {
		wantApplied(t, s, i, []string{"a", "b"})
	}
	checkNoViolations(t, s)
}

// ---- minority old leader: its value may be overwritten, its stale
// ---- ballots must be rejected afterwards ----

func TestMinorityOldLeader(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "old")
	// The old value reaches only a minority (nodes 0 and 1).
	deliverWhere(s, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && (e.Msg.To == 0 || e.Msg.To == 1)
	})
	// Keep one stale Accept for node 3 to check old-ballot rejection later.
	stale := -1
	for _, e := range s.Pending() {
		if e.Msg.Kind == MsgAccept && e.Msg.To == 3 {
			stale = e.ID
		}
	}
	if stale < 0 {
		t.Fatal("no stale Accept found")
	}
	dropWhere(s, func(e Envelope) bool { return e.ID != stale })

	s.Tick(3)
	s.Crash(0)
	s.Tick(4)
	s.Campaign(2)
	// New leader's quorum is {2,3,4}: it never sees the minority value.
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgPrepare && e.Msg.To >= 2 })
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgPromise && e.Msg.To == 2 })
	dropWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgPrepare })

	s.Tick(5)
	s.Propose(2, "new")
	s.DeliverAll() // also delivers the stale old-ballot Accept to node 3

	wantChosen(t, s, 1, "new")
	if e, ok := s.Node(3).Accepted(1); !ok || e.Value != "new" {
		t.Fatalf("node 3 accepted=%v, want slot 1 = new (stale Accept must be rejected)", e)
	}
	for _, i := range []int{1, 2, 3, 4} {
		wantApplied(t, s, i, []string{"new"})
	}
	checkNoViolations(t, s)
}

// ---- chosen value survives even when the Chosen notification is lost ----

func TestChosenSurvivesLostNotification(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "v1")
	// Majority 0,1,2 accepts; only the leader counts the Accepted messages.
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgAccept && e.Msg.To <= 2 })
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgAccepted && e.Msg.To == 0 })
	// The Chosen notifications (and everything else) are lost.
	s.DropAll()

	wantApplied(t, s, 0, []string{"v1"})
	for i := 1; i < NumNodes; i++ {
		wantApplied(t, s, i, nil)
	}

	s.Tick(3)
	s.Crash(0)
	s.Tick(4)
	s.Campaign(1)
	s.DeliverAll()

	// The new leader must discover and re-propose the chosen value.
	wantChosen(t, s, 1, "v1")
	for i := 1; i < NumNodes; i++ {
		wantApplied(t, s, i, []string{"v1"})
	}
	checkNoViolations(t, s)
}

// ---- a value behind a hole: hole filled with no-op, value preserved ----

func TestValueAfterHole(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "first")  // slot 1
	s.Propose(0, "second") // slot 2
	// Only slot 2 reaches a majority; slot 1 remains a hole.
	deliverWhere(s, func(e Envelope) bool {
		return e.Msg.Kind == MsgAccept && e.Msg.Slot == 2 && e.Msg.To <= 2
	})
	s.DropAll()
	s.Tick(3)
	s.Crash(0)
	s.Tick(4)
	s.Campaign(3)
	s.DeliverAll()

	wantChosen(t, s, 1, NoOp)
	wantChosen(t, s, 2, "second")
	for i := 1; i < NumNodes; i++ {
		wantApplied(t, s, i, []string{NoOp, "second"})
	}
	checkNoViolations(t, s)
}

// ---- new leader crashes repeatedly during recovery; durable acceptor
// ---- state survives restarts and the value is still preserved ----

func TestRepeatedRecovery(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "x")
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgAccept && e.Msg.To <= 2 })
	s.DropAll()
	s.Tick(3)
	s.Crash(0)

	// Node 1 starts recovery three times and crashes before finishing phase 1.
	for round := 0; round < 3; round++ {
		s.Tick(10 + round*2)
		s.Campaign(1)
		deliverWhere(s, func(e Envelope) bool {
			return e.Msg.Kind == MsgPrepare && (e.Msg.To == 1 || e.Msg.To == 2)
		})
		deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgPromise && e.Msg.To == 1 })
		s.DropAll()
		s.Crash(1)
		s.Restart(1)
	}

	// Restart preserves the accepted value on node 2 as well.
	s.Crash(2)
	s.Restart(2)
	if e, ok := s.Node(2).Accepted(1); !ok || e.Value != "x" {
		t.Fatalf("node 2 lost accepted value across restart: %v", e)
	}

	s.Tick(20)
	s.Campaign(1)
	s.DeliverAll()

	wantChosen(t, s, 1, "x")
	for i := 1; i < NumNodes; i++ {
		wantApplied(t, s, i, []string{"x"})
	}
	checkNoViolations(t, s)
}

// ---- restart preserves promise/accepted; old-ballot messages rejected ----

func TestRestartRejectsOldBallot(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DeliverAll()
	s.Tick(2)
	s.Propose(0, "persisted")
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgAccept && e.Msg.To == 1 })
	s.DropAll()
	s.Tick(3)
	s.Campaign(0) // ballot (2,0); slot 1 gets chosen as "persisted"
	s.DeliverAll()

	s.Tick(4)
	s.Crash(1)
	s.Restart(1)
	if mp := s.Node(1).MaxPromise(); mp != (Ballot{Round: 2, Node: 0}) {
		t.Fatalf("node 1 maxPromise=%s after restart, want (2,0)", mp)
	}
	if e, ok := s.Node(1).Accepted(1); !ok || e.Value != "persisted" {
		t.Fatalf("node 1 accepted=%v after restart, want slot 1 = persisted", e)
	}

	// Stale-ballot traffic injected after the restart must be rejected.
	s.Inject(Message{Kind: MsgPrepare, From: 4, To: 1, Ballot: Ballot{Round: 1, Node: 0}})
	s.Inject(Message{Kind: MsgAccept, From: 4, To: 1, Ballot: Ballot{Round: 1, Node: 0}, Slot: 1, Value: "stale"})
	s.DeliverAll()
	if n := len(s.Pending()); n != 0 {
		t.Fatalf("old-ballot messages got %d responses, want 0", n)
	}
	if e, _ := s.Node(1).Accepted(1); e.Value != "persisted" {
		t.Fatalf("node 1 accepted=%v, want slot 1 still = persisted", e)
	}
	checkNoViolations(t, s)
}

// ---- persistence failure: a node must not answer without persisting ----

func TestPersistenceFailure(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(0)
	s.DiskFail(3)
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgPrepare && e.Msg.To == 3 })
	if s.Node(3).Up() {
		t.Fatal("node 3 should crash when its promise cannot be persisted")
	}
	for _, e := range s.Pending() {
		if e.Msg.From == 3 {
			t.Fatalf("node 3 sent %s without persisting its promise", e.Msg)
		}
	}
	s.DeliverAll() // phase 1 completes with the remaining 4 promises

	s.Tick(2)
	s.Propose(0, "v")
	s.DiskFail(2)
	deliverWhere(s, func(e Envelope) bool { return e.Msg.Kind == MsgAccept && e.Msg.To == 2 })
	if s.Node(2).Up() {
		t.Fatal("node 2 should crash when its accept cannot be persisted")
	}
	for _, e := range s.Pending() {
		if e.Msg.From == 2 {
			t.Fatalf("node 2 sent %s without persisting its accept", e.Msg)
		}
	}

	s.DiskHeal(2)
	s.Restart(2)
	s.DiskHeal(3)
	s.Restart(3)
	s.DeliverAll()

	wantChosen(t, s, 1, "v")
	for i := 0; i < NumNodes; i++ {
		wantApplied(t, s, i, []string{"v"})
	}
	checkNoViolations(t, s)
}

// ---- liveness: stable majority + fair delivery makes progress, and a new
// ---- leader continues at the next free slots ----

func TestLivenessStableMajority(t *testing.T) {
	s := NewSim()
	s.Tick(1)
	s.Campaign(2)
	s.DeliverAll()
	for i := 0; i < 5; i++ {
		s.Propose(2, fmt.Sprintf("cmd%d", i))
	}
	s.DeliverAll()
	for n := 0; n < NumNodes; n++ {
		wantApplied(t, s, n, []string{"cmd0", "cmd1", "cmd2", "cmd3", "cmd4"})
	}

	s.Tick(2)
	s.Crash(2)
	s.Tick(3)
	s.Campaign(4)
	s.DeliverAll()
	s.Tick(4)
	s.Propose(4, "cmd5")
	s.DeliverAll()

	wantChosen(t, s, 6, "cmd5")
	for n := 0; n < NumNodes; n++ {
		if n == 2 {
			continue
		}
		if got := len(s.Node(n).Applied()); got != 6 {
			t.Fatalf("node %d applied %d slots, want 6", n, got)
		}
	}
	// The restarted old leader reloads its durable chosen prefix.
	s.Restart(2)
	if got := len(s.Node(2).Applied()); got != 5 {
		t.Fatalf("restarted node 2 applied %d slots, want durable 5", got)
	}
	checkNoViolations(t, s)
}

// ---- safety under loss, duplication, reordering, crashes, restarts and
// ---- persistence faults: no slot may ever be chosen with two values ----

func TestChaosSafety(t *testing.T) {
	for seed := int64(0); seed < 25; seed++ {
		r := rand.New(rand.NewSource(seed))
		s := NewSim()
		proposals := 0
		for tick := 1; tick <= 400; tick++ {
			s.Tick(tick)
			pending := s.Pending()
			switch r.Intn(12) {
			case 0, 1:
				s.Campaign(r.Intn(NumNodes))
			case 2, 3:
				s.Propose(r.Intn(NumNodes), fmt.Sprintf("v%d", proposals))
				proposals++
			case 4, 5, 6:
				if len(pending) > 0 {
					s.Deliver(pending[r.Intn(len(pending))].ID)
				}
			case 7:
				if len(pending) > 0 {
					s.Drop(pending[r.Intn(len(pending))].ID)
				}
			case 8:
				if len(pending) > 0 {
					s.Duplicate(pending[r.Intn(len(pending))].ID)
				}
			case 9:
				s.Crash(r.Intn(NumNodes))
			case 10:
				s.Restart(r.Intn(NumNodes))
			case 11:
				if r.Intn(2) == 0 {
					s.DiskFail(r.Intn(NumNodes))
				} else {
					s.DiskHeal(r.Intn(NumNodes))
				}
			}
		}
		if vs := s.Checker().Violations(); len(vs) > 0 {
			t.Fatalf("seed %d: safety violations: %v\n%s", seed, vs, s.Report())
		}
	}
}

// ---- replay scripts ----

func TestReplayScript(t *testing.T) {
	good := `
# comment lines are ignored
tick 1
campaign 0
tick 2
deliver-all
tick 3
propose 0 hello
tick 4
deliver-all
expect-chosen 1 hello
expect-applied 3 1
`
	if err := RunScript(NewSim(), good); err != nil {
		t.Fatalf("good script failed: %v", err)
	}

	bad := `
tick 1
campaign 0
tick 2
deliver-all
tick 3
propose 0 hello
tick 4
deliver-all
expect-chosen 1 goodbye
`
	if err := RunScript(NewSim(), bad); err == nil {
		t.Fatal("script with wrong expectation should fail")
	}
}

func TestExampleScripts(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "examples", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no example scripts found: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := RunScript(NewSim(), string(data)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}
