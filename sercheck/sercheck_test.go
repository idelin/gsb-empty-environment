package sercheck

import (
	"errors"
	"strings"
	"testing"
)

func findEdge(g *Graph, from, to TxnID, typ EdgeType) *Edge {
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.From == from && e.To == to && e.Type == typ {
			return e
		}
	}
	return nil
}

func TestWriteReadEdge(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	h.Write(1, "x", "1")
	h.Commit(1)
	h.Read(2, "x", "1", 1)
	h.Commit(2)

	g := h.Graph()
	e := findEdge(g, 1, 2, EdgeWriteRead)
	if e == nil {
		t.Fatalf("expected WR edge T1->T2, got:\n%s", g)
	}
	if len(e.Reads) != 1 || e.Reads[0].Txn != 2 {
		t.Fatalf("WR edge missing read evidence: %+v", e)
	}
	if len(e.Writes) != 1 || e.Writes[0].Txn != 1 {
		t.Fatalf("WR edge missing write evidence: %+v", e)
	}
	if len(g.Cycles()) != 0 {
		t.Fatalf("unexpected cycle")
	}
	rep := Check(h, DefaultEnumLimit)
	if !rep.Serializable || !rep.WitnessFound || !rep.Consistent {
		t.Fatalf("expected serializable with witness, got:\n%s", rep)
	}
	if len(rep.Witness) != 2 || rep.Witness[0] != 1 || rep.Witness[1] != 2 {
		t.Fatalf("unexpected witness order: %v", rep.Witness)
	}
}

func TestReadWriteAntiDependencyCycle(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	h.SetInitial("y", "0")
	// T1 reads initial x, writes y; T2 reads initial y, writes x.
	h.Read(1, "x", "0", InitialTxn)
	h.Write(1, "y", "1")
	h.Read(2, "y", "0", InitialTxn)
	h.Write(2, "x", "2")
	h.Commit(1)
	h.Commit(2)

	rep := Check(h, DefaultEnumLimit)
	if rep.Serializable {
		t.Fatalf("expected non-serializable, got:\n%s", rep)
	}
	if len(rep.Cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d:\n%s", len(rep.Cycles), rep)
	}
	c := rep.Cycles[0]
	if len(c.Txns) != 2 {
		t.Fatalf("expected 2-txn cycle, got %v", c.Txns)
	}
	if findEdge(rep.Graph, 1, 2, EdgeReadWrite) == nil {
		t.Fatalf("expected RW edge T1->T2 on x")
	}
	if findEdge(rep.Graph, 2, 1, EdgeReadWrite) == nil {
		t.Fatalf("expected RW edge T2->T1 on y")
	}
	// Every hop must carry concrete operation evidence.
	for _, hop := range c.Hops {
		if len(hop.Edges) != 1 || len(hop.Edges[0].Reads) != 1 || len(hop.Edges[0].Writes) != 1 {
			t.Fatalf("hop %v lacks evidence: %+v", hop, hop.Edges)
		}
	}
	if rep.WitnessFound {
		t.Fatalf("enumeration must not find a serial witness")
	}
	if !rep.Consistent {
		t.Fatalf("graph and enumeration disagree:\n%s", rep)
	}
}

func TestWriteWriteEdgeAndNoSameKeyFalseConflict(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	h.Write(1, "x", "1")
	h.Commit(1)
	h.Write(2, "x", "2")
	h.Commit(2)
	// T3 reads the *latest* version of x: same key as T1's write, but
	// no dependency between T1 and T3 may be inferred.
	h.Read(3, "x", "2", 2)
	h.Commit(3)

	g := h.Graph()
	if findEdge(g, 1, 2, EdgeWriteWrite) == nil {
		t.Fatalf("expected WW edge T1->T2, got:\n%s", g)
	}
	if findEdge(g, 2, 3, EdgeWriteRead) == nil {
		t.Fatalf("expected WR edge T2->T3, got:\n%s", g)
	}
	for _, e := range g.Edges {
		if (e.From == 1 && e.To == 3) || (e.From == 3 && e.To == 1) {
			t.Fatalf("same-key access misclassified as conflict: %s", e)
		}
	}
	if len(g.Edges) != 2 {
		t.Fatalf("expected exactly 2 edges, got:\n%s", g)
	}
	rep := Check(h, DefaultEnumLimit)
	if !rep.Serializable || !rep.Consistent {
		t.Fatalf("expected serializable, got:\n%s", rep)
	}
}

func TestStaleReadTargetsOnlyOverwriter(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	h.Write(1, "x", "1")
	h.Commit(1)
	h.Write(2, "x", "2")
	h.Commit(2)
	// T3 reads the *stale* version written by T1.
	h.Read(3, "x", "1", 1)
	h.Commit(3)

	g := h.Graph()
	if findEdge(g, 1, 3, EdgeWriteRead) == nil {
		t.Fatalf("expected WR edge T1->T3")
	}
	rw := findEdge(g, 3, 2, EdgeReadWrite)
	if rw == nil {
		t.Fatalf("expected RW anti-dependency T3->T2 (T2 overwrote the read version)")
	}
	if rw.Key != "x" || len(rw.Reads) != 1 || rw.Reads[0].ReadFrom != 1 {
		t.Fatalf("RW edge evidence wrong: %+v", rw)
	}
	if findEdge(g, 3, 1, EdgeReadWrite) != nil {
		t.Fatalf("no RW edge may point at the txn whose version was read")
	}
	// Serializable as T1, T3, T2? No: WW T1->T2, WR T1->T3, RW T3->T2
	// is acyclic; witness must place T3 between T1 and T2.
	rep := Check(h, DefaultEnumLimit)
	if !rep.Serializable || !rep.Consistent {
		t.Fatalf("expected serializable, got:\n%s", rep)
	}
	w := rep.Witness
	pos := make(map[TxnID]int)
	for i, t := range w {
		pos[t] = i
	}
	if !(pos[1] < pos[3] && pos[3] < pos[2]) {
		t.Fatalf("witness %v violates T1<T3<T2", w)
	}
}

func TestUncommittedExcluded(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	h.Write(1, "x", "1")
	h.Commit(1)
	h.Write(2, "x", "2") // never commits
	h.Read(3, "x", "1", 1)
	h.Commit(3)
	h.Read(4, "x", "2", 2) // dirty read of uncommitted version
	h.Commit(4)

	g := h.Graph()
	for _, n := range g.Nodes() {
		if n == 2 {
			t.Fatalf("uncommitted T2 must not appear in graph")
		}
	}
	if len(g.Edges) != 1 || findEdge(g, 1, 3, EdgeWriteRead) == nil {
		t.Fatalf("expected only WR T1->T3, got:\n%s", g)
	}
	if len(g.Anomalies) != 1 || g.Anomalies[0].Op.Txn != 4 {
		t.Fatalf("expected dirty-read anomaly for T4, got %+v", g.Anomalies)
	}
	if !strings.Contains(g.Anomalies[0].Reason, "dirty read") {
		t.Fatalf("anomaly reason should mention dirty read: %s", g.Anomalies[0].Reason)
	}
	// Graph is acyclic, but the dirty read is not reproducible by any
	// serial execution of committed transactions.
	rep := Check(h, DefaultEnumLimit)
	if !rep.Serializable {
		t.Fatalf("graph over committed txns should be acyclic")
	}
	if rep.WitnessFound {
		t.Fatalf("dirty read must be unreproducible serially")
	}
	if rep.Consistent {
		t.Fatalf("expected inconsistency flag (acyclic graph, no witness)")
	}
}

func TestReadOwnWriteCreatesNoEdge(t *testing.T) {
	h := NewHistory()
	h.Write(1, "x", "1")
	h.Read(1, "x", "1", 1)
	h.Commit(1)

	g := h.Graph()
	if len(g.Edges) != 0 {
		t.Fatalf("own-write read must not create edges, got:\n%s", g)
	}
	rep := Check(h, DefaultEnumLimit)
	if !rep.Serializable || !rep.WitnessFound {
		t.Fatalf("expected serializable, got:\n%s", rep)
	}
}

func TestAntiDependencySkipsReadersOwnVersion(t *testing.T) {
	h := NewHistory()
	h.SetInitial("x", "0")
	// T1 reads initial x, then writes x itself; T2 overwrites T1's
	// version. The anti-dependency must target T2, skipping T1's own
	// intervening version.
	h.Read(1, "x", "0", InitialTxn)
	h.Write(1, "x", "1")
	h.Commit(1)
	h.Write(2, "x", "2")
	h.Commit(2)

	g := h.Graph()
	if findEdge(g, 1, 2, EdgeReadWrite) == nil {
		t.Fatalf("expected RW edge T1->T2, got:\n%s", g)
	}
	for _, e := range g.Edges {
		if e.From == e.To {
			t.Fatalf("self edge found: %s", e)
		}
	}
}

func TestEnumerateLimit(t *testing.T) {
	h := NewHistory()
	for t := TxnID(1); t <= 5; t++ {
		h.Write(t, "k", "v")
		h.Commit(t)
	}
	if _, _, err := h.EnumerateSerial(4); !errors.Is(err, ErrEnumLimit) {
		t.Fatalf("expected ErrEnumLimit, got %v", err)
	}
	if _, ok, err := h.EnumerateSerial(5); err != nil || !ok {
		t.Fatalf("expected witness within limit, got ok=%v err=%v", ok, err)
	}
}

func TestCycleStringIncludesEvidence(t *testing.T) {
	h := NewHistory()
	h.Read(1, "x", "", InitialTxn)
	h.Write(1, "y", "1")
	h.Read(2, "y", "", InitialTxn)
	h.Write(2, "x", "2")
	h.Commit(1)
	h.Commit(2)

	rep := Check(h, DefaultEnumLimit)
	s := rep.String()
	if !strings.Contains(s, "RW") || !strings.Contains(s, "cycle:") {
		t.Fatalf("report should render cycle with edge types:\n%s", s)
	}
	if !strings.Contains(s, "evidence") {
		t.Fatalf("report should render operation evidence:\n%s", s)
	}
}
