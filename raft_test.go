package raft

import "testing"

// newCluster builds the 3-node cluster used by the acceptance scenario:
// node 3 times out first (term-1 leader), then node 1, then node 2.
func newCluster() *Sim {
	s := NewSim([]int{1, 2, 3})
	s.Nodes[1].SetTimeouts(10, 3)
	s.Nodes[2].SetTimeouts(12, 3)
	s.Nodes[3].SetTimeouts(8, 3)
	return s
}

// tickN advances the clock while asserting global invariants:
// terms never decrease and committed prefixes never diverge.
func tickN(t *testing.T, s *Sim, n int, maxTerm map[int]int) {
	t.Helper()
	for i := 0; i < n; i++ {
		s.Tick()
		for _, id := range []int{1, 2, 3} {
			node := s.Nodes[id]
			if node.Term < maxTerm[id] {
				t.Fatalf("tick %d: node %d term went backwards: %d -> %d",
					s.Now, id, maxTerm[id], node.Term)
			}
			maxTerm[id] = node.Term
		}
		assertCommittedConsistent(t, s)
	}
}

// assertCommittedConsistent checks the log-matching safety property:
// any two nodes agree on every entry up to their common commit index.
func assertCommittedConsistent(t *testing.T, s *Sim) {
	t.Helper()
	ids := []int{1, 2, 3}
	for _, a := range ids {
		for _, b := range ids {
			na, nb := s.Nodes[a], s.Nodes[b]
			common := min(na.CommitIndex, nb.CommitIndex)
			for i := 1; i <= common; i++ {
				if na.Log[i] != nb.Log[i] {
					t.Fatalf("committed entries diverge at %d: node%d=%+v node%d=%+v",
						i, a, na.Log[i], b, nb.Log[i])
				}
			}
		}
	}
}

func logData(n *Node) []string {
	out := make([]string, 0, n.LastIndex())
	for _, e := range n.Log[1:] {
		out = append(out, e.Data)
	}
	return out
}

func equalData(a, b []string) bool {
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

// TestViewChangeScenario is the acceptance scenario:
//  1. term 1: 3-node cluster commits 3 entries (node 3 leads);
//  2. node 3 is partitioned; the majority (1,2) moves to term 2 and
//     commits 4 more entries, while the old leader keeps 2 uncommitted
//     conflicting entries;
//  3. node 3 is healed and a higher-term heartbeat is injected: it must
//     roll back its uncommitted conflict, adopt the leader's log, and
//     keep every committed entry.
func TestViewChangeScenario(t *testing.T) {
	s := newCluster()
	maxTerm := map[int]int{1: 0, 2: 0, 3: 0}

	// --- Phase 1: term 1, commit 3 entries on all 3 nodes. ---
	tickN(t, s, 10, maxTerm)
	if got := s.Nodes[3].State; got != Leader || s.Nodes[3].Term != 1 {
		t.Fatalf("phase 1: node 3 should be leader of term 1, got %v term %d", got, s.Nodes[3].Term)
	}
	for _, d := range []string{"e1", "e2", "e3"} {
		if !s.Propose(3, d) {
			t.Fatalf("phase 1: leader rejected proposal %q", d)
		}
	}
	for _, id := range []int{1, 2, 3} {
		if n := s.Nodes[id]; n.CommitIndex != 3 {
			t.Fatalf("phase 1: node %d commitIndex=%d, want 3", id, n.CommitIndex)
		}
		if !equalData(logData(s.Nodes[id]), []string{"e1", "e2", "e3"}) {
			t.Fatalf("phase 1: node %d log=%v", id, logData(s.Nodes[id]))
		}
	}

	// --- Phase 2: partition node 3; majority (1,2) advances to term 2. ---
	s.Isolate(3)
	// The old, partitioned leader accepts two more proposals; they can
	// never reach a quorum and stay uncommitted on node 3 only.
	s.Propose(3, "c4")
	s.Propose(3, "c5")
	tickN(t, s, 25, maxTerm)

	if n := s.Nodes[1]; n.State != Leader || n.Term != 2 {
		t.Fatalf("phase 2: node 1 should be leader of term 2, got %v term %d", n.State, n.Term)
	}
	if n := s.Nodes[3]; n.Term != 1 {
		t.Fatalf("phase 2: pre-vote should stop partitioned node 3 from inflating its term, got term %d", n.Term)
	}
	if n := s.Nodes[3]; n.CommitIndex != 3 {
		t.Fatalf("phase 2: partitioned node 3 commitIndex=%d, want 3", n.CommitIndex)
	}
	for _, d := range []string{"a4", "a5", "a6", "a7"} {
		if !s.Propose(1, d) {
			t.Fatalf("phase 2: leader rejected proposal %q", d)
		}
	}
	for _, id := range []int{1, 2} {
		if n := s.Nodes[id]; n.CommitIndex != 7 {
			t.Fatalf("phase 2: node %d commitIndex=%d, want 7", id, n.CommitIndex)
		}
	}
	if got, want := logData(s.Nodes[3]), []string{"e1", "e2", "e3", "c4", "c5"}; !equalData(got, want) {
		t.Fatalf("phase 2: node 3 log=%v, want %v (uncommitted tail)", got, want)
	}

	// --- Phase 3: heal node 3, inject a higher-term heartbeat. ---
	s.Heal(3)
	hb := s.Nodes[1].AppendMessage(3)
	if hb.Term <= s.Nodes[3].Term {
		t.Fatalf("phase 3: injected heartbeat term %d not higher than node 3 term %d", hb.Term, s.Nodes[3].Term)
	}
	s.Send(hb)
	tickN(t, s, 10, maxTerm)

	c := s.Nodes[3]
	if c.State != Follower || c.Term != 2 {
		t.Fatalf("phase 3: node 3 should be follower of term 2, got %v term %d", c.State, c.Term)
	}
	if c.CommitIndex != 7 {
		t.Fatalf("phase 3: node 3 commitIndex=%d, want 7", c.CommitIndex)
	}
	want := []string{"e1", "e2", "e3", "a4", "a5", "a6", "a7"}
	if got := logData(c); !equalData(got, want) {
		t.Fatalf("phase 3: node 3 log=%v, want %v (conflicting c4/c5 overwritten)", got, want)
	}
	// Committed prefix survived the rollback.
	for i, d := range []string{"e1", "e2", "e3"} {
		if c.Log[i+1].Data != d {
			t.Fatalf("phase 3: committed entry %d lost: log=%v", i+1, logData(c))
		}
	}
	// Whole cluster converged on one identical log.
	for _, id := range []int{1, 2} {
		if got := logData(s.Nodes[id]); !equalData(got, want) {
			t.Fatalf("phase 3: node %d log=%v, want %v", id, got, want)
		}
	}
}

// TestPreVoteKeepsTermStable: a partitioned follower must not raise its
// term (pre-vote cannot reach a quorum), and the remaining majority must
// keep committing without it.
func TestPreVoteKeepsTermStable(t *testing.T) {
	s := newCluster()
	maxTerm := map[int]int{1: 0, 2: 0, 3: 0}
	tickN(t, s, 10, maxTerm)
	if s.Nodes[3].State != Leader {
		t.Fatalf("expected node 3 to lead term 1")
	}

	s.Isolate(1)
	tickN(t, s, 60, maxTerm) // many election timeouts pass for node 1

	if n := s.Nodes[1]; n.Term != 1 {
		t.Fatalf("partitioned node 1 inflated its term to %d despite pre-vote", n.Term)
	}
	if n := s.Nodes[3]; n.State != Leader || n.Term != 1 {
		t.Fatalf("healthy leader disturbed: %v term %d", n.State, n.Term)
	}
	if !s.Propose(3, "x1") || s.Nodes[3].CommitIndex != 1 {
		t.Fatalf("majority of 2 failed to commit, commitIndex=%d", s.Nodes[3].CommitIndex)
	}

	s.Heal(1)
	tickN(t, s, 10, maxTerm)
	if n := s.Nodes[1]; n.CommitIndex != 1 || !equalData(logData(n), []string{"x1"}) {
		t.Fatalf("healed node 1 did not catch up: commit=%d log=%v", n.CommitIndex, logData(n))
	}
}

// TestAppendConsistencyCheckAndRetry exercises the prevLogIndex/prevLogTerm
// check and the leader's back-off retry until the logs converge.
func TestAppendConsistencyCheckAndRetry(t *testing.T) {
	ids := []int{1, 2}
	leader := NewNode(1, ids, 10, 3)
	leader.State = Leader
	leader.Term = 2
	leader.Log = append(leader.Log,
		LogEntry{Term: 1, Data: "a"},
		LogEntry{Term: 2, Data: "b"},
		LogEntry{Term: 2, Data: "c"})
	leader.next[2] = 4 // optimistic: assumes follower has everything

	follower := NewNode(2, ids, 10, 3)
	follower.Term = 2
	follower.Log = append(follower.Log, LogEntry{Term: 1, Data: "a"})

	// First attempt must fail the consistency check (follower too short).
	m := leader.AppendMessage(2)
	follower.Step(m)
	resp := follower.Drain()[0]
	if !resp.Reject {
		t.Fatalf("expected reject for prevLogIndex=%d on short log", m.PrevLogIndex)
	}

	// Leader backs off and retries until the follower converges.
	for i := 0; i < 5 && follower.LastIndex() < leader.LastIndex(); i++ {
		leader.Step(resp)
		leader.Drain()
		m = leader.AppendMessage(2)
		follower.Step(m)
		resp = follower.Drain()[0]
	}
	if resp.Reject {
		t.Fatalf("follower still rejecting after retries")
	}
	if !equalData(logData(follower), []string{"a", "b", "c"}) {
		t.Fatalf("follower log=%v, want [a b c]", logData(follower))
	}
	if resp.MatchIndex != 3 {
		t.Fatalf("MatchIndex=%d, want 3", resp.MatchIndex)
	}
}

// TestTruncateCommittedEntryPanics verifies the safety guard: conflicting
// data may never overwrite a committed entry.
func TestTruncateCommittedEntryPanics(t *testing.T) {
	n := NewNode(2, []int{1, 2, 3}, 10, 3)
	n.Log = append(n.Log, LogEntry{Term: 1, Data: "a"}, LogEntry{Term: 1, Data: "b"})
	n.CommitIndex = 2

	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic when truncating a committed entry")
		}
	}()
	n.appendFrom(1, []LogEntry{{Term: 2, Data: "x"}}) // conflicts at committed index 2
}

// TestOneVotePerTerm: a node grants at most one vote per term.
func TestOneVotePerTerm(t *testing.T) {
	n := NewNode(1, []int{1, 2, 3}, 10, 3)
	n.Step(Message{Type: MsgVote, From: 2, To: 1, Term: 1})
	n.Step(Message{Type: MsgVote, From: 3, To: 1, Term: 1})
	out := n.Drain()
	if len(out) != 2 || !out[0].Grant || out[1].Grant {
		t.Fatalf("expected grant then reject, got %+v", out)
	}
	if n.vote != 2 {
		t.Fatalf("recorded vote=%d, want 2", n.vote)
	}
}
