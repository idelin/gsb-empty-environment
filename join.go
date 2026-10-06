// Package incjoin incrementally maintains the result of a left join between
// two tables under multiset (bag) semantics.
//
// Rows with a NULL join key never match anything on the other side. Every
// insert/delete event returns the signed delta of the join result, so a
// downstream report only needs to apply those deltas. When the right side of
// a key grows from zero to one row, previously emitted null-padded rows are
// retracted; when the last right row for a key is deleted, they are restored.
package incjoin

import "fmt"

// Row is a record in either input table. HasKey == false means the join key
// is NULL: such rows never match rows on the other side.
type Row struct {
	Key     string
	HasKey  bool
	Payload string
}

// OutRow is one row of the join result. HasRight == false denotes a
// null-padded left row (no matching right row exists).
type OutRow struct {
	Left     Row
	Right    Row
	HasRight bool
}

// Delta is a signed change to the join result: Count > 0 inserts copies of
// Row, Count < 0 removes them.
type Delta struct {
	Row   OutRow
	Count int
}

// Joiner holds the incremental state of left JOIN right on key.
type Joiner struct {
	leftByKey  map[string]map[Row]int // non-null left rows, by key
	leftNull   map[Row]int            // null-key left rows
	rightByKey map[string]map[Row]int // non-null right rows, by key
	rightNull  map[Row]int            // null-key right rows (never match)
	rightCount map[string]int         // total right multiplicity per key
}

// New returns an empty Joiner.
func New() *Joiner {
	return &Joiner{
		leftByKey:  make(map[string]map[Row]int),
		leftNull:   make(map[Row]int),
		rightByKey: make(map[string]map[Row]int),
		rightNull:  make(map[Row]int),
		rightCount: make(map[string]int),
	}
}

// InsertLeft records one copy of r in the left table and returns the
// resulting join delta.
func (j *Joiner) InsertLeft(r Row) []Delta {
	if !r.HasKey {
		j.leftNull[r]++
		return []Delta{{Row: OutRow{Left: r}, Count: 1}}
	}
	addTo(j.leftByKey, r)
	if j.rightCount[r.Key] == 0 {
		return []Delta{{Row: OutRow{Left: r}, Count: 1}}
	}
	var out []Delta
	for rr, n := range j.rightByKey[r.Key] {
		out = append(out, Delta{Row: OutRow{Left: r, Right: rr, HasRight: true}, Count: n})
	}
	return out
}

// DeleteLeft removes one copy of r from the left table and returns the
// resulting join delta. It errors if r is not present.
func (j *Joiner) DeleteLeft(r Row) ([]Delta, error) {
	if !r.HasKey {
		if err := removeFromSet(j.leftNull, r, "left"); err != nil {
			return nil, err
		}
		return []Delta{{Row: OutRow{Left: r}, Count: -1}}, nil
	}
	if err := removeFrom(j.leftByKey, r, "left"); err != nil {
		return nil, err
	}
	if j.rightCount[r.Key] == 0 {
		return []Delta{{Row: OutRow{Left: r}, Count: -1}}, nil
	}
	var out []Delta
	for rr, n := range j.rightByKey[r.Key] {
		out = append(out, Delta{Row: OutRow{Left: r, Right: rr, HasRight: true}, Count: -n})
	}
	return out, nil
}

// InsertRight records one copy of r in the right table and returns the
// resulting join delta. A 0->1 transition of the key's right count retracts
// the null-padded rows previously emitted for that key.
func (j *Joiner) InsertRight(r Row) []Delta {
	if !r.HasKey {
		j.rightNull[r]++
		return nil
	}
	addTo(j.rightByKey, r)
	j.rightCount[r.Key]++
	var out []Delta
	for lr, n := range j.leftByKey[r.Key] {
		out = append(out, Delta{Row: OutRow{Left: lr, Right: r, HasRight: true}, Count: n})
	}
	if j.rightCount[r.Key] == 1 {
		for lr, n := range j.leftByKey[r.Key] {
			out = append(out, Delta{Row: OutRow{Left: lr}, Count: -n})
		}
	}
	return out
}

// DeleteRight removes one copy of r from the right table and returns the
// resulting join delta. Deleting the last right row of a key restores the
// null-padded rows for that key. It errors if r is not present.
func (j *Joiner) DeleteRight(r Row) ([]Delta, error) {
	if !r.HasKey {
		if err := removeFromSet(j.rightNull, r, "right"); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := removeFrom(j.rightByKey, r, "right"); err != nil {
		return nil, err
	}
	j.rightCount[r.Key]--
	var out []Delta
	for lr, n := range j.leftByKey[r.Key] {
		out = append(out, Delta{Row: OutRow{Left: lr, Right: r, HasRight: true}, Count: -n})
	}
	if j.rightCount[r.Key] == 0 {
		for lr, n := range j.leftByKey[r.Key] {
			out = append(out, Delta{Row: OutRow{Left: lr}, Count: n})
		}
	}
	return out, nil
}

// FullJoin computes the left join of the two tables from scratch with the
// same multiset semantics. It serves as the reference for validating the
// accumulated deltas.
func FullJoin(left, right []Row) map[OutRow]int {
	rightByKey := make(map[string]map[Row]int)
	rightCount := make(map[string]int)
	for _, r := range right {
		if !r.HasKey {
			continue
		}
		addTo(rightByKey, r)
		rightCount[r.Key]++
	}
	out := make(map[OutRow]int)
	for _, l := range left {
		if !l.HasKey || rightCount[l.Key] == 0 {
			out[OutRow{Left: l}]++
			continue
		}
		for rr, n := range rightByKey[l.Key] {
			out[OutRow{Left: l, Right: rr, HasRight: true}] += n
		}
	}
	return out
}

// Apply folds deltas into an output multiset, dropping zero entries.
func Apply(out map[OutRow]int, deltas []Delta) {
	for _, d := range deltas {
		out[d.Row] += d.Count
		if out[d.Row] == 0 {
			delete(out, d.Row)
		}
	}
}

func addTo(index map[string]map[Row]int, r Row) {
	bucket := index[r.Key]
	if bucket == nil {
		bucket = make(map[Row]int)
		index[r.Key] = bucket
	}
	bucket[r]++
}

func removeFrom(index map[string]map[Row]int, r Row, side string) error {
	bucket := index[r.Key]
	if bucket == nil || bucket[r] == 0 {
		return fmt.Errorf("incjoin: delete of absent %s row %+v", side, r)
	}
	bucket[r]--
	if bucket[r] == 0 {
		delete(bucket, r)
	}
	return nil
}

func removeFromSet(set map[Row]int, r Row, side string) error {
	if set[r] == 0 {
		return fmt.Errorf("incjoin: delete of absent %s row %+v", side, r)
	}
	set[r]--
	if set[r] == 0 {
		delete(set, r)
	}
	return nil
}
