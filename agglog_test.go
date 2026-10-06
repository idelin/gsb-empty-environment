package agglog

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// model is the reference per-event set model: events are applied one at
// a time, each id takes effect at most once, and consistent replays are
// no-ops. Batch application must always converge to the same state.
type model struct {
	totals  map[string]int64
	applied map[string]int64
}

func newModel() *model {
	return &model{
		totals:  make(map[string]int64),
		applied: make(map[string]int64),
	}
}

func (m *model) apply(e Event) {
	if _, ok := m.applied[e.ID]; ok {
		return
	}
	m.applied[e.ID] = e.Delta
	m.totals[e.Key] += e.Delta
}

func applyBatchToBoth(t *testing.T, l *Log, m *model, b Batch) {
	t.Helper()
	if err := l.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch(%+v) failed: %v", b, err)
	}
	for _, e := range Unpack(b) {
		m.apply(e)
	}
}

func assertMatchesModel(t *testing.T, l *Log, m *model) {
	t.Helper()
	if !reflect.DeepEqual(l.totals, m.totals) {
		t.Fatalf("totals diverged: log=%v model=%v", l.totals, m.totals)
	}
	if !reflect.DeepEqual(l.applied, m.applied) {
		t.Fatalf("applied sets diverged: log=%v model=%v", l.applied, m.applied)
	}
}

func TestPackPreservesOrder(t *testing.T) {
	events := []Event{
		{ID: "e3", Key: "k", Delta: 1},
		{ID: "e1", Key: "k", Delta: -2},
		{ID: "e2", Key: "k", Delta: 5},
	}
	b, err := Pack(events)
	if err != nil {
		t.Fatal(err)
	}
	if b.Key != "k" {
		t.Fatalf("key = %q, want k", b.Key)
	}
	for i, e := range events {
		if b.Entries[i].EventID != e.ID || b.Entries[i].Delta != e.Delta {
			t.Fatalf("entry %d = %+v, want id %q delta %d", i, b.Entries[i], e.ID, e.Delta)
		}
	}
	if got := Unpack(b); !reflect.DeepEqual(got, events) {
		t.Fatalf("Unpack(Pack(events)) = %v, want %v", got, events)
	}
}

func TestPackRejectsMixedKeysAndEmpty(t *testing.T) {
	if _, err := Pack(nil); err == nil {
		t.Fatal("Pack(nil) succeeded, want error")
	}
	mixed := []Event{{ID: "e1", Key: "a", Delta: 1}, {ID: "e2", Key: "b", Delta: 1}}
	if _, err := Pack(mixed); err == nil {
		t.Fatal("Pack with mixed keys succeeded, want error")
	}
}

func TestApplyBatchAppliesOnlyUnseen(t *testing.T) {
	l := New()
	if err := l.ApplyEvent(Event{ID: "e1", Key: "k", Delta: 2}); err != nil {
		t.Fatal(err)
	}
	b := Batch{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 2}, // already applied: skip
		{EventID: "e2", Delta: 3},
		{EventID: "e3", Delta: 4},
	}}
	if err := l.ApplyBatch(b); err != nil {
		t.Fatal(err)
	}
	if got := l.Total("k"); got != 9 {
		t.Fatalf("total = %d, want 9", got)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if _, ok := l.Applied(id); !ok {
			t.Fatalf("event %q not marked applied", id)
		}
	}
}

func TestApplyBatchReplayIsNoOp(t *testing.T) {
	l := New()
	b := Batch{Key: "k", Entries: []Entry{{EventID: "e1", Delta: 2}, {EventID: "e2", Delta: 3}}}
	for i := 0; i < 3; i++ {
		if err := l.ApplyBatch(b); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.Total("k"); got != 5 {
		t.Fatalf("total after replays = %d, want 5", got)
	}
}

func TestApplyBatchConflictIsAtomic(t *testing.T) {
	l := New()
	if err := l.ApplyEvent(Event{ID: "e1", Key: "k", Delta: 5}); err != nil {
		t.Fatal(err)
	}
	err := l.ApplyBatch(Batch{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 7}, // conflicts with applied delta 5
		{EventID: "e2", Delta: 3}, // unseen, must not be applied
	}})
	if err == nil {
		t.Fatal("ApplyBatch succeeded, want conflict")
	}
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if ce.EventID != "e1" || ce.Existing != 5 || ce.Incoming != 7 {
		t.Fatalf("unexpected conflict detail: %+v", ce)
	}
	if got := l.Total("k"); got != 5 {
		t.Fatalf("total changed after rejected batch: %d, want 5", got)
	}
	if _, ok := l.Applied("e2"); ok {
		t.Fatal("e2 applied despite batch rejection")
	}
}

func TestApplyBatchIntraBatchConflict(t *testing.T) {
	l := New()
	err := l.ApplyBatch(Batch{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 1},
		{EventID: "e1", Delta: 2},
	}})
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("error = %v, want *ConflictError", err)
	}
	if got := l.Total("k"); got != 0 {
		t.Fatalf("total = %d, want 0 after rejected batch", got)
	}
}

func TestBatchEqualsUnpackedEventStream(t *testing.T) {
	b := Batch{Key: "k", Entries: []Entry{
		{EventID: "e1", Delta: 1},
		{EventID: "e2", Delta: 2},
		{EventID: "e3", Delta: 3},
	}}
	fromBatch := New()
	if err := fromBatch.ApplyBatch(b); err != nil {
		t.Fatal(err)
	}
	fromEvents := New()
	for _, e := range Unpack(b) {
		if err := fromEvents.ApplyEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(fromBatch.totals, fromEvents.totals) ||
		!reflect.DeepEqual(fromBatch.applied, fromEvents.applied) {
		t.Fatal("batch application diverged from ordered per-event application")
	}
}

// TestBatchMatchesPerEventModel packs random same-key runs into batches
// and applies them with random replays of earlier batches. Because each
// id carries one fixed delta per trial, every overlap is a consistent
// replay, and the log must converge to the per-event set model.
func TestBatchMatchesPerEventModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c"}
	for trial := 0; trial < 200; trial++ {
		// Fix one (key, delta) per id so overlaps are consistent replays.
		deltas := make(map[string]int64)
		idKeys := make(map[string]string)
		var stream []Event
		for i, n := 0, 1+rng.Intn(40); i < n; i++ {
			id := fmt.Sprintf("e%d", rng.Intn(12))
			if _, ok := deltas[id]; !ok {
				deltas[id] = int64(rng.Intn(9) + 1)
				idKeys[id] = keys[rng.Intn(len(keys))]
			}
			stream = append(stream, Event{ID: id, Key: idKeys[id], Delta: deltas[id]})
		}
		// Compress consecutive same-key runs into batches.
		var batches []Batch
		for i := 0; i < len(stream); {
			j := i + 1
			for j < len(stream) && stream[j].Key == stream[i].Key {
				j++
			}
			b, err := Pack(stream[i:j])
			if err != nil {
				t.Fatal(err)
			}
			batches = append(batches, b)
			i = j
		}
		l := New()
		m := newModel()
		for i, b := range batches {
			applyBatchToBoth(t, l, m, b)
			if rng.Intn(3) == 0 { // replay a random earlier batch
				applyBatchToBoth(t, l, m, batches[rng.Intn(i+1)])
			}
		}
		assertMatchesModel(t, l, m)
	}
}

// TestBatchOrderDoesNotMatter shuffles disjoint-id batches: the final
// state is a pure function of the applied id set, independent of the
// order in which batches arrive and are unpacked.
func TestBatchOrderDoesNotMatter(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	keys := []string{"a", "b", "c"}
	for trial := 0; trial < 100; trial++ {
		var batches []Batch
		m := newModel()
		for bi := 0; bi < 5; bi++ {
			key := keys[rng.Intn(len(keys))]
			var events []Event
			for i, n := 0, 1+rng.Intn(5); i < n; i++ {
				e := Event{
					ID:    fmt.Sprintf("t%d-b%d-e%d", trial, bi, i),
					Key:   key,
					Delta: int64(rng.Intn(9) + 1),
				}
				events = append(events, e)
				m.apply(e)
			}
			b, err := Pack(events)
			if err != nil {
				t.Fatal(err)
			}
			batches = append(batches, b)
		}
		l := New()
		for _, idx := range rng.Perm(len(batches)) {
			if err := l.ApplyBatch(batches[idx]); err != nil {
				t.Fatal(err)
			}
		}
		assertMatchesModel(t, l, m)
	}
}
