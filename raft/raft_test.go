package raft

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// makeNode builds a single-node simulator whose node carries the given log
// entries (after the sentinel) and commitIndex.
func makeNode(logEntries []Entry, commitIndex int) *Node {
	sim := NewSim(1, 1)
	n := sim.nodes[0]
	n.log = append(n.log, logEntries...)
	n.commitIndex = commitIndex
	n.persist()
	return n
}

func ents(term int, cmds ...string) []Entry {
	out := make([]Entry, len(cmds))
	for i, c := range cmds {
		out[i] = Entry{Term: term, Command: c}
	}
	return out
}

func commands(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Command)
	}
	return out
}

// 重复追加幂等: delivering the same AppendEntries repeatedly must not
// change the log.
func TestAppendEntriesIdempotentDuplicates(t *testing.T) {
	n := makeNode(ents(1, "a"), 0)
	req := &AppendEntriesRequest{
		Term: 1, LeaderID: 0,
		PrevLogIndex: 1, PrevLogTerm: 1,
		Entries: ents(1, "b", "c"),
	}
	if r := n.HandleAppendEntries(req); !r.Success || r.MatchIndex != 3 {
		t.Fatalf("first append rejected: %+v", r)
	}
	before := append([]Entry(nil), n.log...)
	for i := 0; i < 3; i++ {
		r := n.HandleAppendEntries(req)
		if !r.Success {
			t.Fatalf("duplicate delivery %d rejected", i)
		}
		if !reflect.DeepEqual(before, n.log) {
			t.Fatalf("duplicate delivery %d mutated the log: %v", i, n.log)
		}
	}
	// A duplicate that also carries a higher LeaderCommit still succeeds and
	// only advances the commit index.
	r := n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 1, LeaderID: 0,
		PrevLogIndex: 1, PrevLogTerm: 1,
		Entries: ents(1, "b", "c"), LeaderCommit: 3,
	})
	if !r.Success || n.commitIndex != 3 || !reflect.DeepEqual(before, n.log) {
		t.Fatalf("commit-carrying duplicate broke idempotency: %+v log=%v", r, n.log)
	}
}

// 前缀不匹配不能直接接尾: a failed PrevLogIndex/PrevLogTerm check rejects
// the request and leaves the log untouched.
func TestPrefixMismatchRejected(t *testing.T) {
	n := makeNode([]Entry{{1, "a"}, {1, "b"}, {2, "c"}}, 0)

	// Wrong term at PrevLogIndex.
	r := n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 3, PrevLogIndex: 3, PrevLogTerm: 1, Entries: ents(3, "d"),
	})
	if r.Success {
		t.Fatal("prefix mismatch must not append at the tail")
	}
	if r.ConflictTerm != 2 || r.ConflictIndex != 3 {
		t.Fatalf("bad catch-up hint: %+v", r)
	}
	if got := len(n.log); got != 4 {
		t.Fatalf("log mutated on rejection, len=%d", got)
	}

	// PrevLogIndex beyond the end of the log.
	r = n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 3, PrevLogIndex: 7, PrevLogTerm: 2, Entries: ents(3, "d"),
	})
	if r.Success {
		t.Fatal("missing prefix must not append at the tail")
	}
	if r.ConflictTerm != 0 || r.ConflictIndex != 4 {
		t.Fatalf("bad short-log hint: %+v", r)
	}
	if got := len(n.log); got != 4 {
		t.Fatalf("log mutated on rejection, len=%d", got)
	}
}

// 禁止覆盖已提交条目: any attempt to overwrite at or below commitIndex is
// refused and the log stays intact; conflicts above commitIndex are fine.
func TestCommittedEntriesCannotBeOverwritten(t *testing.T) {
	n := makeNode([]Entry{{1, "a"}, {1, "b"}, {2, "c"}}, 2)

	r := n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 3, PrevLogIndex: 0, PrevLogTerm: 0, Entries: ents(3, "x"),
	})
	if r.Success || !r.CommittedConflict {
		t.Fatalf("overwrite of committed index 1 not refused: %+v", r)
	}

	r = n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 3, PrevLogIndex: 1, PrevLogTerm: 1, Entries: ents(3, "y"),
	})
	if r.Success || !r.CommittedConflict {
		t.Fatalf("overwrite of committed index 2 not refused: %+v", r)
	}
	if got := commands(n.log[1:]); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("committed entries changed: %v", got)
	}

	// The same request conflicting only above commitIndex succeeds.
	r = n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 3, PrevLogIndex: 2, PrevLogTerm: 1, Entries: ents(3, "z"),
	})
	if !r.Success {
		t.Fatal("uncommitted conflict should be resolved by truncation")
	}
	if got := commands(n.log[1:]); !reflect.DeepEqual(got, []string{"a", "b", "z"}) {
		t.Fatalf("unexpected log after truncation: %v", got)
	}
}

// 冲突删除未提交后缀再追加.
func TestUncommittedSuffixTruncatedBeforeAppend(t *testing.T) {
	n := makeNode([]Entry{{1, "a"}, {2, "b"}, {2, "c"}, {3, "d"}}, 1)
	r := n.HandleAppendEntries(&AppendEntriesRequest{
		Term: 4, PrevLogIndex: 1, PrevLogTerm: 1, Entries: ents(4, "e", "f"),
	})
	if !r.Success {
		t.Fatalf("append after truncation rejected: %+v", r)
	}
	want := []Entry{{0, ""}, {1, "a"}, {4, "e"}, {4, "f"}}
	if !reflect.DeepEqual(want, n.log) {
		t.Fatalf("got %v, want %v", n.log, want)
	}
}

// 返回追赶提示: the follower reports ConflictTerm/ConflictIndex and the
// leader uses them to converge in two rounds instead of stepping back one
// index at a time.
func TestCatchUpHintAcceleratesReplication(t *testing.T) {
	sim := NewSim(2, 1)
	leader, follower := sim.nodes[0], sim.nodes[1]
	leader.currentTerm = 3
	leader.role = Leader
	leader.log = []Entry{{Term: 0}}
	leader.log = append(leader.log,
		ents(1, "e1", "e2", "e3")...)
	leader.log = append(leader.log, ents(2, "e4", "e5")...)
	leader.log = append(leader.log, ents(3, "e6", "e7", "e8", "e9", "e10")...)
	leader.nextIndex = []int{0, 11}
	leader.matchIndex = []int{0, 0}
	follower.log = []Entry{{Term: 0}}
	follower.log = append(follower.log, ents(1, "e1", "e2", "e3")...)

	rounds := 0
	for {
		rounds++
		next := leader.nextIndex[1]
		req := &AppendEntriesRequest{
			Term:         3,
			PrevLogIndex: next - 1,
			PrevLogTerm:  leader.log[next-1].Term,
			Entries:      append([]Entry(nil), leader.log[next:]...),
		}
		reply := follower.HandleAppendEntries(req)
		leader.handleAppendReply(1, 3, req, reply)
		if reply.Success {
			break
		}
		if rounds > 10 {
			t.Fatal("replication did not converge")
		}
	}
	if rounds != 2 {
		t.Fatalf("catch-up hint should converge in 2 rounds, took %d", rounds)
	}
	if !reflect.DeepEqual(leader.log, follower.log) {
		t.Fatalf("logs differ after catch-up:\nleader:   %v\nfollower: %v", leader.log, follower.log)
	}
}

// 选举限制: a candidate whose log is behind must not collect votes.
func TestElectionRestrictionStaleCandidate(t *testing.T) {
	n := makeNode([]Entry{{1, "a"}, {2, "b"}}, 2)
	n.currentTerm = 2
	n.persist()

	// Shorter log, same last term.
	if r := n.HandleRequestVote(&VoteRequest{Term: 3, CandidateID: 1, LastLogIndex: 1, LastLogTerm: 1}); r.Granted {
		t.Fatal("vote granted to candidate with shorter log")
	}
	// Longer log but older last term.
	if r := n.HandleRequestVote(&VoteRequest{Term: 3, CandidateID: 1, LastLogIndex: 9, LastLogTerm: 1}); r.Granted {
		t.Fatal("vote granted to candidate with stale last term")
	}
	// Up-to-date candidate gets the vote, once per term.
	if r := n.HandleRequestVote(&VoteRequest{Term: 3, CandidateID: 1, LastLogIndex: 2, LastLogTerm: 2}); !r.Granted {
		t.Fatal("vote refused for up-to-date candidate")
	}
	if r := n.HandleRequestVote(&VoteRequest{Term: 3, CandidateID: 2, LastLogIndex: 2, LastLogTerm: 2}); r.Granted {
		t.Fatal("second vote granted in the same term")
	}
}

// 短日志与长日志恢复后已提交前缀一致: a node that missed entries (short
// log) and a node carrying a longer divergent uncommitted suffix (long log)
// both converge to the leader's committed prefix after crash/recovery.
func TestShortAndLongLogRecoveryCommittedPrefix(t *testing.T) {
	sim := NewSim(3, 7)
	sim.Run(800)
	lid := sim.Leader()
	if lid < 0 {
		t.Fatal("no leader elected")
	}
	if !sim.Propose("a") {
		t.Fatal("propose failed")
	}
	sim.RunFor(400)
	for i := 0; i < 3; i++ {
		if got := sim.AppliedCommands(i); !reflect.DeepEqual(got, []string{"a"}) {
			t.Fatalf("node %d applied %v, want [a]", i, got)
		}
	}

	var followers []int
	for i := 0; i < 3; i++ {
		if i != lid {
			followers = append(followers, i)
		}
	}
	short, long := followers[0], followers[1]
	sim.Crash(short)
	sim.Crash(long)
	// Craft the on-disk state a prior term could have left behind: `long`
	// holds a divergent uncommitted suffix, `short` missed everything.
	sim.disks[long].Log = []Entry{{Term: 0}, {Term: 1, Command: "a"},
		{Term: 1, Command: "x1"}, {Term: 1, Command: "x2"}, {Term: 1, Command: "x3"}}
	sim.disks[short].Log = []Entry{{Term: 0}, {Term: 1, Command: "a"}}

	// Force a new term so the next leader's entries differ in term from the
	// divergent suffix, then commit b and c on the {leader, short} majority.
	sim.Crash(lid)
	sim.Restart(lid)
	sim.Restart(short)
	sim.RunFor(1000)
	lid2 := sim.Leader()
	if lid2 < 0 || lid2 == long {
		t.Fatalf("unexpected leader after recovery: %d", lid2)
	}
	sim.Propose("b")
	sim.Propose("c")
	sim.RunFor(600)
	for _, id := range []int{lid2, short} {
		if got := sim.AppliedCommands(id); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
			t.Fatalf("node %d applied %v, want [a b c]", id, got)
		}
	}

	// The long-divergent node rejoins and must converge to the same
	// committed prefix; its uncommitted suffix is truncated, never committed.
	sim.Restart(long)
	sim.RunFor(1200)
	want := []string{"a", "b", "c"}
	for i := 0; i < 3; i++ {
		if got := sim.AppliedCommands(i); !reflect.DeepEqual(got, want) {
			t.Fatalf("node %d committed prefix %v, want %v", i, got, want)
		}
	}
	for _, e := range sim.NodeLog(long) {
		if strings.HasPrefix(e.Command, "x") {
			t.Fatalf("divergent uncommitted entry %q survived recovery", e.Command)
		}
	}
	if len(sim.Violations) > 0 {
		t.Fatalf("safety violations: %v", sim.Violations)
	}
}

// 安全性证据: under randomized crashes, restarts, partitions, message loss
// and duplication, the simulator continuously checks state-machine safety
// (no committed entry is ever changed or divergent) and the run must end
// with zero violations and a single agreed committed prefix.
func TestSafetyUnderRandomFaults(t *testing.T) {
	sim := NewSim(5, 2024)
	sim.LossProb = 0.1
	sim.DupProb = 0.1
	rng := rand.New(rand.NewSource(99))

	for at := 200; at < 8000; at += 40 {
		at := at
		sim.Schedule(at, func() {
			switch rng.Intn(10) {
			case 0, 1:
				sim.Crash(rng.Intn(5))
			case 2, 3:
				sim.Restart(rng.Intn(5))
			case 4:
				sim.Partition(rng.Intn(5), rng.Intn(5))
			case 5:
				sim.Heal()
			default:
				sim.Propose(fmt.Sprintf("cmd@%d", at))
			}
		})
	}
	sim.RunFor(8000)
	if len(sim.Violations) > 0 {
		t.Fatalf("safety violations under faults: %v", sim.Violations)
	}

	// After faults stop, the cluster must converge to one committed prefix.
	sim.Heal()
	for i := 0; i < 5; i++ {
		sim.Restart(i)
	}
	sim.RunFor(4000)
	committed := false
	for try := 0; try < 5 && !committed; try++ {
		committed = sim.Propose("final")
		sim.RunFor(300)
	}
	if !committed {
		t.Fatal("no leader available after stabilization")
	}
	sim.RunFor(2000)

	ref := sim.Applied[0]
	if len(ref) < 5 {
		t.Fatalf("too few committed entries (%d); the run did not exercise replication", len(ref))
	}
	for i := 1; i < 5; i++ {
		if !reflect.DeepEqual(sim.Applied[i], ref) {
			t.Fatalf("node %d committed prefix diverges:\n got %v\nwant %v",
				i, sim.AppliedCommands(i), sim.AppliedCommands(0))
		}
	}
	if got := sim.AppliedCommands(0)[len(ref)-1]; got != "final" {
		t.Fatalf("last committed command = %q, want final", got)
	}
}

// 活性证据: with a stable network the cluster elects a leader within a
// bounded time and commits client commands within a bounded time, with no
// further elections disturbing the leader.
func TestLivenessAfterStability(t *testing.T) {
	sim := NewSim(3, 5)
	sim.RunFor(1000)
	lid := sim.Leader()
	if lid < 0 {
		t.Fatal("no leader elected within 1000ms")
	}
	term := sim.nodes[lid].currentTerm

	if !sim.Propose("x") {
		t.Fatal("propose failed")
	}
	sim.RunFor(500)
	for i := 0; i < 3; i++ {
		if got := sim.AppliedCommands(i); !reflect.DeepEqual(got, []string{"x"}) {
			t.Fatalf("node %d applied %v within 500ms, want [x]", i, got)
		}
		if sim.nodes[i].commitIndex != 1 {
			t.Fatalf("node %d commitIndex = %d, want 1", i, sim.nodes[i].commitIndex)
		}
	}
	if sim.Leader() != lid || sim.nodes[lid].currentTerm != term {
		t.Fatal("leadership changed without any fault")
	}
}
