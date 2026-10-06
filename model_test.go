package eventsched

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// The reference model is a plain versioned dictionary per branch; ordering
// answers come from sorting the dictionary. It has no queues, no laziness,
// and no structural sharing, so any disagreement with Branch is a bug in
// the implementation (stale-entry handling or merge logic).

type modelEvent struct {
	time    int64
	payload string
	rev     int
}

type modelBranch struct {
	m map[string]modelEvent
}

func newModel() modelBranch { return modelBranch{m: map[string]modelEvent{}} }

func (mb modelBranch) clone() modelBranch {
	out := modelBranch{m: make(map[string]modelEvent, len(mb.m))}
	for k, v := range mb.m {
		out.m[k] = v
	}
	return out
}

func (mb modelBranch) upsert(ev Event) {
	old := mb.m[ev.ID]
	mb.m[ev.ID] = modelEvent{time: ev.Time, payload: ev.Payload, rev: old.rev + 1}
}

func (mb modelBranch) cancel(id string) { delete(mb.m, id) }

func (mb modelBranch) sorted() []Event {
	out := make([]Event, 0, len(mb.m))
	for id, e := range mb.m {
		out = append(out, Event{ID: id, Time: e.time, Payload: e.payload})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (mb modelBranch) peek() (Event, bool) {
	s := mb.sorted()
	if len(s) == 0 {
		return Event{}, false
	}
	return s[0], true
}

func (mb modelBranch) pop() (Event, bool) {
	ev, ok := mb.peek()
	if !ok {
		return Event{}, false
	}
	delete(mb.m, ev.ID)
	return ev, true
}

func modelMerge(a, b modelBranch) (modelBranch, error) {
	out := a.clone()
	for id, eb := range b.m {
		ea, ok := out.m[id]
		switch {
		case !ok:
			out.m[id] = eb
		case eb.rev > ea.rev:
			out.m[id] = eb
		case eb.rev == ea.rev:
			if eb.time != ea.time || eb.payload != ea.payload {
				return modelBranch{}, fmt.Errorf("conflict on %q", id)
			}
		}
	}
	return out, nil
}

// TestAgainstModel drives both the implementation and the reference model
// through identical random operation sequences over a pool of forked
// branches, comparing snapshots after every step.
func TestAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	const (
		steps     = 4000
		maxBranch = 6
		idPool    = 8
	)

	impl := []Branch{New()}
	model := []modelBranch{newModel()}

	check := func(step int, op string) {
		for i := range impl {
			got := impl[i].List()
			want := model[i].sorted()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d (%s): branch %d snapshot = %+v, model wants %+v", step, op, i, got, want)
			}
			if impl[i].Len() != len(want) {
				t.Fatalf("step %d (%s): branch %d Len = %d, model wants %d", step, op, i, impl[i].Len(), len(want))
			}
		}
	}

	for step := 0; step < steps; step++ {
		bi := rng.Intn(len(impl))
		op := rng.Intn(100)
		switch {
		case op < 40: // upsert
			ev := Event{
				ID:      fmt.Sprintf("id%d", rng.Intn(idPool)),
				Time:    int64(rng.Intn(20)),
				Payload: fmt.Sprintf("p%d", rng.Intn(4)),
			}
			impl[bi] = impl[bi].Upsert(ev)
			model[bi].upsert(ev)
			check(step, "upsert")

		case op < 55: // cancel
			id := fmt.Sprintf("id%d", rng.Intn(idPool))
			impl[bi] = impl[bi].Cancel(id)
			model[bi].cancel(id)
			check(step, "cancel")

		case op < 65: // fork
			if len(impl) < maxBranch {
				impl = append(impl, impl[bi].Fork())
				model = append(model, model[bi].clone())
			}
			check(step, "fork")

		case op < 75: // peek
			gotEv, gotOK := impl[bi].Peek()
			wantEv, wantOK := model[bi].peek()
			if gotOK != wantOK || (gotOK && gotEv != wantEv) {
				t.Fatalf("step %d: peek = %+v,%v; model wants %+v,%v", step, gotEv, gotOK, wantEv, wantOK)
			}

		case op < 85: // pop
			gotEv, rest, gotOK := impl[bi].Pop()
			wantEv, wantOK := model[bi].pop()
			if gotOK != wantOK || (gotOK && gotEv != wantEv) {
				t.Fatalf("step %d: pop = %+v,%v; model wants %+v,%v", step, gotEv, gotOK, wantEv, wantOK)
			}
			impl[bi] = rest
			check(step, "pop")

		default: // merge
			bj := rng.Intn(len(impl))
			got, gotErr := Merge(impl[bi], impl[bj])
			want, wantErr := modelMerge(model[bi], model[bj])
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("step %d: merge err = %v, model wants %v", step, gotErr, wantErr)
			}
			if gotErr == nil {
				if !reflect.DeepEqual(got.List(), want.sorted()) {
					t.Fatalf("step %d: merge = %+v, model wants %+v", step, got.List(), want.sorted())
				}
				// The merged result becomes a new branch in the pool.
				if len(impl) < maxBranch {
					impl = append(impl, got)
					model = append(model, want)
				} else {
					impl[bi] = got
					model[bi] = want
				}
			}
			check(step, "merge")
		}
	}
}

// TestStaleEntriesUnderHeavyRewrite hammers a single ID with updates so the
// heap accumulates many stale entries, then verifies exact drain order.
func TestStaleEntriesUnderHeavyRewrite(t *testing.T) {
	b := New()
	for i := 0; i < 500; i++ {
		b = b.Upsert(Event{ID: "hot", Time: int64(1000 - i)})
		b = b.Upsert(Event{ID: fmt.Sprintf("cold%d", i%7), Time: int64(i % 7)})
	}
	got := b.List()
	for i := 1; i < len(got); i++ {
		if got[i-1].Time > got[i].Time ||
			(got[i-1].Time == got[i].Time && got[i-1].ID > got[i].ID) {
			t.Fatalf("List out of order at %d: %+v then %+v", i, got[i-1], got[i])
		}
	}
	seen := map[string]bool{}
	for _, ev := range got {
		if seen[ev.ID] {
			t.Fatalf("duplicate event for %q: stale entry resurfaced", ev.ID)
		}
		seen[ev.ID] = true
	}
	if len(got) != 8 { // "hot" + cold0..cold6
		t.Fatalf("len(List) = %d, want 8", len(got))
	}
}
