package evsched

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// The reference model is a plain versioned dictionary: each version handle
// maps to an immutable snapshot of (live entries, per-id revision counters),
// and the observable state of a version is its entries sorted by (time, id).
// Revision counters survive cancellation, matching the library. The library
// under test must agree with this model on every operation.

type mEntry struct {
	rev  uint64
	time int64
	data string
}

type mVersion struct {
	entries map[string]mEntry
	revs    map[string]uint64
}

type model struct {
	versions map[int]mVersion
	next     int
}

func newModel() *model {
	m := &model{versions: make(map[int]mVersion)}
	m.versions[0] = mVersion{entries: map[string]mEntry{}, revs: map[string]uint64{}}
	m.next = 1
	return m
}

func cloneVersion(v mVersion) mVersion {
	entries := make(map[string]mEntry, len(v.entries))
	for k, e := range v.entries {
		entries[k] = e
	}
	revs := make(map[string]uint64, len(v.revs))
	for k, r := range v.revs {
		revs[k] = r
	}
	return mVersion{entries: entries, revs: revs}
}

func (m *model) fork(src int) int {
	h := m.next
	m.next++
	m.versions[h] = cloneVersion(m.versions[src])
	return h
}

func (m *model) upsert(h int, id string, time int64, data string) {
	v := cloneVersion(m.versions[h])
	rev := v.revs[id] + 1
	v.revs[id] = rev
	v.entries[id] = mEntry{rev: rev, time: time, data: data}
	m.versions[h] = v
}

func (m *model) cancel(h int, id string) bool {
	v := m.versions[h]
	if _, ok := v.entries[id]; !ok {
		return false
	}
	nv := cloneVersion(v)
	delete(nv.entries, id)
	m.versions[h] = nv
	return true
}

func (m *model) pop(h int) (Event, bool) {
	v := m.versions[h]
	if len(v.entries) == 0 {
		return Event{}, false
	}
	best := ""
	for id := range v.entries {
		if best == "" || less(v.entries[id], id, v.entries[best], best) {
			best = id
		}
	}
	e := v.entries[best]
	nv := cloneVersion(v)
	delete(nv.entries, best)
	m.versions[h] = nv
	return Event{ID: best, Time: e.time, Data: e.data}, true
}

func less(a mEntry, aID string, b mEntry, bID string) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	return aID < bID
}

func (m *model) merge(a, b int) (int, error) {
	merged := cloneVersion(m.versions[a])
	for id, e := range m.versions[b].entries {
		cur, ok := merged.entries[id]
		switch {
		case !ok || e.rev > cur.rev:
			merged.entries[id] = e
		case e.rev == cur.rev:
			if e.time != cur.time || e.data != cur.data {
				return 0, &ConflictError{ID: id, Rev: e.rev}
			}
		}
	}
	for id, r := range m.versions[b].revs {
		if r > merged.revs[id] {
			merged.revs[id] = r
		}
	}
	h := m.next
	m.next++
	m.versions[h] = merged
	return h, nil
}

// sortedView is the observable state: entries sorted by (time, id).
func (m *model) sortedView(h int) []Event {
	v := m.versions[h]
	out := make([]Event, 0, len(v.entries))
	for id, e := range v.entries {
		out = append(out, Event{ID: id, Time: e.time, Data: e.data})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].ID < out[j].ID
	})
	return out
}

type handle struct {
	q *Queue
	h int
}

func checkAgainstModel(t *testing.T, name string, q *Queue, m *model, h int) {
	t.Helper()
	got := q.Snapshot()
	want := m.sortedView(h)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: snapshot mismatch\n got: %v\nwant: %v", name, got, want)
	}
	if q.Len() != len(want) {
		t.Fatalf("%s: Len = %d, want %d", name, q.Len(), len(want))
	}
	gp, gok := q.Peek()
	if gok != (len(want) > 0) {
		t.Fatalf("%s: Peek ok = %v, want %v", name, gok, len(want) > 0)
	}
	if gok && gp != want[0] {
		t.Fatalf("%s: Peek = %+v, want %+v", name, gp, want[0])
	}
}

func TestRandomizedAgainstVersionedDictModel(t *testing.T) {
	seeds := []int64{1, 7, 42, 1337, 20261007}
	for _, seed := range seeds {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := NewScheduler()
			m := newModel()
			handles := []handle{{q: s.NewQueue(), h: 0}}
			ids := []string{"a", "b", "c", "d", "e", "f"}

			for step := 0; step < 400; step++ {
				pick := handles[rng.Intn(len(handles))]
				switch rng.Intn(6) {
				case 0: // fork
					handles = append(handles, handle{q: pick.q.Fork(), h: m.fork(pick.h)})
				case 1, 2: // upsert (weighted: most common op)
					id := ids[rng.Intn(len(ids))]
					tm := int64(rng.Intn(20))
					data := string(rune('A' + rng.Intn(4)))
					pick.q.Upsert(id, tm, data)
					m.upsert(pick.h, id, tm, data)
				case 3: // cancel
					id := ids[rng.Intn(len(ids))]
					if got := pick.q.Cancel(id); got != m.cancel(pick.h, id) {
						t.Fatalf("seed %d step %d: Cancel(%q) = %v, model says %v", seed, step, id, got, !got)
					}
				case 4: // pop
					gEv, gOk := pick.q.Pop()
					wEv, wOk := m.pop(pick.h)
					if gOk != wOk || gEv != wEv {
						t.Fatalf("seed %d step %d: Pop = %+v,%v, model says %+v,%v", seed, step, gEv, gOk, wEv, wOk)
					}
				case 5: // merge two random handles into a new one
					other := handles[rng.Intn(len(handles))]
					gq, gErr := Merge(pick.q, other.q)
					wh, wErr := m.merge(pick.h, other.h)
					if (gErr != nil) != (wErr != nil) {
						t.Fatalf("seed %d step %d: Merge err = %v, model says %v", seed, step, gErr, wErr)
					}
					if gErr == nil {
						handles = append(handles, handle{q: gq, h: wh})
					} else if _, ok := gErr.(*ConflictError); !ok {
						t.Fatalf("seed %d step %d: Merge err type = %T, want *ConflictError", seed, step, gErr)
					}
				}
				// After every step, verify all live handles against the model.
				for i, hd := range handles {
					checkAgainstModel(t, "handle "+itoa(i), hd.q, m, hd.h)
				}
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
