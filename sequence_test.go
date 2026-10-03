package lseq

import (
	"math/rand"
	"strings"
	"testing"
)

// TestAllocateBetweenAdjacent verifies that Allocate always produces dense
// identifiers strictly between two neighbours, even after repeated splits.
func TestAllocateBetweenAdjacent(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	ids := []Identifier{nil, nil} // virtual bounds
	for i := 0; i < 2000; i++ {
		// pick a random adjacent pair and split it
		idx := rng.Intn(len(ids) - 1)
		id := Allocate(ids[idx], ids[idx+1], uint64(i%7)+1, rng)
		// nil neighbours are virtual bounds: nil p orders before everything,
		// nil q after everything.
		if (ids[idx] != nil && Compare(ids[idx], id) >= 0) ||
			(ids[idx+1] != nil && Compare(id, ids[idx+1]) >= 0) {
			t.Fatalf("iteration %d: identifier not strictly between neighbours", i)
		}
		ids = append(ids, nil)
		copy(ids[idx+2:], ids[idx+1:])
		ids[idx+1] = id
	}
	for i := 2; i < len(ids)-1; i++ { // skip the two virtual bounds
		if Compare(ids[i-1], ids[i]) >= 0 {
			t.Fatalf("identifiers out of order at %d", i)
		}
	}
}

// TestDenseAlternatingInsert is the acceptance boundary: 1000 dense
// alternating inserts at the head and the tail of the document. Identifier
// depth must grow smoothly (logarithmically), not explode.
func TestDenseAlternatingInsert(t *testing.T) {
	s := New(1, rand.New(rand.NewSource(42)))
	const n = 1000
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			s.Insert(0, 'A') // dense typing at the beginning
		} else {
			s.Insert(s.Len(), 'Z') // dense typing at the end
		}
	}
	if !s.Sorted() {
		t.Fatal("sequence identifiers are not strictly ordered")
	}
	want := strings.Repeat("A", n/2) + strings.Repeat("Z", n/2)
	if got := s.String(); got != want {
		t.Fatalf("unexpected document content (len=%d)", len(got))
	}
	maxDepth, avgDepth := s.DepthStats()
	t.Logf("after %d dense alternating inserts: maxDepth=%d avgDepth=%.2f", n, maxDepth, avgDepth)
	// A naive fixed-base scheme needs depth >= log_base(n) at one level and
	// degrades linearly under monotonic typing; LSEQ keeps it logarithmic.
	// For 1000 edits anything below 16 levels (<= 2 bytes per digit) is
	// smooth; the observed value is typically 4-6.
	if maxDepth > 16 {
		t.Fatalf("identifier depth exploded: maxDepth=%d", maxDepth)
	}
	if avgDepth > 8 {
		t.Fatalf("average identifier depth too high: %.2f", avgDepth)
	}
}

// TestConcurrentMergeCausalOrder simulates several nodes typing concurrently
// without synchronization, then merges the full operation log into fresh
// replicas under many random permutations. Every replica must converge to
// the same sequence, and each node's characters must appear in the exact
// order that node typed them (strict causal order).
func TestConcurrentMergeCausalOrder(t *testing.T) {
	const nodes = 4
	words := []string{"alpha", "BETA", "gamma!", "Delta?"}
	rng := rand.New(rand.NewSource(7))

	replicas := make([]*Sequence, nodes)
	var ops []Op
	for i := 0; i < nodes; i++ {
		replicas[i] = New(uint64(i+1), rand.New(rand.NewSource(int64(100+i))))
		for _, r := range words[i] {
			// each node appends its own word locally, no sync yet
			ops = append(ops, replicas[i].Insert(replicas[i].Len(), r))
		}
	}

	// reference merge: causal (per-site FIFO) delivery
	reference := New(999, nil)
	for _, op := range ops {
		reference.Apply(op)
	}
	if !reference.Sorted() {
		t.Fatal("reference replica is not sorted")
	}

	// random full-permutation merges must all converge to the same document
	for trial := 0; trial < 64; trial++ {
		perm := rng.Perm(len(ops))
		replica := New(1000+uint64(trial), nil)
		for _, j := range perm {
			replica.Apply(ops[j])
		}
		if got := replica.String(); got != reference.String() {
			t.Fatalf("trial %d: divergent merge:\n got %q\nwant %q", trial, got, reference.String())
		}
		if !replica.Sorted() {
			t.Fatalf("trial %d: replica not sorted", trial)
		}
	}

	// strict causal order: every node's word survives as an in-order
	// subsequence of the merged document
	doc := reference.String()
	for i, w := range words {
		if !isSubsequence(doc, w) {
			t.Fatalf("causal order violated for node %d: %q is not an in-order subsequence of %q", i, w, doc)
		}
	}
	t.Logf("merged document: %q", doc)
}

// TestCausalDeliveryWithDeletes mixes inserts and deletes across nodes and
// delivers operations in a random order that still respects per-site
// causality (the standard causal-broadcast assumption). All replicas must
// converge.
func TestCausalDeliveryWithDeletes(t *testing.T) {
	const nodes = 3
	rng := rand.New(rand.NewSource(99))

	replicas := make([]*Sequence, nodes)
	opsBySite := make([][]Op, nodes)
	for i := 0; i < nodes; i++ {
		replicas[i] = New(uint64(i+1), rand.New(rand.NewSource(int64(50+i))))
	}
	// phase 1: everyone inserts concurrently
	for round := 0; round < 30; round++ {
		for i := 0; i < nodes; i++ {
			r := replicas[i]
			op := r.Insert(rng.Intn(r.Len()+1), rune('a'+i))
			opsBySite[i] = append(opsBySite[i], op)
		}
	}
	// phase 2: everyone deletes some of its visible elements
	for i := 0; i < nodes; i++ {
		r := replicas[i]
		for k := 0; k < 10 && r.Len() > 0; k++ {
			op := r.Delete(rng.Intn(r.Len()))
			opsBySite[i] = append(opsBySite[i], op)
		}
	}

	// reference: all operations applied in causal order to one replica
	reference := New(9999, nil)
	for i := range opsBySite {
		for _, op := range opsBySite[i] {
			reference.Apply(op)
		}
	}
	want := reference.String()
	// deliver to fresh replicas in random causal (per-site FIFO) order
	for trial := 0; trial < 32; trial++ {
		fresh := New(5000+uint64(trial), nil)
		pos := make([]int, nodes)
		remaining := 0
		for i := range opsBySite {
			remaining += len(opsBySite[i])
		}
		for remaining > 0 {
			i := rng.Intn(nodes)
			if pos[i] >= len(opsBySite[i]) {
				continue
			}
			fresh.Apply(opsBySite[i][pos[i]])
			pos[i]++
			remaining--
		}
		if got := fresh.String(); got != want {
			t.Fatalf("trial %d: divergence after causal delivery:\n got %q\nwant %q", trial, got, want)
		}
	}
}

func isSubsequence(doc, word string) bool {
	i := 0
	for _, r := range doc {
		if i < len(word) && r == rune(word[i]) {
			i++
		}
	}
	return i == len(word)
}
