package delta

import (
	"fmt"
	"net"
	"testing"
)

// buildDoc creates a document of size characters at the given node,
// each inserted in O(1) after the previous one.
func buildDoc(n *Node, size int) {
	parent := Dot{}
	for i := 0; i < size; i++ {
		parent = n.Doc().InsertAfter(parent, 'a'+rune(i%26))
	}
}

// syncNodes runs one in-process sync round and returns the byte sizes
// of the two deltas exchanged.
func syncNodes(t *testing.T, a, b *Node) (int, int) {
	t.Helper()
	da := a.DeltaFor(b.ID())
	db := b.DeltaFor(a.ID())
	sa, err := EncodedSize(da)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := EncodedSize(db)
	if err != nil {
		t.Fatal(err)
	}
	ackB := b.HandleDelta(a.ID(), da)
	ackA := a.HandleDelta(b.ID(), db)
	a.HandleAck(b.ID(), ackB)
	b.HandleAck(a.ID(), ackA)
	return sa, sb
}

// Acceptance test: in a 10 000-element document, modifying a single
// character must produce a constant-size delta, and the receiver's
// state after applying it must be identical to a full-state transfer.
func TestDeltaConstantSizeOneCharChange(t *testing.T) {
	const docSize = 10000
	const maxDeltaBytes = 512 // constant bound, independent of docSize

	a, b := NewNode("A"), NewNode("B")
	buildDoc(a, docSize)

	// Round 1: initial sync degrades to a full-state transfer.
	fullSize, _ := syncNodes(t, a, b)
	if got := b.Doc().Len(); got != docSize {
		t.Fatalf("after full sync B has %d chars, want %d", got, docSize)
	}

	// Modify exactly one character in the middle: delete + insert.
	before, ok := a.Doc().DotAt(docSize/2 - 1)
	if !ok {
		t.Fatal("DotAt failed")
	}
	doomed, _ := a.Doc().DotAt(docSize / 2)
	a.Doc().Delete(doomed)
	a.Doc().InsertAfter(before, 'Z')

	// Round 2: only the change since B's ack is sent.
	deltaSize, _ := syncNodes(t, a, b)
	t.Logf("docSize=%d  full-state=%d bytes (%d ops)  delta=%d bytes",
		docSize, fullSize, a.Doc().OpCount(), deltaSize)

	if deltaSize > maxDeltaBytes {
		t.Fatalf("delta = %d bytes, exceeds constant bound %d", deltaSize, maxDeltaBytes)
	}

	// Equivalence: B (delta recipient) must equal a fresh node C that
	// received the full state, and both must match A's content.
	c := NewNode("C")
	c.HandleDelta(a.ID(), a.DeltaFor(c.ID()))
	if !b.Doc().EqualState(c.Doc()) {
		t.Fatal("delta-applied state differs from full-state transfer")
	}
	if b.Doc().String() != a.Doc().String() {
		t.Fatal("delta-applied content differs from source")
	}
}

// The delta for a one-character change must not grow with the document.
func TestDeltaSizeIndependentOfDocSize(t *testing.T) {
	sizes := []int{1000, 10000, 50000}
	deltaSizes := make([]int, len(sizes))
	for i, size := range sizes {
		a, b := NewNode(ReplicaID(fmt.Sprintf("A%d", i))), NewNode("B")
		buildDoc(a, size)
		syncNodes(t, a, b) // full sync

		doomed, _ := a.Doc().DotAt(size / 2)
		a.Doc().Delete(doomed)

		deltaSize, _ := syncNodes(t, a, b)
		deltaSizes[i] = deltaSize
		t.Logf("docSize=%-6d delta=%d bytes", size, deltaSize)
	}
	// Allow a few bytes of varint slack for larger sequence numbers,
	// but the size must be flat: not proportional to the document.
	if lo, hi := deltaSizes[0], deltaSizes[len(deltaSizes)-1]; hi-lo > 16 {
		t.Fatalf("delta grew with document size: %v", deltaSizes)
	}
}

// After a successful sync round with no new edits, the next delta must
// be empty: the ack watermark covers everything.
func TestAckWatermarkShrinksDeltas(t *testing.T) {
	a, b := NewNode("A"), NewNode("B")
	buildDoc(a, 100)

	d1 := a.DeltaFor(b.ID())
	if len(d1.Ops) != 100 {
		t.Fatalf("first delta has %d ops, want 100", len(d1.Ops))
	}
	SyncPair(a, b)

	d2 := a.DeltaFor(b.ID())
	if len(d2.Ops) != 0 {
		t.Fatalf("delta after ack has %d ops, want 0", len(d2.Ops))
	}
	if !a.Watermark(b.ID()).Equal(b.Doc().Context()) {
		t.Fatal("A's watermark for B does not match B's context")
	}

	// One new edit -> exactly one op in the next delta.
	a.Doc().Append('!')
	d3 := a.DeltaFor(b.ID())
	if len(d3.Ops) != 1 {
		t.Fatalf("delta after one edit has %d ops, want 1", len(d3.Ops))
	}
}

// Delta sync must also propagate operations learned from third parties
// (transitive dissemination).
func TestDeltaPropagatesThirdPartyOps(t *testing.T) {
	a, b, c := NewNode("A"), NewNode("B"), NewNode("C")
	buildDoc(a, 10)
	SyncPair(a, b) // B learns A's ops
	SyncPair(b, c) // C must learn A's ops via B's delta
	if c.Doc().String() != a.Doc().String() {
		t.Fatalf("C = %q, want %q", c.Doc().String(), a.Doc().String())
	}
}

// The wire protocol over a real net.Conn (net.Pipe): full sync, then a
// one-character change synced in a second round on the same connection.
func TestSyncConnOverPipe(t *testing.T) {
	a, b := NewNode("A"), NewNode("B")
	buildDoc(a, 10000)

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	errCh := make(chan error, 2)
	go func() { errCh <- SyncConn(a, b.ID(), c1, 1) }()
	go func() { errCh <- SyncConn(b, a.ID(), c2, 1) }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if b.Doc().Len() != 10000 {
		t.Fatalf("B has %d chars after full sync, want 10000", b.Doc().Len())
	}

	// One-character change, second round on the same connection.
	doomed, _ := a.Doc().DotAt(5000)
	a.Doc().Delete(doomed)
	deltaSize, err := EncodedSize(a.DeltaFor(b.ID()))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("second-round delta over conn: %d bytes", deltaSize)

	go func() { errCh <- SyncConn(a, b.ID(), c1, 1) }()
	go func() { errCh <- SyncConn(b, a.ID(), c2, 1) }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if !a.Doc().EqualState(b.Doc()) {
		t.Fatal("states differ after conn sync")
	}
	if deltaSize > 512 {
		t.Fatalf("conn delta = %d bytes, exceeds constant bound", deltaSize)
	}
}

// N nodes with concurrent edits converge to identical states after
// pair-wise delta sync rounds.
func TestMultiNodeConvergence(t *testing.T) {
	const n = 4
	nodes := make([]*Node, n)
	for i := range nodes {
		nodes[i] = NewNode(ReplicaID(fmt.Sprintf("N%d", i)))
	}

	// Concurrent, disjoint edits on every node.
	for i, nd := range nodes {
		for j := 0; j < 50; j++ {
			nd.Doc().Append(rune('a' + i))
			_ = j
		}
	}

	// Two full mesh rounds are enough for total dissemination.
	for round := 0; round < 2; round++ {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				SyncPair(nodes[i], nodes[j])
			}
		}
	}

	for i := 1; i < n; i++ {
		if !nodes[0].Doc().EqualState(nodes[i].Doc()) {
			t.Fatalf("node 0 and node %d diverged", i)
		}
	}
	if got := nodes[0].Doc().Len(); got != n*50 {
		t.Fatalf("converged length = %d, want %d", got, n*50)
	}
}
