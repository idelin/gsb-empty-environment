package replsim

import (
	"fmt"
	"reflect"
	"testing"
)

// buildScript is a message script mixing terms, duplicate deliveries, a
// missing predecessor, a conflicting-suffix truncation, a committed-prefix
// attack, a corruption attempt, and commit advancement.
func buildScript() []AppendRequest {
	return []AppendRequest{
		// Term 1: three entries, delivered twice (at-least-once delivery).
		{Term: 1, Entries: entries(1, "a", "b", "c")},
		{Term: 1, Entries: entries(1, "a", "b", "c")},
		// Heartbeat advancing commit to 2.
		{Term: 1, PrevIndex: 3, PrevTerm: 1, LeaderCommit: 2},
		// Gap: predecessor at index 5 does not exist yet.
		{Term: 2, PrevIndex: 5, PrevTerm: 2, Entries: entries(2, "x")},
		// Term 2: conflicting suffix "c" is truncated, replaced by C, D.
		{Term: 2, PrevIndex: 2, PrevTerm: 1, Entries: entries(2, "C", "D"), LeaderCommit: 2},
		// Duplicate of the truncating append must be idempotent.
		{Term: 2, PrevIndex: 2, PrevTerm: 1, Entries: entries(2, "C", "D"), LeaderCommit: 2},
		// Stale term is rejected.
		{Term: 1, PrevIndex: 4, PrevTerm: 2, Entries: entries(1, "q")},
		// Same index and term, different content: corruption.
		{Term: 3, PrevIndex: 3, PrevTerm: 2, Entries: []Entry{{Term: 2, Data: "D-tampered"}}},
		// Attempt to truncate the committed prefix: rejected.
		{Term: 3, PrevIndex: 0, Entries: entries(3, "A2")},
		// Term 3 extends the log and commits everything.
		{Term: 3, PrevIndex: 4, PrevTerm: 2, Entries: entries(3, "E"), LeaderCommit: 5},
		// Commit beyond last index is capped.
		{Term: 3, PrevIndex: 5, PrevTerm: 3, LeaderCommit: 100},
	}
}

func TestScriptBaseline(t *testing.T) {
	s := NewMemStorage()
	responses, err := Replay(s, buildScript())
	if err != nil {
		t.Fatal(err)
	}
	wantReasons := []RejectReason{
		RejectNone, RejectNone, RejectNone,
		RejectMissingPredecessor,
		RejectNone, RejectNone,
		RejectStaleTerm,
		RejectCorruption,
		RejectCommittedConflict,
		RejectNone, RejectNone,
	}
	if len(responses) != len(wantReasons) {
		t.Fatalf("got %d responses", len(responses))
	}
	for i, resp := range responses {
		if resp.Reason != wantReasons[i] {
			t.Errorf("response %d reason = %q, want %q", i, resp.Reason, wantReasons[i])
		}
	}
	durable, _ := s.Load()
	wantLog := []Entry{{1, "a"}, {1, "b"}, {2, "C"}, {2, "D"}, {3, "E"}}
	if !reflect.DeepEqual(durable.Log, wantLog) {
		t.Fatalf("durable log = %v, want %v", durable.Log, wantLog)
	}
	if durable.CommitIndex != 5 || durable.CurrentTerm != 3 {
		t.Fatalf("durable = %+v", durable)
	}
}

// ackChecker tracks every acknowledged append and asserts the simulator's
// safety invariants after each event and after each crash recovery.
type ackChecker struct {
	t            *testing.T
	ackedCommit  uint64
	ackedPrefix  []Entry
	lastCommit   uint64
	commitSynced bool
}

func (c *ackChecker) onResponse(node *Node, storage *MemStorage, resp AppendResponse) {
	if !resp.Success {
		return
	}
	durable, err := storage.Load()
	if err != nil {
		c.t.Fatalf("load: %v", err)
	}
	// Invariant 1: an ack is never emitted before persistence, so the
	// durable state must already contain everything the ack implies.
	if durable.CommitIndex != node.CommitIndex() {
		c.t.Fatalf("ack before persist: durable commit %d, node commit %d",
			durable.CommitIndex, node.CommitIndex())
	}
	if !reflect.DeepEqual(durable.Log, node.Log()) {
		c.t.Fatalf("ack before persist: durable log %v, node log %v", durable.Log, node.Log())
	}
	// Invariant 2: the committed prefix is stable once acknowledged.
	if node.CommitIndex() > c.ackedCommit {
		if c.ackedCommit > 0 && !reflect.DeepEqual(node.Log()[:c.ackedCommit], c.ackedPrefix) {
			c.t.Fatalf("committed prefix changed: was %v, now %v",
				c.ackedPrefix, node.Log()[:c.ackedCommit])
		}
		c.ackedCommit = node.CommitIndex()
		c.ackedPrefix = append([]Entry(nil), node.Log()[:c.ackedCommit]...)
	}
	// Invariant 3: commit index never retreats within a node lifetime.
	if node.CommitIndex() < c.lastCommit {
		c.t.Fatalf("commit index retreated: %d -> %d", c.lastCommit, node.CommitIndex())
	}
	c.lastCommit = node.CommitIndex()
}

func (c *ackChecker) onRecover(node *Node) {
	// Invariant 4: a crash never loses acknowledged committed entries.
	if node.CommitIndex() < c.ackedCommit {
		c.t.Fatalf("recovered commit %d behind acked commit %d",
			node.CommitIndex(), c.ackedCommit)
	}
	if c.ackedCommit > 0 && !reflect.DeepEqual(node.Log()[:c.ackedCommit], c.ackedPrefix) {
		c.t.Fatalf("recovered log %v lost acked committed prefix %v",
			node.Log(), c.ackedPrefix)
	}
	c.lastCommit = node.CommitIndex()
}

// TestCrashCutPoints replays the script once per crash cut-point: the
// (k+1)-th persist is lost to a crash, the node restarts from durable
// state, the in-flight request is redelivered, and the remaining events
// are replayed. Every cut-point must preserve all safety invariants.
func TestCrashCutPoints(t *testing.T) {
	script := buildScript()

	baseline := NewMemStorage()
	if _, err := Replay(baseline, script); err != nil {
		t.Fatal(err)
	}
	totalPersists := baseline.PersistCount()

	for cut := 0; cut <= totalPersists; cut++ {
		cut := cut
		t.Run(fmt.Sprintf("cut-%d-of-%d", cut, totalPersists), func(t *testing.T) {
			storage := NewMemStorage()
			storage.CrashAfter = cut
			node, err := NewNode(storage)
			if err != nil {
				t.Fatal(err)
			}
			checker := &ackChecker{t: t}
			for i := 0; i < len(script); i++ {
				resp := node.HandleAppend(script[i])
				if storage.Crashed() {
					// The in-flight persist was lost; restart from
					// durable state and redeliver the request.
					storage.Recover()
					node, err = NewNode(storage)
					if err != nil {
						t.Fatal(err)
					}
					checker.onRecover(node)
					i--
					continue
				}
				checker.onResponse(node, storage, resp)
			}
			checker.onRecover(node)
		})
	}
}
