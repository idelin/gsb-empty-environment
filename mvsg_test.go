package mvsg

import (
	"strings"
	"testing"
)

func depKinds(g *Graph, from, to TxnID) []EdgeKind {
	var kinds []EdgeKind
	for _, d := range g.Between(from, to) {
		kinds = append(kinds, d.Kind)
	}
	return kinds
}

func TestEdgeKinds(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{
			Write("x", "1"), Write("y", "1"),
		}},
		{ID: "T2", Committed: true, Ops: []Operation{
			Read("x", "T1", "1"), Write("x", "2"),
		}},
		{ID: "T3", Committed: true, Ops: []Operation{
			Read("y", "T1", "1"), Read("x", "T2", "2"),
		}},
	}}
	g := BuildGraph(h)

	// WR: T2 read the version T1 wrote.
	if !g.HasEdge("T1", "T2") {
		t.Fatal("missing WR edge T1 -> T2")
	}
	// WW: x@T1 is followed by x@T2.
	foundWW := false
	for _, d := range g.Between("T1", "T2") {
		if d.Kind == EdgeWW && d.Key == "x" {
			foundWW = true
		}
	}
	if !foundWW {
		t.Fatal("missing WW edge T1 -> T2 on x")
	}
	// RW: T3 read y@T1 and nobody overwrote y, so no RW edge from T3.
	if g.HasEdge("T3", "T1") {
		t.Fatal("unexpected edge T3 -> T1")
	}
	// WR: T3 read x@T2.
	if !g.HasEdge("T2", "T3") {
		t.Fatal("missing WR edge T2 -> T3")
	}
}

func TestSameKeyAccessIsNotBlanketConflict(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{Write("x", "1")}},
		{ID: "T2", Committed: true, Ops: []Operation{Write("x", "2")}},
		{ID: "T3", Committed: true, Ops: []Operation{Write("x", "3")}},
		{ID: "T4", Committed: true, Ops: []Operation{Read("x", "T1", "1")}},
		{ID: "T5", Committed: true, Ops: []Operation{Read("x", "T1", "1")}},
	}}
	g := BuildGraph(h)

	// Readers of the same key never conflict with each other.
	if g.HasEdge("T4", "T5") || g.HasEdge("T5", "T4") {
		t.Fatal("readers of the same key must not conflict")
	}
	// The anti-dependency targets only the writer of the immediately
	// following version, not every later writer of the key.
	if !g.HasEdge("T4", "T2") {
		t.Fatal("missing RW edge T4 -> T2")
	}
	if g.HasEdge("T4", "T3") {
		t.Fatal("RW edge must skip non-successor versions: no T4 -> T3")
	}
	// WW edges only between consecutive versions.
	if !g.HasEdge("T1", "T2") || !g.HasEdge("T2", "T3") {
		t.Fatal("missing WW edges between consecutive versions")
	}
	if g.HasEdge("T1", "T3") {
		t.Fatal("WW edge must not skip versions: no T1 -> T3")
	}
}

func TestUncommittedExcluded(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{
			ReadInitial("y", "0"), Write("x", "1"),
		}},
		{ID: "T2", Committed: false, Ops: []Operation{
			Read("x", "T1", "1"), Write("y", "2"),
		}},
	}}
	rep := Analyze(h)
	if !rep.Serializable() {
		t.Fatalf("uncommitted T2 must not create a cycle:\n%s", rep)
	}
	if len(rep.Graph.Txns) != 1 || rep.Graph.Txns[0] != "T1" {
		t.Fatalf("graph must contain only committed txns, got %v", rep.Graph.Txns)
	}
	if len(rep.Graph.Deps) != 0 {
		t.Fatalf("uncommitted txn must not contribute edges, got %v", rep.Graph.Deps)
	}
}

func TestReadOfUncommittedVersionWarns(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: false, Ops: []Operation{Write("x", "1")}},
		{ID: "T2", Committed: true, Ops: []Operation{Read("x", "T1", "1")}},
	}}
	rep := Analyze(h)
	if len(rep.Graph.Warnings) == 0 {
		t.Fatal("expected a warning for reading an uncommitted version")
	}
	if len(rep.Graph.Deps) != 0 {
		t.Fatal("no edge may be built from an uncommitted version")
	}
}

func TestWriteSkewCycleVerified(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{
			ReadInitial("y", "10"), Write("x", "10"),
		}},
		{ID: "T2", Committed: true, Ops: []Operation{
			ReadInitial("x", "10"), Write("y", "10"),
		}},
	}}
	rep := Analyze(h)
	if rep.Serializable() {
		t.Fatal("write skew must produce a cycle")
	}
	if len(rep.Cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(rep.Cycles))
	}
	c := rep.Cycles[0]
	if !c.Verified {
		t.Fatal("write-skew cycle must be verified by serial-order enumeration")
	}
	if c.PermutationsChecked != 2 {
		t.Fatalf("expected 2 serial orders checked, got %d", c.PermutationsChecked)
	}
	out := rep.String()
	for _, want := range []string{"RW(x)", "RW(y)", "T1", "T2", "verified"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestSerializableHistory(t *testing.T) {
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{Write("x", "1")}},
		{ID: "T2", Committed: true, Ops: []Operation{
			Read("x", "T1", "1"), Write("y", "1"),
		}},
		{ID: "T3", Committed: true, Ops: []Operation{
			Read("x", "T1", "1"), Read("y", "T2", "1"),
		}},
	}}
	rep := Analyze(h)
	if !rep.Serializable() {
		t.Fatalf("expected serializable, got:\n%s", rep)
	}
}

func TestThreeTxnCycleEvidence(t *testing.T) {
	// T1 -> T2 (WR x), T2 -> T3 (WR y), T3 -> T1 (WR z).
	h := &History{Txns: []*Transaction{
		{ID: "T1", Committed: true, Ops: []Operation{
			Read("z", "T3", "1"), Write("x", "1"),
		}},
		{ID: "T2", Committed: true, Ops: []Operation{
			Read("x", "T1", "1"), Write("y", "1"),
		}},
		{ID: "T3", Committed: true, Ops: []Operation{
			Read("y", "T2", "1"), Write("z", "1"),
		}},
	}}
	rep := Analyze(h)
	if rep.Serializable() {
		t.Fatal("expected a cycle")
	}
	c := rep.Cycles[0]
	if len(c.Txns) != 3 {
		t.Fatalf("expected 3 txns in cycle, got %v", c.Txns)
	}
	if !c.Verified {
		t.Fatal("cycle must be verified")
	}
	if c.PermutationsChecked != 6 {
		t.Fatalf("expected 6 serial orders, got %d", c.PermutationsChecked)
	}
	// Every hop must carry at least one dependency as evidence.
	for _, e := range c.Edges {
		if len(e.Deps) == 0 {
			t.Fatalf("cycle edge %s -> %s has no evidence", e.From, e.To)
		}
	}
}

func TestCycleWithExternalReadUnverified(t *testing.T) {
	// The cycle T1 <-> T2 exists in the graph, but the read of x@T9
	// involves a transaction outside the cycle, so the serial-order
	// enumeration cannot confirm the anomaly from the cycle alone.
	h := &History{Txns: []*Transaction{
		{ID: "T9", Committed: true, Ops: []Operation{Write("x", "9")}},
		{ID: "T1", Committed: true, Ops: []Operation{
			Write("x", "1"), Write("y", "1"),
		}},
		{ID: "T2", Committed: true, Ops: []Operation{
			Read("x", "T9", "9"), Read("y", "T1", "1"),
		}},
	}}
	rep := Analyze(h)
	if rep.Serializable() {
		t.Fatal("expected a cycle")
	}
	c := rep.Cycles[0]
	if c.Verified {
		t.Fatal("cycle with an unconstrained external read must stay unverified")
	}
	if c.PermutationsChecked == 0 {
		t.Fatal("enumeration should still have run")
	}
}

func TestMultipleCycles(t *testing.T) {
	// Two independent write-skew pairs.
	h := &History{Txns: []*Transaction{
		{ID: "A1", Committed: true, Ops: []Operation{
			ReadInitial("b", "0"), Write("a", "1"),
		}},
		{ID: "A2", Committed: true, Ops: []Operation{
			ReadInitial("a", "0"), Write("b", "1"),
		}},
		{ID: "B1", Committed: true, Ops: []Operation{
			ReadInitial("d", "0"), Write("c", "1"),
		}},
		{ID: "B2", Committed: true, Ops: []Operation{
			ReadInitial("c", "0"), Write("d", "1"),
		}},
	}}
	rep := Analyze(h)
	if len(rep.Cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d:\n%s", len(rep.Cycles), rep)
	}
	for _, c := range rep.Cycles {
		if !c.Verified {
			t.Fatalf("cycle %v must be verified", c.Txns)
		}
	}
}
