package blink

import (
	"bytes"
	"errors"
)

var errNilRangeFunc = errors.New("blink: nil range callback")

// Scan returns all entries with start <= key <= end, in ascending order. A nil
// start means negative infinity and a nil end means positive infinity. The
// returned keys and values are copies. If start is greater than end, Scan
// returns an empty slice.
func (t *Tree) Scan(start, end []byte) []Entry {
	var out []Entry
	_ = t.Range(start, end, func(key, value []byte) bool {
		out = append(out, Entry{Key: key, Value: value})
		return true
	})
	if out == nil {
		return []Entry{}
	}
	return out
}

// Snapshot is an alias for Scan.
func (t *Tree) Snapshot(start, end []byte) []Entry {
	return t.Scan(start, end)
}

// RangeEntries is an alias for Scan.
func (t *Tree) RangeEntries(start, end []byte) []Entry {
	return t.Scan(start, end)
}

// Range visits entries with start <= key <= end in ascending key order. A nil
// start or end denotes an unbounded side. The callback receives copies and may
// mutate the tree; no page lock is held while it runs. Returning false stops
// the scan early.
//
// The scan holds one leaf read lock at a time and advances through right links.
// A concurrent split therefore changes at most where the scan obtains the next
// page, never the ascending order of the entries it returns. Split-carried
// entries are de-duplicated by key.
func (t *Tree) Range(start, end []byte, fn func(key, value []byte) bool) error {
	if fn == nil {
		return errNilRangeFunc
	}
	if start != nil && end != nil && bytes.Compare(start, end) > 0 {
		return ErrInvalidRange
	}

	cur := t.descendLeftmost()
	if start != nil {
		cur = t.descend(start)
	}

	var (
		last     []byte
		haveLast bool
	)

	for cur != nil {
		cur.mu.RLock()
		if cur.dead {
			next := cur.rightLink
			cur.mu.RUnlock()
			cur = next
			continue
		}

		// A split can complete between descend and this lock. Catch up on the
		// right chain under the current page lock.
		if start != nil && keyExceedsHigh(start, cur.highKey) {
			next := cur.rightLink
			cur.mu.RUnlock()
			t.moveRights.Add(1)
			cur = next
			continue
		}

		next := cur.rightLink
		batch := make([]Entry, 0, len(cur.keys))
		pastEnd := false

		for i, key := range cur.keys {
			if start != nil && bytes.Compare(key, start) < 0 {
				continue
			}
			if end != nil && bytes.Compare(key, end) > 0 {
				pastEnd = true
				break
			}
			if haveLast && bytes.Compare(key, last) <= 0 {
				continue
			}
			batch = append(batch, Entry{
				Key:   cloneKey(key),
				Value: cloneValue(cur.values[i]),
			})
			last = key
			haveLast = true
		}
		cur.mu.RUnlock()

		for _, entry := range batch {
			if !fn(entry.Key, entry.Value) {
				return nil
			}
		}
		if pastEnd {
			return nil
		}
		cur = next
	}
	return nil
}

// Ascend visits every entry in ascending order. It is shorthand for Range with
// open bounds.
func (t *Tree) Ascend(fn func(key, value []byte) bool) error {
	return t.Range(nil, nil, fn)
}
