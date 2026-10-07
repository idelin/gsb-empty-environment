// Package incjoin maintains the result of an incremental LEFT JOIN over two
// tables under multiset (bag) semantics.
//
// Rows are joined on a nullable string key. Following SQL semantics, NULL
// keys never match each other. Duplicate rows are tracked by multiplicity:
// a left row with count m matching a right row with count n contributes
// m*n copies of the joined output row.
//
// Every applied event returns the delta it causes on the join result, so a
// downstream report can be maintained without recomputation. When the right
// side of a key transitions from zero rows to one, previously emitted
// null-padded rows are retracted; when the last right row for a key is
// deleted, the null-padded rows are emitted again.
package incjoin

import "fmt"

// NullString is a nullable string, mirroring sql.NullString. It is a value
// type so result rows can be used as map keys.
type NullString struct {
	String string
	Valid  bool // false represents SQL NULL
}

// NS builds a non-NULL NullString.
func NS(s string) NullString { return NullString{String: s, Valid: true} }

// Row is an input row of either table. A nil Key represents SQL NULL.
type Row struct {
	Key *string
	Val string
}

// JoinRow is one output row of the left join. RVal.Valid is false for the
// null-padded row emitted when a left row has no right match.
type JoinRow struct {
	LKey NullString
	LVal string
	RVal NullString
}

// Delta is a signed multiplicity change on a join result row: positive
// Count inserts copies, negative Count retracts them.
type Delta struct {
	Row   JoinRow
	Count int
}

// key is the internal map key; valid distinguishes NULL from a real key.
type key struct {
	valid bool
	k     string
}

func keyOf(r Row) key {
	if r.Key == nil {
		return key{}
	}
	return key{valid: true, k: *r.Key}
}

func (k key) nullString() NullString {
	return NullString{String: k.k, Valid: k.valid}
}

// table is a multiset of rows grouped by join key: key -> val -> count.
type table map[key]map[string]int

func (t table) add(k key, val string, n int) {
	m, ok := t[k]
	if !ok {
		m = make(map[string]int)
		t[k] = m
	}
	m[val] += n
	if m[val] == 0 {
		delete(m, val)
	}
	if len(m) == 0 {
		delete(t, k)
	}
}

func (t table) count(k key) int {
	total := 0
	for _, c := range t[k] {
		total += c
	}
	return total
}

func (t table) has(k key, val string) bool {
	return t[k][val] > 0
}

// LeftJoin incrementally maintains LEFT JOIN results for two tables.
type LeftJoin struct {
	left  table
	right table
}

// New returns an empty incremental left join.
func New() *LeftJoin {
	return &LeftJoin{left: make(table), right: make(table)}
}

// InsertLeft applies an insertion on the left table and returns the delta.
func (j *LeftJoin) InsertLeft(r Row) []Delta {
	k := keyOf(r)
	j.left.add(k, r.Val, 1)
	return j.leftDelta(k, r.Val, 1)
}

// DeleteLeft applies a deletion on the left table and returns the delta.
// It returns an error if the row is not present.
func (j *LeftJoin) DeleteLeft(r Row) ([]Delta, error) {
	k := keyOf(r)
	if !j.left.has(k, r.Val) {
		return nil, fmt.Errorf("incjoin: delete of absent left row %+v", r)
	}
	j.left.add(k, r.Val, -1)
	return j.leftDelta(k, r.Val, -1), nil
}

// leftDelta computes the output change for one left row entering (sign=+1)
// or leaving (sign=-1). NULL keys never match, so they only ever produce
// null-padded rows.
func (j *LeftJoin) leftDelta(k key, lval string, sign int) []Delta {
	if !k.valid || j.right.count(k) == 0 {
		return []Delta{{Row: JoinRow{LKey: k.nullString(), LVal: lval}, Count: sign}}
	}
	var out []Delta
	for rval, cnt := range j.right[k] {
		out = append(out, Delta{
			Row:   JoinRow{LKey: k.nullString(), LVal: lval, RVal: NS(rval)},
			Count: sign * cnt,
		})
	}
	return out
}

// InsertRight applies an insertion on the right table and returns the delta.
func (j *LeftJoin) InsertRight(r Row) []Delta {
	k := keyOf(r)
	// A NULL key never matches; a key with no left rows yields no output.
	if !k.valid || j.left.count(k) == 0 {
		j.right.add(k, r.Val, 1)
		return nil
	}
	wasEmpty := j.right.count(k) == 0
	j.right.add(k, r.Val, 1)
	var out []Delta
	if wasEmpty {
		// 0 -> 1 transition: retract the null-padded rows for this key.
		out = append(out, j.nullPadDelta(k, -1)...)
	}
	for lval, cnt := range j.left[k] {
		out = append(out, Delta{
			Row:   JoinRow{LKey: k.nullString(), LVal: lval, RVal: NS(r.Val)},
			Count: cnt,
		})
	}
	return out
}

// DeleteRight applies a deletion on the right table and returns the delta.
// It returns an error if the row is not present.
func (j *LeftJoin) DeleteRight(r Row) ([]Delta, error) {
	k := keyOf(r)
	if !j.right.has(k, r.Val) {
		return nil, fmt.Errorf("incjoin: delete of absent right row %+v", r)
	}
	if !k.valid || j.left.count(k) == 0 {
		j.right.add(k, r.Val, -1)
		return nil, nil
	}
	var out []Delta
	for lval, cnt := range j.left[k] {
		out = append(out, Delta{
			Row:   JoinRow{LKey: k.nullString(), LVal: lval, RVal: NS(r.Val)},
			Count: -cnt,
		})
	}
	j.right.add(k, r.Val, -1)
	if j.right.count(k) == 0 {
		// 1 -> 0 transition: emit the null-padded rows again.
		out = append(out, j.nullPadDelta(k, 1)...)
	}
	return out, nil
}

// nullPadDelta emits (sign=+1) or retracts (sign=-1) one null-padded row
// per left row currently stored under k.
func (j *LeftJoin) nullPadDelta(k key, sign int) []Delta {
	var out []Delta
	for lval, cnt := range j.left[k] {
		out = append(out, Delta{
			Row:   JoinRow{LKey: k.nullString(), LVal: lval},
			Count: sign * cnt,
		})
	}
	return out
}

// Rows returns the materialized join result as a multiset.
func (j *LeftJoin) Rows() map[JoinRow]int {
	out := make(map[JoinRow]int)
	for k, vals := range j.left {
		for lval, lcnt := range vals {
			if !k.valid || j.right.count(k) == 0 {
				out[JoinRow{LKey: k.nullString(), LVal: lval}] += lcnt
				continue
			}
			for rval, rcnt := range j.right[k] {
				out[JoinRow{LKey: k.nullString(), LVal: lval, RVal: NS(rval)}] += lcnt * rcnt
			}
		}
	}
	return out
}

// FullLeftJoin computes the left join of two row multisets from scratch.
// It is the reference implementation used to validate incremental results.
func FullLeftJoin(left, right []Row) map[JoinRow]int {
	rightByKey := make(map[string]map[string]int)
	for _, r := range right {
		if r.Key == nil {
			continue // NULL keys never match
		}
		m, ok := rightByKey[*r.Key]
		if !ok {
			m = make(map[string]int)
			rightByKey[*r.Key] = m
		}
		m[r.Val]++
	}
	out := make(map[JoinRow]int)
	for _, l := range left {
		var lk NullString
		matched := false
		if l.Key != nil {
			lk = NS(*l.Key)
			for rval, rcnt := range rightByKey[*l.Key] {
				out[JoinRow{LKey: lk, LVal: l.Val, RVal: NS(rval)}] += rcnt
				matched = true
			}
		}
		if !matched {
			out[JoinRow{LKey: lk, LVal: l.Val}]++
		}
	}
	return out
}
