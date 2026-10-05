package snapverify

import "fmt"

// Entry is a single key/value operation in a Delta.
type Entry struct {
	Key   uint64
	Value []byte
}

// Delta declares every state change between two consecutive snapshot
// versions. The receiver only trusts the old root; everything else is
// checked against this declaration.
//
//   - Adds:     key must be empty in the old snapshot; Value is the new block.
//   - Deletes:  key must hold Value in the old snapshot; it becomes empty.
//   - Inherits: key holds Value in the old snapshot and keeps it unchanged.
//
// A key may appear at most once across all three lists, and every key must
// lie inside [RangeLo, RangeHi).
type Delta struct {
	OldVersion uint64
	NewVersion uint64
	RangeLo    uint64 // inclusive
	RangeHi    uint64 // exclusive
	Adds       []Entry
	Deletes    []Entry
	Inherits   []Entry
}

// Validate checks the structural invariants of the delta for a key space of
// 2^depth ordered IDs.
func (d *Delta) Validate(depth int) error {
	if depth < 1 || depth > 63 {
		return fmt.Errorf("snapverify: invalid tree depth %d", depth)
	}
	size := uint64(1) << uint(depth)
	if d.NewVersion != d.OldVersion+1 {
		return fmt.Errorf("snapverify: version must advance by one (old=%d new=%d)", d.OldVersion, d.NewVersion)
	}
	if d.RangeLo > d.RangeHi || d.RangeHi > size {
		return fmt.Errorf("snapverify: invalid range [%d, %d) for key space of %d", d.RangeLo, d.RangeHi, size)
	}
	seen := make(map[uint64]string)
	check := func(kind string, entries []Entry) error {
		for _, e := range entries {
			if e.Key < d.RangeLo || e.Key >= d.RangeHi {
				return fmt.Errorf("snapverify: %s key %d outside declared range [%d, %d)", kind, e.Key, d.RangeLo, d.RangeHi)
			}
			if prev, dup := seen[e.Key]; dup {
				return fmt.Errorf("snapverify: duplicate operation on key %d (already in %s)", e.Key, prev)
			}
			seen[e.Key] = kind
		}
		return nil
	}
	if err := check("adds", d.Adds); err != nil {
		return err
	}
	if err := check("deletes", d.Deletes); err != nil {
		return err
	}
	return check("inherits", d.Inherits)
}

// keys returns every key touched by the delta.
func (d *Delta) keys() []uint64 {
	out := make([]uint64, 0, len(d.Adds)+len(d.Deletes)+len(d.Inherits))
	for _, e := range d.Adds {
		out = append(out, e.Key)
	}
	for _, e := range d.Deletes {
		out = append(out, e.Key)
	}
	for _, e := range d.Inherits {
		out = append(out, e.Key)
	}
	return out
}
