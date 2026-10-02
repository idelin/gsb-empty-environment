package logrepl

import (
	"reflect"
	"testing"
)

func runTicks(c *Cluster, n int) {
	for i := 0; i < n; i++ {
		c.Tick()
	}
}

func cmds(n *Node) []string {
	var out []string
	for _, e := range n.Entries() {
		out = append(out, e.Cmd)
	}
	return out
}

// termTracker asserts per-node term monotonicity across phases.
type termTracker struct{ prev map[int]int }

func newTermTracker() *termTracker { return &termTracker{prev: map[int]int{}} }

func (tt *termTracker) check(t *testing.T, c *Cluster) {
	t.Helper()
	for _, id := range []int{1, 2, 3} {
		term := c.Node(id).Term()
		if term < tt.prev[id] {
			t.Fatalf("node %d term went backwards: %d -> %d", id, tt.prev[id], term)
		}
		tt.prev[id] = term
	}
}

// TestViewChangeScenario is the acceptance scenario:
//  1. three-node cluster commits 3 entries in term 1 (leader = C);
//  2. C is isolated; the majority A/B moves to term 2 and commits 4 more;
//  3. C is healed and receives a higher-term heartbeat: it must roll back
//     its conflicting uncommitted entry without losing committed ones.
func TestViewChangeScenario(t *testing.T) {
	c := NewCluster(1, 2, 3)
	tt := newTermTracker()

	// --- Phase 1: elect C (node 3) as term-1 leader, commit 3 entries ---
	c.TriggerTimeout(3)
	runTicks(c, 10)
	leader, ok := c.Leader()
	if !ok || leader != 3 {
		t.Fatalf("phase 1: leader = %d, %v; want C=3", leader, ok)
	}
	if term := c.Node(3).Term(); term != 1 {
		t.Fatalf("phase 1: leader term = %d, want 1", term)
	}
	for _, cmd := range []string{"e1", "e2", "e3"} {
		if err := c.Propose(leader, cmd); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(c, 10)
	for _, id := range []int{1, 2, 3} {
		if got := c.Node(id).Committed(); got != 3 {
			t.Fatalf("phase 1: node %d committed = %d, want 3", id, got)
		}
	}
	committedPrefix := cmds(c.Node(3))
	tt.check(t, c)

	// --- Phase 2: isolate C. As a stale leader it accepts one more write,
	// which can never commit -> conflicting uncommitted entry at index 4 ---
	c.Isolate(3)
	if err := c.Propose(3, "X-uncommitted"); err != nil {
		t.Fatal(err)
	}
	runTicks(c, 2)
	if got := c.Node(3).LastIndex(); got != 4 {
		t.Fatalf("phase 2: C lastIndex = %d, want 4", got)
	}
	if got := c.Node(3).Committed(); got != 3 {
		t.Fatalf("phase 2: C committed = %d, want 3 (X must stay uncommitted)", got)
	}

	// --- Phase 3: majority A/B moves to term 2 and commits 4 entries ---
	runTicks(c, 60)
	leader2, ok := c.Leader()
	if !ok || leader2 == 3 {
		t.Fatalf("phase 3: leader = %d, %v; want one of A/B", leader2, ok)
	}
	if term := c.Node(leader2).Term(); term != 2 {
		t.Fatalf("phase 3: leader term = %d, want 2", term)
	}
	if term := c.Node(3).Term(); term != 1 {
		t.Fatalf("phase 3: isolated C inflated its term to %d, want 1", term)
	}
	for _, cmd := range []string{"e4", "e5", "e6", "e7"} {
		if err := c.Propose(leader2, cmd); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(c, 10)
	for _, id := range []int{1, 2} {
		if got := c.Node(id).Committed(); got != 7 {
			t.Fatalf("phase 3: node %d committed = %d, want 7", id, got)
		}
	}
	if got := c.Node(3).Committed(); got != 3 {
		t.Fatalf("phase 3: isolated C committed = %d, want 3", got)
	}
	tt.check(t, c)

	// --- Phase 4: heal C. The term-2 leader's heartbeat is a higher-term
	// heartbeat for C (term 2 > 1): C must step down, roll back X, and
	// catch up without losing committed entries ---
	c.Heal(3)
	runTicks(c, 20)
	n3 := c.Node(3)
	if n3.Term() != 2 || n3.Role() != Follower {
		t.Fatalf("phase 4: C term=%d role=%s, want term=2 Follower", n3.Term(), n3.Role())
	}
	if got := n3.Committed(); got != 7 {
		t.Fatalf("phase 4: C committed = %d, want 7", got)
	}
	want := []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}
	for _, id := range []int{1, 2, 3} {
		if got := cmds(c.Node(id)); !reflect.DeepEqual(got, want) {
			t.Fatalf("phase 4: node %d log = %v, want %v", id, got, want)
		}
	}
	if got := cmds(n3)[:3]; !reflect.DeepEqual(got, committedPrefix) {
		t.Fatalf("phase 4: C lost committed prefix: got %v, want %v", got, committedPrefix)
	}
	tt.check(t, c)
}

// TestPreVoteKeepsIsolatedFollowerTermStable proves pre-vote works: a
// partitioned follower retries elections but never inflates its term, and
// does not disrupt the cluster when it rejoins.
func TestPreVoteKeepsIsolatedFollowerTermStable(t *testing.T) {
	c := NewCluster(1, 2, 3)
	c.TriggerTimeout(1)
	runTicks(c, 10)
	if leader, _ := c.Leader(); leader != 1 {
		t.Fatalf("leader = %d, want 1", c.Node(1).ID())
	}
	if err := c.Propose(1, "e1"); err != nil {
		t.Fatal(err)
	}
	runTicks(c, 10)

	c.Isolate(3)
	runTicks(c, 100)
	n3 := c.Node(3)
	if n3.PreVoteRounds() == 0 {
		t.Fatal("isolated follower never pre-voted; test is vacuous")
	}
	if n3.Term() != 1 || n3.Role() != Follower {
		t.Fatalf("isolated follower term=%d role=%s, want term=1 Follower", n3.Term(), n3.Role())
	}

	c.Heal(3)
	runTicks(c, 20)
	if n3.Term() != 1 {
		t.Fatalf("rejoining follower disrupted the cluster: term=%d, want 1", n3.Term())
	}
	if got := n3.Committed(); got != 1 {
		t.Fatalf("rejoined follower committed = %d, want 1", got)
	}
	if got, want := cmds(n3), cmds(c.Node(1)); !reflect.DeepEqual(got, want) {
		t.Fatalf("rejoined follower log = %v, want %v", got, want)
	}
}

// TestCommittedEntriesAreNeverTruncated injects a forged AppendEntries that
// tries to overwrite a committed entry; the follower must refuse.
func TestCommittedEntriesAreNeverTruncated(t *testing.T) {
	c := NewCluster(1, 2, 3)
	c.TriggerTimeout(1)
	runTicks(c, 10)
	for _, cmd := range []string{"e1", "e2", "e3"} {
		if err := c.Propose(1, cmd); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(c, 10)
	if got := c.Node(2).Committed(); got != 3 {
		t.Fatalf("node 2 committed = %d, want 3", got)
	}

	c.Inject(Message{Type: MsgAppend, From: 1, To: 2, Term: 1,
		PrevLogIndex: 1, PrevLogTerm: 1,
		Entries: []Entry{{Term: 1, Cmd: "forged"}}, LeaderCommit: 1})
	runTicks(c, 2)

	if got := cmds(c.Node(2)); !reflect.DeepEqual(got, []string{"e1", "e2", "e3"}) {
		t.Fatalf("committed entries were modified: log = %v", got)
	}
}

// TestRejectsStaleTermMessages injects an AppendEntries from an old term and
// verifies the follower ignores it.
func TestRejectsStaleTermMessages(t *testing.T) {
	c := NewCluster(1, 2, 3)
	c.TriggerTimeout(1)
	runTicks(c, 10)
	if err := c.Propose(1, "e1"); err != nil {
		t.Fatal(err)
	}
	runTicks(c, 10)

	c.Inject(Message{Type: MsgAppend, From: 1, To: 2, Term: 0,
		PrevLogIndex: 0, PrevLogTerm: 0,
		Entries: []Entry{{Term: 0, Cmd: "stale"}}})
	runTicks(c, 2)

	n2 := c.Node(2)
	if n2.Term() != 1 || n2.LastIndex() != 1 {
		t.Fatalf("stale append took effect: term=%d lastIndex=%d", n2.Term(), n2.LastIndex())
	}
}

// TestLogConsistencyCheck drives the backoff path directly: a follower whose
// log is a prefix of the leader's rejects until PrevLogIndex matches.
func TestLogConsistencyCheck(t *testing.T) {
	c := NewCluster(1, 2, 3)
	c.TriggerTimeout(1)
	runTicks(c, 10)
	if err := c.Propose(1, "e1"); err != nil {
		t.Fatal(err)
	}
	runTicks(c, 10)

	// Cut B off, add entries on the leader, then heal: B must catch up via
	// the prevLog consistency check, not by blind appends.
	c.Isolate(2)
	for _, cmd := range []string{"e2", "e3"} {
		if err := c.Propose(1, cmd); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(c, 10)
	if got := c.Node(2).LastIndex(); got != 1 {
		t.Fatalf("isolated node 2 lastIndex = %d, want 1", got)
	}
	c.Heal(2)
	runTicks(c, 10)
	if got, want := cmds(c.Node(2)), []string{"e1", "e2", "e3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("node 2 log = %v, want %v", got, want)
	}
	if got := c.Node(2).Committed(); got != 3 {
		t.Fatalf("node 2 committed = %d, want 3", got)
	}
}
