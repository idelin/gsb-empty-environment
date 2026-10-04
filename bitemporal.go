// Package bitemporal implements an append-only bitemporal version store.
//
// Every version carries two time intervals:
//   - Valid (business time): when the fact is effective in the real world.
//   - Sys (system time): when the store learned about the fact.
//
// History is never rewritten: a correction closes the system interval of the
// old versions and appends new versions. Business intervals may be split into
// adjacent pieces. Within one system slice, versions of the same key must
// never have overlapping business intervals.
package bitemporal

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Infinity marks an open-ended interval bound.
var Infinity = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Interval is a half-open time range [From, To). To == Infinity means open-ended.
type Interval struct {
	From time.Time
	To   time.Time
}

// Contains reports whether t lies in [From, To).
func (iv Interval) Contains(t time.Time) bool {
	return !t.Before(iv.From) && t.Before(iv.To)
}

// Overlaps reports whether two intervals share any instant.
func (iv Interval) Overlaps(o Interval) bool {
	return iv.From.Before(o.To) && o.From.Before(iv.To)
}

func (iv Interval) String() string {
	return "[" + formatTime(iv.From) + ", " + formatTime(iv.To) + ")"
}

func formatTime(t time.Time) string {
	if !t.Before(Infinity) {
		return "∞"
	}
	return t.Format("2006-01-02")
}

// Version is one immutable fact about a key, stamped with both timelines.
type Version struct {
	Key   string
	Value string
	Valid Interval // business-effective interval
	Sys   Interval // system-recorded interval
}

func (v *Version) String() string {
	return fmt.Sprintf("key=%-8s valid=%-25s sys=%-25s value=%s",
		v.Key, v.Valid.String(), v.Sys.String(), v.Value)
}

// openAt reports whether the version is visible at system time t.
func (v *Version) openAt(t time.Time) bool {
	return v.Sys.Contains(t)
}

// Segment is a piece of a business timeline used as correction input.
type Segment struct {
	Valid Interval
	Value string
}

// Transaction is the audit record of one correction (or undo).
type Transaction struct {
	ID      int
	SysTime time.Time
	Note    string
	Closed  []*Version // versions whose system interval this tx closed
	Added   []*Version // versions this tx appended
	Undone  bool
}

// Store is an append-only bitemporal version store.
type Store struct {
	versions []*Version
	txs      []*Transaction
	lastSys  time.Time
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{} }

func (s *Store) requireSysTime(sysTime time.Time) error {
	if sysTime.Before(s.lastSys) {
		return fmt.Errorf("system time %s goes backwards (last: %s)",
			formatTime(sysTime), formatTime(s.lastSys))
	}
	return nil
}

// checkNoOverlap enforces: for one key, versions coexisting in any system
// slice must not have overlapping business intervals.
func (s *Store) checkNoOverlap(nv *Version) error {
	for _, e := range s.versions {
		if e.Key != nv.Key {
			continue
		}
		coexist := e.Sys.From.Before(nv.Sys.To) && e.Sys.To.After(nv.Sys.From)
		if coexist && e.Valid.Overlaps(nv.Valid) {
			return fmt.Errorf("overlap on key %q: new valid=%s conflicts with existing valid=%s sys=%s",
				nv.Key, nv.Valid, e.Valid, e.Sys)
		}
	}
	return nil
}

// Put inserts the first version of a fact, learned at sysTime.
func (s *Store) Put(key, value string, valid Interval, sysTime time.Time) (*Version, error) {
	if !valid.From.Before(valid.To) {
		return nil, fmt.Errorf("empty valid interval %s", valid)
	}
	if err := s.requireSysTime(sysTime); err != nil {
		return nil, err
	}
	nv := &Version{Key: key, Value: value, Valid: valid, Sys: Interval{From: sysTime, To: Infinity}}
	if err := s.checkNoOverlap(nv); err != nil {
		return nil, err
	}
	s.versions = append(s.versions, nv)
	s.lastSys = sysTime
	return nv, nil
}

// Correct rewrites business history for key as of sysTime: every currently
// open version whose valid interval intersects the new segments is closed
// (Sys.To = sysTime), and the segments are appended as new versions.
// Segments may split a business interval into adjacent pieces; they must not
// overlap each other or any remaining open version.
func (s *Store) Correct(key string, sysTime time.Time, segs []Segment, note string) (*Transaction, error) {
	if len(segs) == 0 {
		return nil, errors.New("correction needs at least one segment")
	}
	if err := s.requireSysTime(sysTime); err != nil {
		return nil, err
	}
	sorted := sortSegments(segs)
	for _, seg := range sorted {
		if !seg.Valid.From.Before(seg.Valid.To) {
			return nil, fmt.Errorf("empty valid interval %s", seg.Valid)
		}
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Valid.Overlaps(sorted[i].Valid) {
			return nil, fmt.Errorf("segments %s and %s overlap each other",
				sorted[i-1].Valid, sorted[i].Valid)
		}
	}

	tx := &Transaction{ID: len(s.txs) + 1, SysTime: sysTime, Note: note}

	// Close open versions touched by the corrected ranges.
	for _, v := range s.versions {
		if v.Key != key || !v.openAt(sysTime) {
			continue
		}
		for _, seg := range sorted {
			if v.Valid.Overlaps(seg.Valid) {
				v.Sys.To = sysTime
				tx.Closed = append(tx.Closed, v)
				break
			}
		}
	}

	// Validate new versions against everything that can coexist with them.
	rollback := func(err error) (*Transaction, error) {
		for _, c := range tx.Closed {
			c.Sys.To = Infinity
		}
		return nil, err
	}
	for _, seg := range sorted {
		nv := &Version{Key: key, Value: seg.Value, Valid: seg.Valid,
			Sys: Interval{From: sysTime, To: Infinity}}
		if err := s.checkNoOverlap(nv); err != nil {
			return rollback(err)
		}
		for _, prev := range tx.Added {
			if prev.Valid.Overlaps(nv.Valid) {
				return rollback(fmt.Errorf("segments %s and %s overlap each other",
					prev.Valid, nv.Valid))
			}
		}
		tx.Added = append(tx.Added, nv)
	}

	s.versions = append(s.versions, tx.Added...)
	s.txs = append(s.txs, tx)
	s.lastSys = sysTime
	return tx, nil
}

// Undo reverses transaction txID as of sysTime. It is itself recorded as a
// new transaction: versions added by txID are closed, and the versions txID
// had closed are re-appended with their original content. Undo fails if a
// later transaction has already changed the same ranges.
func (s *Store) Undo(txID int, sysTime time.Time) (*Transaction, error) {
	if txID < 1 || txID > len(s.txs) {
		return nil, fmt.Errorf("unknown transaction %d", txID)
	}
	src := s.txs[txID-1]
	if src.Undone {
		return nil, fmt.Errorf("transaction %d already undone", txID)
	}
	if err := s.requireSysTime(sysTime); err != nil {
		return nil, err
	}

	tx := &Transaction{ID: len(s.txs) + 1, SysTime: sysTime,
		Note: fmt.Sprintf("undo of tx#%d", txID)}

	rollback := func(err error) (*Transaction, error) {
		for _, c := range tx.Closed {
			c.Sys.To = Infinity
		}
		return nil, err
	}

	// Close what the original transaction added (if still open).
	for _, v := range src.Added {
		if v.Sys.To.After(sysTime) {
			v.Sys.To = sysTime
			tx.Closed = append(tx.Closed, v)
		}
	}
	// Re-append what the original transaction closed.
	for _, old := range src.Closed {
		nv := &Version{Key: old.Key, Value: old.Value, Valid: old.Valid,
			Sys: Interval{From: sysTime, To: Infinity}}
		if err := s.checkNoOverlap(nv); err != nil {
			return rollback(fmt.Errorf("cannot undo tx#%d: %w", txID, err))
		}
		tx.Added = append(tx.Added, nv)
	}

	s.versions = append(s.versions, tx.Added...)
	src.Undone = true
	s.txs = append(s.txs, tx)
	s.lastSys = sysTime
	return tx, nil
}

// AsOf answers the bitemporal query: what did we know at system time sys
// about the key's value effective at business time valid?
func (s *Store) AsOf(key string, valid, sys time.Time) (string, bool) {
	for _, v := range s.versions {
		if v.Key == key && v.Valid.Contains(valid) && v.Sys.Contains(sys) {
			return v.Value, true
		}
	}
	return "", false
}

// SliceAt returns the versions of key visible at system time sys,
// ordered by business time.
func (s *Store) SliceAt(key string, sys time.Time) []*Version {
	var out []*Version
	for _, v := range s.versions {
		if v.Key == key && v.openAt(sys) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Valid.From.Before(out[j].Valid.From) })
	return out
}

// History returns every version of key (open and closed), ordered by system
// time then business time.
func (s *Store) History(key string) []*Version {
	var out []*Version
	for _, v := range s.versions {
		if v.Key == key {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Sys.From.Equal(out[j].Sys.From) {
			return out[i].Sys.From.Before(out[j].Sys.From)
		}
		return out[i].Valid.From.Before(out[j].Valid.From)
	})
	return out
}

// Transactions returns the audit log.
func (s *Store) Transactions() []*Transaction { return s.txs }

// CheckTiling verifies that segments tile [from, to) exactly: first segment
// starts at from, each boundary is adjacent (prev.To == next.From), and the
// last segment ends at to.
func CheckTiling(segs []Segment, from, to time.Time) error {
	if len(segs) == 0 {
		return errors.New("no segments")
	}
	sorted := sortSegments(segs)
	if !sorted[0].Valid.From.Equal(from) {
		return fmt.Errorf("gap at start: first segment begins %s, want %s",
			formatTime(sorted[0].Valid.From), formatTime(from))
	}
	for i := 1; i < len(sorted); i++ {
		prev, next := sorted[i-1].Valid, sorted[i].Valid
		if !prev.To.Equal(next.From) {
			return fmt.Errorf("boundary not adjacent: %s ends %s but %s begins %s",
				prev, formatTime(prev.To), next, formatTime(next.From))
		}
	}
	if last := sorted[len(sorted)-1].Valid; !last.To.Equal(to) {
		return fmt.Errorf("gap at end: last segment ends %s, want %s",
			formatTime(last.To), formatTime(to))
	}
	return nil
}

func sortSegments(segs []Segment) []Segment {
	out := make([]Segment, len(segs))
	copy(out, segs)
	sort.Slice(out, func(i, j int) bool { return out[i].Valid.From.Before(out[j].Valid.From) })
	return out
}
