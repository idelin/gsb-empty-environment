package netsec

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// allPartitions enumerates every set partition of n elements as blocks of
// element indices using restricted growth strings: a[0]=0 and each next value
// is at most one greater than the maximum seen so far.
func allPartitions(n int) [][][]int {
	var partitions [][][]int

	var walk func(pos, maxBlock int, blocks [][]int)
	walk = func(pos, maxBlock int, blocks [][]int) {
		if pos == n {
			frozen := make([][]int, len(blocks))
			for i, b := range blocks {
				frozen[i] = append([]int(nil), b...)
			}
			partitions = append(partitions, frozen)
			return
		}

		for block := 0; block <= maxBlock+1; block++ {
			next := make([][]int, len(blocks))
			for i, b := range blocks {
				next[i] = append([]int(nil), b...)
			}
			if block == len(blocks) {
				next = append(next, []int{pos})
			} else {
				next[block] = append(next[block], pos)
			}
			nextMax := maxBlock
			if block > nextMax {
				nextMax = block
			}
			walk(pos+1, nextMax, next)
		}
	}

	walk(1, 0, [][]int{{0}})
	return partitions
}

func sameResult(a, b Result) bool {
	return reflect.DeepEqual(a, b)
}

type op struct {
	kind     int // 0 = ingest, 1 = withdraw
	record   Record
	withdraw string
}

// TestMergeExhaustivePartitions distributes one fixed collection across shards
// in every possible set partition, with retransmissions, withdrawals and
// shuffled ingest order, and asserts that merging shards and trimming yields
// exactly the same Result (including trimming reasons) as the full collection.
func TestMergeExhaustivePartitions(t *testing.T) {
	const localK, globalCap = 2, 3

	active := []Record{
		rec("e1", "u1", "gA", 5, "p1"),
		rec("e2", "u1", "gA", 5, "p2"),
		rec("e3", "u1", "gA", 5, "p3"),
		rec("e4", "u1", "gB", 5, "p4"),
		rec("e5", "u1", "gB", 5, "p5"),
		rec("e6", "u1", "gC", 5, "p6"),
		rec("e7", "u1", "gC", 5, "p7"),
		rec("e8", "u2", "gA", 5, "p8"),
	}
	withdrawIDs := []string{"e2", "e6"}

	// streams[i] holds the delivery stream for element i: its record,
	// optionally a duplicate retransmission, and optionally a withdrawal.
	// Order inside a shard is shuffled later to model out-of-order transport.
	streams := make([][]op, len(active))
	for i, r := range active {
		streams[i] = []op{{record: r}}
	}
	streams[0] = append(streams[0], op{record: active[0]})                 // retransmit e1
	streams[3] = append(streams[3], op{record: active[3]})                 // retransmit e4
	streams[1] = append(streams[1], op{kind: 1, withdraw: withdrawIDs[0]}) // withdraw e2
	streams[5] = append(streams[5], op{kind: 1, withdraw: withdrawIDs[1]}) // withdraw e6

	// Reference: one selector seeing the complete, shuffled collection.
	reference := func(rng *rand.Rand) Result {
		ref, err := NewSelector(testKey, localK, globalCap)
		if err != nil {
			t.Fatalf("reference NewSelector: %v", err)
		}
		var flatIngests []Record
		for _, s := range streams {
			for _, operation := range s {
				if operation.kind == 0 {
					flatIngests = append(flatIngests, operation.record)
				}
			}
		}
		rng.Shuffle(len(flatIngests), func(i, j int) { flatIngests[i], flatIngests[j] = flatIngests[j], flatIngests[i] })
		for _, r := range flatIngests {
			if err := ref.Ingest(r); err != nil {
				t.Fatalf("reference Ingest %s: %v", r.EventID, err)
			}
		}
		withdrawOrder := append([]string(nil), withdrawIDs...)
		rng.Shuffle(len(withdrawOrder), func(i, j int) { withdrawOrder[i], withdrawOrder[j] = withdrawOrder[j], withdrawOrder[i] })
		for _, id := range withdrawOrder {
			ref.Withdraw(id)
		}
		return ref.Trim()
	}

	partitions := allPartitions(len(active))
	if got, want := len(partitions), bellNumber(len(active)); got != want {
		t.Fatalf("partition count = %d, want Bell(%d)=%d", got, len(active), want)
	}

	seed := int64(101)
	for pi, partition := range partitions {
		rng := rand.New(rand.NewSource(seed + int64(pi)))

		shards := make([]*Selector, 0, len(partition))
		for _, block := range partition {
			shard, err := NewSelector(testKey, localK, globalCap)
			if err != nil {
				t.Fatalf("shard NewSelector: %v", err)
			}
			var shardOps []op
			for _, idx := range block {
				shardOps = append(shardOps, streams[idx]...)
			}

			var ingestOps []Record
			var withdrawOps []string
			for _, operation := range shardOps {
				if operation.kind == 1 {
					withdrawOps = append(withdrawOps, operation.withdraw)
				} else {
					ingestOps = append(ingestOps, operation.record)
				}
			}
			rng.Shuffle(len(ingestOps), func(i, j int) { ingestOps[i], ingestOps[j] = ingestOps[j], ingestOps[i] })
			rng.Shuffle(len(withdrawOps), func(i, j int) { withdrawOps[i], withdrawOps[j] = withdrawOps[j], withdrawOps[i] })
			for _, r := range ingestOps {
				if err := shard.Ingest(r); err != nil {
					t.Fatalf("shard Ingest %s: %v", r.EventID, err)
				}
			}
			for _, id := range withdrawOps {
				shard.Withdraw(id)
			}
			shards = append(shards, shard)
		}

		merged, err := Merge(shards...)
		if err != nil {
			t.Fatalf("partition %v Merge: %v", partition, err)
		}

		refRng := rand.New(rand.NewSource(seed + 10_000 + int64(pi)))
		want := reference(refRng)
		got := merged.Trim()
		if !sameResult(got, want) {
			t.Fatalf("partition %v mismatch:\n merged  = %+v\n want    = %+v", partition, got, want)
		}

		// The merged state must itself be equivalent to one big selector that
		// received the same stream, including tombstone accounting.
		if merged.Len() != len(active) {
			t.Fatalf("partition %v: merged Len = %d, want %d", partition, merged.Len(), len(active))
		}
		for _, id := range withdrawIDs {
			if !merged.IsWithdrawn(id) {
				t.Fatalf("partition %v: tombstone for %s lost in merge", partition, id)
			}
		}
	}

	// Cross-check against the stateless reference on active records only.
	stateless, err := TrimRecords(testKey, active, localK, globalCap)
	if err != nil {
		t.Fatalf("TrimRecords: %v", err)
	}
	full, _ := NewSelector(testKey, localK, globalCap)
	for _, r := range active {
		if err := full.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}
	if !sameResult(full.Trim(), stateless) {
		t.Fatalf("stateful and stateless trimming diverged:\n %+v\n %+v", full.Trim(), stateless)
	}
}

func bellNumber(n int) int {
	row := []int{1}
	for i := 1; i <= n; i++ {
		next := make([]int, i+1)
		next[0] = row[i-1]
		for k := 1; k <= i; k++ {
			next[k] = next[k-1] + row[k-1]
		}
		row = next
	}
	return row[0]
}

func TestMergeConflictAndConfig(t *testing.T) {
	s1, _ := NewSelector(testKey, 2, 2)
	s2, _ := NewSelector(testKey, 2, 2)
	if err := s1.Ingest(rec("e1", "u1", "gA", 1, "from-shard-1")); err != nil {
		t.Fatal(err)
	}
	if err := s2.Ingest(rec("e1", "u1", "gA", 1, "from-shard-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(s1, s2); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-shard conflict: got %v, want ErrConflict", err)
	}

	otherKey, _ := NewSelector([]byte("different-key"), 2, 2)
	if _, err := Merge(s1, otherKey); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("key mismatch: got %v, want ErrInvalidConfig", err)
	}
	otherCap, _ := NewSelector(testKey, 1, 2)
	if _, err := Merge(s1, otherCap); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("cap mismatch: got %v, want ErrInvalidConfig", err)
	}
	if _, err := Merge(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty merge: got %v, want ErrInvalidConfig", err)
	}
}

func TestMergeIdempotentDuplicates(t *testing.T) {
	r := rec("e1", "u1", "gA", 1, "shared")
	s1, _ := NewSelector(testKey, 2, 2)
	s2, _ := NewSelector(testKey, 2, 2)
	s3, _ := NewSelector(testKey, 2, 2)
	if err := s1.Ingest(r); err != nil {
		t.Fatal(err)
	}
	if err := s2.Ingest(r); err != nil {
		t.Fatal(err)
	}

	merged, err := Merge(s1, s2, s3)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Len() != 1 {
		t.Fatalf("Len = %d, want 1", merged.Len())
	}
	res := merged.Trim()
	assertIDs(t, "kept", res.Kept, []string{"e1"})
}

func TestCloneIsIndependent(t *testing.T) {
	s, _ := NewSelector(testKey, 1, 1)
	if err := s.Ingest(rec("e1", "u1", "gA", 1, "p1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(rec("e2", "u1", "gA", 1, "p2")); err != nil {
		t.Fatal(err)
	}

	clone := s.Clone()
	s.Withdraw("e1")
	if clone.IsWithdrawn("e1") {
		t.Fatal("clone must not observe mutations of the original")
	}
	if !s.IsWithdrawn("e1") {
		t.Fatal("original withdrawal missing")
	}

	cloneRes := clone.Trim()
	originRes := s.Trim()
	if sameResult(cloneRes, originRes) {
		t.Fatalf("clone and original unexpectedly identical after divergence: %+v", cloneRes)
	}
}
