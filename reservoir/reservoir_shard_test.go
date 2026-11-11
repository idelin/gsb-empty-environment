package reservoir

import (
	"bytes"
	"strconv"
	"testing"
)

func TestCrossShardIDConflict(t *testing.T) {
	cfg := testConfig()

	// The same ID arrives in two shards with conflicting class and
	// payload. The merged reservoir must keep exactly one first-seen
	// record and account for the content conflict.
	s1, _ := New(cfg, testKey)
	s1.Add(Event{Class: "A", ID: "shared-id", Payload: []byte("sharder-one")})
	s1.Add(Event{Class: "A", ID: "a-only", Payload: []byte("x")})

	s2, _ := New(cfg, testKey)
	s2.Add(Event{Class: "B", ID: "shared-id", Payload: []byte("sharder-two")})
	s2.Add(Event{Class: "B", ID: "b-only", Payload: []byte("y")})

	if err := s1.Merge(s2); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var shared *Record
	seen := map[string]int{}
	for _, r := range s1.Snapshot() {
		seen[r.Event.ID]++
		if r.Event.ID == "shared-id" {
			cp := r
			shared = &cp
		}
	}
	if seen["shared-id"] != 1 {
		t.Fatalf("shared ID stored %d times, want 1", seen["shared-id"])
	}
	if shared == nil {
		t.Fatalf("shared ID missing after merge")
	}
	if shared.Event.Class != "A" || !bytes.Equal(shared.Event.Payload, []byte("sharder-one")) {
		t.Fatalf("first-seen content lost: %#v", shared.Event)
	}
	if stats := s1.Stats(); stats.ContentConflicts != 1 || stats.Duplicates != 1 {
		t.Fatalf("merge conflict stats wrong: %+v", stats)
	}

	// Reverse the merge order: the other shard's content is first-seen.
	r1, _ := New(cfg, testKey)
	r1.Add(Event{Class: "A", ID: "shared-id", Payload: []byte("sharder-one")})
	r1.Add(Event{Class: "A", ID: "a-only", Payload: []byte("x")})
	r2, _ := New(cfg, testKey)
	r2.Add(Event{Class: "B", ID: "shared-id", Payload: []byte("sharder-two")})
	r2.Add(Event{Class: "B", ID: "b-only", Payload: []byte("y")})
	if err := r2.Merge(r1); err != nil {
		t.Fatalf("reverse Merge: %v", err)
	}
	for _, r := range r2.Snapshot() {
		if r.Event.ID == "shared-id" &&
			(!bytes.Equal(r.Event.Payload, []byte("sharder-two")) || r.Event.Class != "B") {
			t.Fatalf("reverse-order first-seen content lost: %#v", r.Event)
		}
	}

	// Both orders retain the same ID set; only the conflicting content
	// follows first-seen, which is determined by explicit merge order.
	idSet := func(records []Record) map[string]bool {
		out := map[string]bool{}
		for _, r := range records {
			out[r.Event.ID] = true
		}
		return out
	}
	if !sameIDSet(idSet(s1.Snapshot()), idSet(r2.Snapshot())) {
		t.Fatalf("merge order changed the retained ID set")
	}
}

func sameIDSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func TestLargeStreamBoundedAndSelective(t *testing.T) {
	cfg := Config{Capacity: 6, MinQuota: map[string]int{"A": 2, "B": 1, "C": 1}}
	s, _ := New(cfg, testKey)

	// 10,000 noisy events across five classes, plus protected rare ones.
	for i := 0; i < 10000; i++ {
		class := []string{"A", "A", "A", "D", "E"}[i%5]
		s.Add(Event{Class: class, ID: "evt-" + strconv.Itoa(i), Payload: []byte("p")})
	}
	s.Add(Event{Class: "B", ID: "rare-b", Payload: []byte("b")})
	s.Add(Event{Class: "C", ID: "rare-c", Payload: []byte("c")})

	records := s.Snapshot()
	if len(records) != cfg.Capacity {
		t.Fatalf("retained = %d, want %d", len(records), cfg.Capacity)
	}
	classCount := map[string]int{}
	ids := map[string]bool{}
	var priorities [][]byte
	for _, r := range records {
		if ids[r.Event.ID] {
			t.Fatalf("duplicate retained ID %s", r.Event.ID)
		}
		ids[r.Event.ID] = true
		classCount[r.Event.Class]++
		priorities = append(priorities, r.Priority)
	}
	if classCount["B"] != 1 || classCount["C"] != 1 || classCount["A"] < 2 {
		t.Fatalf("quota not honored on large stream: %v", classCount)
	}
	for i := 1; i < len(priorities); i++ {
		if bytes.Compare(priorities[i], priorities[i-1]) > 0 {
			t.Fatalf("snapshot not priority-sorted")
		}
	}

	// Buffered state remains bounded by the per-class limits.
	for class, buf := range s.candidates {
		if len(buf) > s.classLimit(class) {
			t.Fatalf("class %s exceeded buffer limit", class)
		}
	}
	if s.Stats().EventsSeen != 10002 {
		t.Fatalf("EventsSeen = %d, want 10002", s.Stats().EventsSeen)
	}
}
