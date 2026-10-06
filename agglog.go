// Package agglog implements aggregated increment logs: a run of
// same-key increments is packed into a single Package that lists the
// original event ids and their per-event deltas. Receivers apply
// packages idempotently against a per-event set model.
package agglog

import (
	"errors"
	"fmt"
	"sort"
)

// Event is a single increment to a key.
type Event struct {
	ID    string
	Key   string
	Delta int64
}

// Entry is one event's contribution inside a Package.
type Entry struct {
	EventID string
	Delta   int64
}

// Package aggregates same-key increments. Entries are sorted by
// EventID so a package is a canonical, order-independent summary.
type Package struct {
	Key     string
	Entries []Entry
}

// Contains reports whether the package carries event id.
func (p Package) Contains(id string) bool {
	for _, e := range p.Entries {
		if e.EventID == id {
			return true
		}
	}
	return false
}

// Delta returns the delta recorded for id, and whether id is present.
func (p Package) Delta(id string) (int64, bool) {
	for _, e := range p.Entries {
		if e.EventID == id {
			return e.Delta, true
		}
	}
	return 0, false
}

// EventIDs returns the ids carried by the package, sorted.
func (p Package) EventIDs() []string {
	ids := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		ids = append(ids, e.EventID)
	}
	sort.Strings(ids)
	return ids
}

// ErrMixedKeys is returned when events of different keys are packed.
var ErrMixedKeys = errors.New("agglog: events of different keys cannot share a package")

// ErrEmptyPack is returned when packing zero events.
var ErrEmptyPack = errors.New("agglog: cannot pack zero events")

// DuplicateError reports the same event id appearing in one pack
// run with two different deltas.
type DuplicateError struct {
	EventID       string
	First, Second int64
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("agglog: event %q packed twice with deltas %d and %d", e.EventID, e.First, e.Second)
}

// Pack aggregates a run of same-key increments into one Package.
// Repeated ids with equal deltas are folded; repeated ids with
// different deltas are rejected as a DuplicateError.
func Pack(events []Event) (Package, error) {
	if len(events) == 0 {
		return Package{}, ErrEmptyPack
	}
	key := events[0].Key
	byID := make(map[string]int64, len(events))
	for _, ev := range events {
		if ev.Key != key {
			return Package{}, ErrMixedKeys
		}
		if prev, ok := byID[ev.ID]; ok {
			if prev != ev.Delta {
				return Package{}, &DuplicateError{EventID: ev.ID, First: prev, Second: ev.Delta}
			}
			continue
		}
		byID[ev.ID] = ev.Delta
	}
	entries := make([]Entry, 0, len(byID))
	for id, d := range byID {
		entries = append(entries, Entry{EventID: id, Delta: d})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EventID < entries[j].EventID })
	return Package{Key: key, Entries: entries}, nil
}
