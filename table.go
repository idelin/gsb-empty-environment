package freqsketch

import (
	"sort"
	"sync"
)

// Entry is one admitted key in the heavy-hitter table.
type Entry struct {
	Key   string
	Bytes int
	Freq  uint64 // sketch estimate at admission/refresh time; an upper bound
}

// TableState is a checkpoint of a Table.
type TableState struct {
	Entries map[string]Entry
	Used    int
}

// Table is an admission-controlled heavy-hitter table with a byte
// budget. A candidate is admitted only if its sketch estimate beats the
// entry that would be evicted to make room. Admission, eviction and
// budget accounting happen atomically under one lock.
type Table struct {
	mu      sync.Mutex
	sketch  *Sketch
	budget  int
	used    int
	entries map[string]*Entry
}

func NewTable(sk *Sketch, byteBudget int) *Table {
	if byteBudget < 0 {
		byteBudget = 0
	}
	return &Table{sketch: sk, budget: byteBudget, entries: map[string]*Entry{}}
}

// Admit tries to admit key occupying size bytes. It returns true if the
// key is in the table after the call. The decision is atomic: the
// candidate's estimate is compared against the lowest-frequency
// occupant(s); victims are evicted only if the candidate wins, and the
// byte budget is updated in the same critical section.
func (t *Table) Admit(key string, size int) bool {
	if size < 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok {
		e.Freq = t.sketch.Estimate(key)
		return true
	}
	if size > t.budget {
		return false
	}
	est := t.sketch.Estimate(key)

	// Phase 1: decide which victims the candidate would evict. If any
	// required victim beats the candidate, reject without touching state.
	evict := map[string]bool{}
	used := t.used
	for used+size > t.budget {
		v := lowestExcluding(t.entries, evict)
		if v == nil || v.Freq > est {
			return false
		}
		evict[v.Key] = true
		used -= v.Bytes
	}

	// Phase 2: apply eviction + admission + budget accounting atomically.
	for k := range evict {
		t.used -= t.entries[k].Bytes
		delete(t.entries, k)
	}
	t.entries[key] = &Entry{Key: key, Bytes: size, Freq: est}
	t.used += size
	return true
}

// lowestExcluding returns the occupant with the smallest Freq, breaking
// ties by smallest key so decisions are deterministic and reproducible
// across checkpoint restore.
func lowestExcluding(entries map[string]*Entry, skip map[string]bool) *Entry {
	var low *Entry
	for _, e := range entries {
		if skip[e.Key] {
			continue
		}
		if low == nil || e.Freq < low.Freq || (e.Freq == low.Freq && e.Key < low.Key) {
			low = e
		}
	}
	return low
}

// Used returns the bytes currently held by the table. Invariant:
// 0 <= Used() <= budget.
func (t *Table) Used() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.used
}

// Len returns the number of admitted entries.
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Get returns the entry for key, if admitted.
func (t *Table) Get(key string) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok {
		return *e, true
	}
	return Entry{}, false
}

// Entries returns all entries sorted by Freq descending, then Key.
func (t *Table) Entries() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Freq != out[j].Freq {
			return out[i].Freq > out[j].Freq
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Snapshot captures the table state.
func (t *Table) Snapshot() TableState {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := TableState{Entries: make(map[string]Entry, len(t.entries)), Used: t.used}
	for k, e := range t.entries {
		st.Entries[k] = *e
	}
	return st
}

// Restore replaces the table state with a previous snapshot.
func (t *Table) Restore(st TableState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = make(map[string]*Entry, len(st.Entries))
	for k, e := range st.Entries {
		cp := e
		t.entries[k] = &cp
	}
	t.used = st.Used
}
