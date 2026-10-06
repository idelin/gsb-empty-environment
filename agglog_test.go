package agglog

import (
	"reflect"
	"testing"
)

func TestPackAggregatesSameKey(t *testing.T) {
	p, err := Pack([]Event{
		{ID: "e2", Key: "k", Delta: 5},
		{ID: "e1", Key: "k", Delta: 3},
		{ID: "e3", Key: "k", Delta: -2},
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	want := Package{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 3},
		{EventID: "e2", Delta: 5},
		{EventID: "e3", Delta: -2},
	}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v, want %+v", p, want)
	}
}

func TestPackFoldsRepeatedIDWithSameDelta(t *testing.T) {
	p, err := Pack([]Event{
		{ID: "e1", Key: "k", Delta: 7},
		{ID: "e1", Key: "k", Delta: 7},
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(p.Entries) != 1 || p.Entries[0].Delta != 7 {
		t.Fatalf("got %+v, want single entry delta 7", p)
	}
}

func TestPackRejectsMixedKeys(t *testing.T) {
	_, err := Pack([]Event{
		{ID: "e1", Key: "a", Delta: 1},
		{ID: "e2", Key: "b", Delta: 1},
	})
	if err != ErrMixedKeys {
		t.Fatalf("got %v, want ErrMixedKeys", err)
	}
}

func TestPackRejectsRepeatedIDWithDifferentDelta(t *testing.T) {
	_, err := Pack([]Event{
		{ID: "e1", Key: "k", Delta: 1},
		{ID: "e1", Key: "k", Delta: 2},
	})
	dup, ok := err.(*DuplicateError)
	if !ok {
		t.Fatalf("got %v, want *DuplicateError", err)
	}
	if dup.EventID != "e1" || dup.First != 1 || dup.Second != 2 {
		t.Fatalf("unexpected DuplicateError: %+v", dup)
	}
}

func TestPackRejectsEmpty(t *testing.T) {
	if _, err := Pack(nil); err != ErrEmptyPack {
		t.Fatalf("got %v, want ErrEmptyPack", err)
	}
}

func TestApplyPartiallyProcessedPackage(t *testing.T) {
	s := NewStore()
	first := Package{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 1},
		{EventID: "e2", Delta: 2},
	}}
	r1 := s.Apply(first)
	if len(r1.Applied) != 2 || !r1.OK() {
		t.Fatalf("first apply: %+v", r1)
	}

	overlapping := Package{Key: "k", Entries: []Entry{
		{EventID: "e2", Delta: 2}, // already applied
		{EventID: "e3", Delta: 3}, // new
	}}
	r2 := s.Apply(overlapping)
	if !reflect.DeepEqual(r2.Applied, []string{"e3"}) {
		t.Fatalf("applied = %v, want [e3]", r2.Applied)
	}
	if !reflect.DeepEqual(r2.Skipped, []string{"e2"}) {
		t.Fatalf("skipped = %v, want [e2]", r2.Skipped)
	}
	if got := s.Total("k"); got != 6 {
		t.Fatalf("total = %d, want 6", got)
	}
}

func TestReplayIsIdempotent(t *testing.T) {
	s := NewStore()
	p := Package{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 4},
		{EventID: "e2", Delta: 6},
	}}
	s.Apply(p)
	before := s.Total("k")
	r := s.Apply(p)
	if len(r.Applied) != 0 || len(r.Skipped) != 2 || !r.OK() {
		t.Fatalf("replay receipt: %+v", r)
	}
	if got := s.Total("k"); got != before {
		t.Fatalf("total changed on replay: %d -> %d", before, got)
	}
}

func TestOverlapWithDifferentDeltaConflicts(t *testing.T) {
	s := NewStore()
	s.Apply(Package{Key: "k", Entries: []Entry{{EventID: "e1", Delta: 10}}})

	r := s.Apply(Package{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 99}, // conflicting overlap
		{EventID: "e2", Delta: 5},  // unseen, still applied
	}})
	if r.OK() {
		t.Fatal("expected conflict")
	}
	if len(r.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", r.Conflicts)
	}
	c := r.Conflicts[0]
	if c.EventID != "e1" || c.Applied != 10 || c.Incoming != 99 {
		t.Fatalf("unexpected conflict: %+v", c)
	}
	// Original delta retained, unseen event still applied.
	if got := s.Total("k"); got != 15 {
		t.Fatalf("total = %d, want 15", got)
	}
}

func TestUnpackOrderDoesNotMatter(t *testing.T) {
	packages := []Package{
		{Key: "k", Entries: []Entry{{EventID: "e1", Delta: 1}, {EventID: "e2", Delta: 2}}},
		{Key: "k", Entries: []Entry{{EventID: "e2", Delta: 2}, {EventID: "e3", Delta: 3}}},
		{Key: "k", Entries: []Entry{{EventID: "e3", Delta: 3}, {EventID: "e1", Delta: 1}, {EventID: "e4", Delta: 4}}},
	}
	model := NewModel()
	for _, p := range packages {
		model.AddPackage(p)
	}

	perm := []int{0, 1, 2}
	for {
		s := NewStore()
		for _, i := range perm {
			if r := s.Apply(packages[i]); !r.OK() {
				t.Fatalf("perm %v: unexpected conflicts %+v", perm, r.Conflicts)
			}
		}
		if !Equivalent(s, model, "k") {
			t.Fatalf("perm %v: store diverged from model", perm)
		}
		if !nextPerm(perm) {
			break
		}
	}
}

func nextPerm(p []int) bool {
	i := len(p) - 2
	for i >= 0 && p[i] >= p[i+1] {
		i--
	}
	if i < 0 {
		return false
	}
	j := len(p) - 1
	for p[j] <= p[i] {
		j--
	}
	p[i], p[j] = p[j], p[i]
	for lo, hi := i+1, len(p)-1; lo < hi; lo, hi = lo+1, hi-1 {
		p[lo], p[hi] = p[hi], p[lo]
	}
	return true
}
