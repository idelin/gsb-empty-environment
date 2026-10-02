// Package dictcol implements vectorized comparison and projection operators
// over compact dictionary-encoded columns. String predicates are pushed down
// to integer dictionary-ID comparisons executed directly on the encoded byte
// array; real strings are only materialized at final output (projection).
package dictcol

// Dictionary maps strings to compact integer IDs and back.
type Dictionary struct {
	values []string
	index  map[string]int
}

// NewDictionary builds a dictionary from the distinct values of a column.
// IDs are assigned in first-seen order, starting at 0.
func NewDictionary(values []string) *Dictionary {
	d := &Dictionary{index: make(map[string]int)}
	for _, v := range values {
		if _, ok := d.index[v]; !ok {
			d.index[v] = len(d.values)
			d.values = append(d.values, v)
		}
	}
	return d
}

// Intern returns the dictionary ID for v, or -1 if absent.
func (d *Dictionary) Intern(v string) int {
	if id, ok := d.index[v]; ok {
		return id
	}
	return -1
}

// Value returns the string for a dictionary ID.
func (d *Dictionary) Value(id int) string {
	return d.values[id]
}

// Len returns the number of distinct values.
func (d *Dictionary) Len() int {
	return len(d.values)
}
