// Package dictenc implements a compact dictionary-encoded column with
// vectorized comparison and projection operators.
//
// Strings are encoded once into small integer dictionary IDs. All filter
// and projection operators work directly on the integer ID vectors; string
// literals are pushed down through the dictionary ("dictionary transform
// pushdown") so that a string comparison becomes a plain integer array
// comparison. Decoding back to real strings only happens at final output.
package dictenc

// Dictionary is a bidirectional string <-> ID mapping.
// IDs are assigned in insertion order starting at 0.
type Dictionary struct {
	idOf map[string]uint32
	strs []string
}

// NewDictionary builds a dictionary from the given distinct values.
func NewDictionary(values ...string) *Dictionary {
	d := &Dictionary{idOf: make(map[string]uint32, len(values))}
	for _, v := range values {
		d.Intern(v)
	}
	return d
}

// Intern returns the ID for s, appending it to the dictionary if new.
func (d *Dictionary) Intern(s string) uint32 {
	if id, ok := d.idOf[s]; ok {
		return id
	}
	id := uint32(len(d.strs))
	d.idOf[s] = id
	d.strs = append(d.strs, s)
	return id
}

// Lookup returns the ID for s and whether it exists in the dictionary.
// A missing string can never match any encoded row, which lets filters
// short-circuit without touching the column at all.
func (d *Dictionary) Lookup(s string) (uint32, bool) {
	id, ok := d.idOf[s]
	return id, ok
}

// Decode maps an ID back to its string. Used only at result output time.
func (d *Dictionary) Decode(id uint32) string { return d.strs[id] }

// Len returns the number of distinct values.
func (d *Dictionary) Len() int { return len(d.strs) }
