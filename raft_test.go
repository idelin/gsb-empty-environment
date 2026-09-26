package raftsim

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// --- helpers ---------------------------------------------------------------

func cmdsOf(entries []Entry) []string {
	cmds := make([]string, len(entries))
	for i, e := range entries {
		cmds[i] = e.Cmd
	}
	return cmds
}

func containsInOrder(haystack []string, needle ...string) bool {
	pos := 0
	for _, h := range haystack {
		if pos < len(needle) && h == needle[pos] {
			pos++
		}
	}
	return pos == len(needle)
}

func contains(haystack []string, want string) bool {
	for _, h := range haystack {
		if h == want {
			return true
		}
	}
	return false
}

// assertCommittedConsistency panics if any two nodes disagree on a committed
// (applied) entry. Installed as Sim.Check it verifies the safety invariant
// after every single event, not just at the end of a run.
func assertCommittedConsistency(s *Sim) {
	for i := 0; i < len(s.Nodes); i++ {
		for j := i + 1; j < len(s.Nodes); j++ {
			a, b := s.Nodes[i].Applied, s.Nodes[j].Applied
			for k := 0; k < len(a) && k < len(b); k++ {
				if a[k] != b[k] {
					panic(fmt.Sprintf(
						"SAFETY VIOLATION at t=%d: nodes %d and %d diverge at committed index %d\n  node %d: %v\n  node %d: %v",
						s.Now, i, j, k+1, i, cmdsOf(a), j, cmdsOf(b)))
				}
			}
		}
	}
}

func allAppliedEqual(t *testing.T, s *Sim) []string {
	t.Helper()
	want := cmdsOf(s.Nodes[0].Applied)
	for _, nd := range s.Nodes[1:] {
		if got := cmdsOf(nd.Applied); !reflect.DeepEqual(got, want) {
			t.Fatalf("committed prefixes differ: node 0 has %v, node %d has %v", want, nd.ID, got)
		}
	}
	return want
}

// --- safety: append semantics (node level) ---------------------------------

// 重复追加幂等: delivering the same AppendEntries twice must not change the
// log, and re-delivering the commit point must not re-apply entries.
func TestDuplicateAppendIsIdempotent(t *testing.T) {
	n := NewNode(0, 3, rand.New(rand.NewSource(1)))
	m := Message{From: 1, To: 0, Type: MsgAppend, Term: 1,
		PrevIndex: 0, PrevTerm: 0,
		Entries: []Entry{{Term: 1, Cmd: "a"}, {Term: 1, Cmd: "b"}}}

	r1 := n.Step(m)[0]
	if !r1.Success || r1.MatchIndex != 2 {
		t.Fatalf("first append: success=%v match=%d, want success match=2", r1.Success, r1.MatchIndex)
	}
	before := append([]Entry(nil), n.Log...)
	r2 := n.Step(m)[0]
	if !r2.Success || !reflect.DeepEqual(before, n.Log) {
		t.Fatalf("duplicate append mutated log: %v -> %v", before, n.Log)
	}

	commit := m
	commit.LeaderCommit = 2
	n.Step(commit)
	applied := append([]Entry(nil), n.Applied...)
	n.Step(commit) // duplicate commit notification
	if !reflect.DeepEqual(applied, n.Applied) {
		t.Fatalf("duplicate commit mutated applied: %v -> %v", applied, n.Applied)
	}
	if got := cmdsOf(n.Applied); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("applied = %v, want [a b]", got)
	}
}

// 前缀不匹配不能直接接尾: a follower whose log conflicts at prevLogIndex
// rejects the append, keeps its log untouched, and returns a catch-up hint
// naming the conflicting term and its first index.
func TestPrefixMismatchRejectedWithHint(t *testing.T) {
	n := NewNode(0, 3, rand.New(rand.NewSource(1)))
	n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 1,
		Entries: []Entry{{Term: 1, Cmd: "a"}, {Term: 1, Cmd: "b"}, {Term: 1, Cmd: "c"}}})

	// Leader believes prev=(3, term 2); follower has term 1 there.
	resp := n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 2,
		PrevIndex: 3, PrevTerm: 2, Entries: []Entry{{Term: 2, Cmd: "x"}}})[0]
	if resp.Success {
		t.Fatal("mismatched prevTerm must be rejected")
	}
	if resp.ConflictTerm != 1 || resp.ConflictIndex != 1 {
		t.Fatalf("hint = (term %d, index %d), want (1, 1)", resp.ConflictTerm, resp.ConflictIndex)
	}
	if got := cmdsOf(n.Log); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("rejected append changed log: %v", got)
	}

	// Log too short: follower has 3 entries, leader points past the end.
	resp = n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 2,
		PrevIndex: 5, PrevTerm: 2, Entries: []Entry{{Term: 2, Cmd: "x"}}})[0]
	if resp.Success || resp.ConflictTerm != -1 || resp.ConflictIndex != 4 {
		t.Fatalf("short-log hint = (ok=%v, term %d, index %d), want (false, -1, 4)",
			resp.Success, resp.ConflictTerm, resp.ConflictIndex)
	}
}

// 冲突删除未提交后缀再追加: once the leader backs up to a matching prefix,
// the uncommitted conflicting suffix is deleted and the new entries append.
func TestConflictTruncatesUncommittedSuffix(t *testing.T) {
	n := NewNode(0, 3, rand.New(rand.NewSource(1)))
	n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 1,
		Entries: []Entry{{Term: 1, Cmd: "a"}, {Term: 1, Cmd: "b"}, {Term: 1, Cmd: "c"}}})

	resp := n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 2,
		PrevIndex: 1, PrevTerm: 1,
		Entries: []Entry{{Term: 2, Cmd: "x"}, {Term: 2, Cmd: "y"}}})[0]
	if !resp.Success || resp.MatchIndex != 3 {
		t.Fatalf("append after backup: success=%v match=%d", resp.Success, resp.MatchIndex)
	}
	want := []Entry{{1, "a"}, {2, "x"}, {2, "y"}}
	if !reflect.DeepEqual(n.Log, want) {
		t.Fatalf("log = %v, want %v", n.Log, want)
	}
}

// 禁止覆盖已提交条目: any attempt to overwrite a committed entry is a
// safety violation and must be stopped, even by a buggy/Byzantine sender.
func TestCommittedEntryOverwritePanics(t *testing.T) {
	n := NewNode(0, 3, rand.New(rand.NewSource(1)))
	n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 1,
		Entries: []Entry{{Term: 1, Cmd: "a"}}, LeaderCommit: 1})
	if n.CommitIndex() != 1 {
		t.Fatalf("commitIndex = %d, want 1", n.CommitIndex())
	}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "SAFETY") {
			t.Fatalf("expected safety panic, got %v", r)
		}
		if got := cmdsOf(n.Log); !reflect.DeepEqual(got, []string{"a"}) {
			t.Fatalf("committed log mutated: %v", got)
		}
	}()
	n.Step(Message{From: 1, To: 0, Type: MsgAppend, Term: 2,
		PrevIndex: 0, PrevTerm: 0, Entries: []Entry{{Term: 2, Cmd: "evil"}}})
}

// --- liveness: election + replication ---------------------------------------

// A 3-node cluster elects exactly one leader and commits client commands on
// every node, in the same order.
func TestElectionAndCommitLiveness(t *testing.T) {
	s := NewSim(3, 42)
	s.Check = func() { assertCommittedConsistency(s) }
	s.Run(60)

	leaders := s.LeaderIDs()
	if len(leaders) != 1 {
		t.Fatalf("leaders = %v, want exactly 1", leaders)
	}
	for _, cmd := range []string{"x", "y"} {
		if !s.Propose(cmd) {
			t.Fatalf("no leader to propose %q", cmd)
		}
		s.Run(30)
	}
	s.Run(30)

	final := allAppliedEqual(t, s)
	if !containsInOrder(final, "x", "y") {
		t.Fatalf("committed prefix %v does not contain x,y in order", final)
	}
	t.Logf("committed on all nodes: %v", final)
}

// --- safety + liveness under faults -----------------------------------------

// 短日志与长日志恢复后已提交前缀一致: a 5-node cluster splits into a
// minority (old leader, short divergent uncommitted log) and a majority
// (new leader, longer committed log). After healing, every node must
// converge to the same committed prefix, and the minority's uncommitted
// entries must be discarded rather than committed.
func TestShortAndLongLogsConvergeAfterHeal(t *testing.T) {
	s := NewSim(5, 7)
	s.Check = func() { assertCommittedConsistency(s) }
	s.Run(60)

	leaders := s.LeaderIDs()
	if len(leaders) != 1 {
		t.Fatalf("leaders = %v, want 1", leaders)
	}
	oldLeader := leaders[0]
	if !s.Propose("a") {
		t.Fatal("no leader")
	}
	s.Run(60)
	for _, nd := range s.Nodes {
		if !contains(cmdsOf(nd.Applied), "a") {
			t.Fatalf("node %d did not commit 'a' before partition: %v", nd.ID, cmdsOf(nd.Applied))
		}
	}

	// Minority: old leader + one follower. Majority: the other three.
	minorityFollower := (oldLeader + 1) % 5
	var majority []int
	for i := 0; i < 5; i++ {
		if i != oldLeader && i != minorityFollower {
			majority = append(majority, i)
		}
	}
	s.Partition([]int{oldLeader, minorityFollower}, majority)

	// Old leader keeps appending in the minority: never committed.
	if !s.ProposeOn(oldLeader, "stale") {
		t.Fatal("old leader should still accept writes in its partition")
	}
	s.Run(80) // majority side elects a new leader

	var newLeader = -1
	for _, id := range s.LeaderIDs() {
		for _, m := range majority {
			if id == m {
				newLeader = id
			}
		}
	}
	if newLeader == -1 {
		t.Fatal("majority side did not elect a leader")
	}
	if !s.ProposeOn(newLeader, "c") || !s.ProposeOn(newLeader, "d") {
		t.Fatal("new leader refused proposals")
	}
	s.Run(80)

	// The majority committed c,d; the minority must not have them.
	for _, id := range majority {
		if got := cmdsOf(s.Nodes[id].Applied); !containsInOrder(got, "a", "c", "d") {
			t.Fatalf("majority node %d applied %v, want a,c,d", id, got)
		}
	}
	if got := cmdsOf(s.Nodes[oldLeader].Applied); contains(got, "c") {
		t.Fatalf("minority leader applied majority entry: %v", got)
	}

	// Heal: the long committed log wins; the short uncommitted suffix is
	// truncated everywhere and all committed prefixes become identical.
	s.Heal()
	s.Run(200)

	final := allAppliedEqual(t, s)
	if !containsInOrder(final, "a", "c", "d") {
		t.Fatalf("final committed prefix %v missing a,c,d", final)
	}
	if contains(final, "stale") {
		t.Fatalf("uncommitted minority entry was committed: %v", final)
	}
	if contains(cmdsOf(s.Nodes[oldLeader].Log), "stale") {
		t.Fatalf("old leader kept divergent suffix: %v", cmdsOf(s.Nodes[oldLeader].Log))
	}
	t.Logf("converged committed prefix on all 5 nodes: %v", final)
}

// Randomized chaos: message loss, crashes, restarts, partitions and
// proposals interleave while the committed-prefix invariant is checked
// after every event (safety). After all faults are repaired the cluster
// must make progress again and agree on a final command (liveness).
func TestRandomizedSafetyAndLiveness(t *testing.T) {
	const nodes = 5
	s := NewSim(nodes, 2024)
	s.LossRate = 0.1
	s.MinDelay, s.MaxDelay = 1, 3
	s.Check = func() { assertCommittedConsistency(s) }

	rng := rand.New(rand.NewSource(99))
	for round := 0; round < 40; round++ {
		s.Run(25)
		switch rng.Intn(6) {
		case 0:
			id := rng.Intn(nodes)
			if s.Alive(id) {
				s.Crash(id)
			}
		case 1:
			s.Restart(rng.Intn(nodes))
		case 2:
			var a, b []int
			for i := 0; i < nodes; i++ {
				if rng.Intn(2) == 0 {
					a = append(a, i)
				} else {
					b = append(b, i)
				}
			}
			s.Heal()
			s.Partition(a, b)
		case 3:
			s.Heal()
		case 4, 5:
			s.Propose(fmt.Sprintf("cmd-%d", round))
		}
	}

	// Repair everything; the cluster must liven up and converge.
	s.Heal()
	for i := 0; i < nodes; i++ {
		s.Restart(i)
	}
	s.Run(300)
	for tries := 0; !s.Propose("final") && tries < 50; tries++ {
		s.Run(10)
	}
	s.Run(300)

	final := allAppliedEqual(t, s)
	if !contains(final, "final") {
		t.Fatalf("cluster made no progress after healing: %v", final)
	}
	t.Logf("converged after chaos, %d committed entries: %v", len(final), final)
}
