package paxos

import (
	"strings"
	"testing"
)

// healthyCluster returns a 5-node cluster with 3 committed entries.
func healthyCluster(t *testing.T) *Cluster {
	t.Helper()
	c := NewCluster(5)
	mustBecomeLeader(t, c.Node(0))
	mustPropose(t, c.Node(0), "a")
	mustPropose(t, c.Node(0), "b")
	mustPropose(t, c.Node(0), "c")
	return c
}

func TestInspectHealthyCluster(t *testing.T) {
	c := healthyCluster(t)
	if rep := Inspect(c.Nodes()); !rep.OK() {
		t.Fatalf("expected clean report, got:\n%s", rep)
	}
	if vs := AssertInvariants(c.Nodes()); len(vs) > 0 {
		t.Fatalf("expected no violations, got %v", vs)
	}
}

func TestDanglingCommitIndex(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(2)
	n.mu.Lock()
	n.commitIndex = 42 // dangling: beyond log length
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindDanglingReference)
	if len(found) == 0 {
		t.Fatalf("expected dangling-reference finding, got:\n%s", rep)
	}
	if found[0].Node != 2 || found[0].Severity != SeverityCritical {
		t.Fatalf("unexpected finding: %+v", found[0])
	}
}

func TestDanglingCommitHole(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(1)
	n.mu.Lock()
	n.log[2].Committed = false // hole below commitIndex
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindDanglingReference)
	if len(found) == 0 || !strings.Contains(found[0].Detail, "hole") {
		t.Fatalf("expected hole-below-commitIndex finding, got:\n%s", rep)
	}
}

func TestDanglingPrevReference(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(3)
	n.mu.Lock()
	n.log[2].Entry.Prev = 9 // targets a non-existent slot
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindDanglingReference)
	if len(found) == 0 {
		t.Fatalf("expected dangling Prev finding, got:\n%s", rep)
	}
}

func TestNegativePrevReference(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(0)
	n.mu.Lock()
	n.log[1].Entry.Prev = -1
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	if len(rep.ByKind(KindDanglingReference)) == 0 {
		t.Fatalf("expected negative Prev finding, got:\n%s", rep)
	}
}

func TestOrphanCycle(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(0)
	n.mu.Lock()
	// Forge an isolated loop: 2 -> 3 -> 2.
	n.log[2].Entry.Prev = 3
	n.log[3].Entry.Prev = 2
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindOrphanCycle)
	if len(found) == 0 {
		t.Fatalf("expected orphan-cycle finding, got:\n%s", rep)
	}
	if !strings.Contains(found[0].Detail, "cycle") {
		t.Fatalf("expected cycle detail, got %q", found[0].Detail)
	}
}

func TestOrphanBreak(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(4)
	n.mu.Lock()
	// Slot 3 references empty slot 2: the chain is orphaned.
	n.log[2] = Slot{}
	n.log[3].Entry.Prev = 2
	n.log[3].Committed = true
	n.commitIndex = 1
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindOrphanCycle)
	if len(found) == 0 || !strings.Contains(found[0].Detail, "orphan") {
		t.Fatalf("expected orphan finding, got:\n%s", rep)
	}
}

func TestDataSkewUnderReplicatedCommit(t *testing.T) {
	c := healthyCluster(t)
	// Erase slot 3 from all but one node: the committed entry is no
	// longer durable on a quorum.
	for _, id := range []int{1, 2, 3, 4} {
		n := c.Node(id)
		n.mu.Lock()
		n.log = n.log[:3]
		n.commitIndex = 2
		n.mu.Unlock()
	}
	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindDataSkew)
	if len(found) == 0 || found[0].Severity != SeverityCritical {
		t.Fatalf("expected critical data-skew finding, got:\n%s", rep)
	}
	vs := AssertInvariants(c.Nodes())
	if len(vs) == 0 || !strings.Contains(vs[0], "LeaderCompleteness") {
		t.Fatalf("expected LeaderCompleteness violation, got %v", vs)
	}
}

func TestDataSkewLogLength(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(0)
	n.mu.Lock()
	// Inflate node 0's log far beyond its peers (uncommitted tail).
	for s := len(n.log); s <= 8; s++ {
		n.log = append(n.log, Slot{
			HasAccepted: true,
			Accepted:    Ballot{Term: 1, Node: 0},
			Entry:       LogEntry{Term: 1, Value: "junk", Prev: s - 1},
		})
	}
	n.mu.Unlock()

	rep := Inspect(c.Nodes())
	found := rep.ByKind(KindDataSkew)
	if len(found) == 0 || found[0].Severity != SeverityWarning {
		t.Fatalf("expected warning log-length skew finding, got:\n%s", rep)
	}
}

func TestInvariantElectionSafety(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(3)
	n.mu.Lock()
	n.isLeader = true // second leader in the same term
	n.currentTerm = c.Node(0).Snapshot().CurrentTerm
	n.mu.Unlock()

	vs := AssertInvariants(c.Nodes())
	if len(vs) == 0 || !strings.Contains(vs[0], "ElectionSafety") {
		t.Fatalf("expected ElectionSafety violation, got %v", vs)
	}
}

func TestInvariantLogMatching(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(1)
	n.mu.Lock()
	// Same slot, same accept term, different value.
	n.log[2].Entry.Value = "forged"
	n.mu.Unlock()

	vs := AssertInvariants(c.Nodes())
	foundLogMatching := false
	foundStateMachine := false
	for _, v := range vs {
		if strings.Contains(v, "LogMatching") {
			foundLogMatching = true
		}
		if strings.Contains(v, "StateMachineSafety") {
			foundStateMachine = true
		}
	}
	if !foundLogMatching || !foundStateMachine {
		t.Fatalf("expected LogMatching and StateMachineSafety violations, got %v", vs)
	}
}

func TestInvariantStateMachineSafety(t *testing.T) {
	c := healthyCluster(t)
	n := c.Node(4)
	n.mu.Lock()
	n.log[1].Entry.Value = "conflict" // divergent committed value
	n.mu.Unlock()

	vs := AssertInvariants(c.Nodes())
	found := false
	for _, v := range vs {
		if strings.Contains(v, "StateMachineSafety") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected StateMachineSafety violation, got %v", vs)
	}
}

// TestReportHelpers exercises the report accessors.
func TestReportHelpers(t *testing.T) {
	c := healthyCluster(t)
	rep := Inspect(c.Nodes())
	if !rep.OK() || len(rep.Critical()) != 0 {
		t.Fatalf("healthy cluster should be OK, got:\n%s", rep)
	}
	if !strings.Contains(rep.String(), "OK") {
		t.Fatalf("unexpected report string: %q", rep.String())
	}
}
