// Package dictagg implements filter and group-by aggregation over
// dictionary-encoded columns with null bitmaps and selection vectors.
package dictagg

// DictColumn is a dictionary-encoded string column with a null bitmap.
//
// Dict maps a dictionary code to its decoded key. Codes holds one dictionary
// code per row; the code is meaningless for rows where Nulls is true.
// Nulls is the per-row null bitmap (true means the row is null).
type DictColumn struct {
	Dict  []string
	Codes []int32
	Nulls []bool
}

// Len returns the number of logical rows in the column.
func (c *DictColumn) Len() int { return len(c.Codes) }

// KeyAt returns the decoded key for row i. ok is false when the row is null.
func (c *DictColumn) KeyAt(i int) (key string, ok bool) {
	if c.Nulls[i] {
		return "", false
	}
	return c.Dict[c.Codes[i]], true
}

// Values is a float64 column with its own null bitmap.
type Values struct {
	V     []float64
	Nulls []bool
}

// Len returns the number of logical rows in the column.
func (v *Values) Len() int { return len(v.V) }

// Selection is a selection vector: a list of row indices into the input
// columns. Indices may appear in any order and may repeat; repeated indices
// select the same row multiple times. An empty selection selects no rows.
type Selection []uint32

// Filter returns a new DictColumn containing exactly the rows picked by sel,
// in selection order. Duplicate indices duplicate rows.
func Filter(col *DictColumn, sel Selection) *DictColumn {
	out := &DictColumn{
		Dict:  col.Dict,
		Codes: make([]int32, len(sel)),
		Nulls: make([]bool, len(sel)),
	}
	for j, i := range sel {
		out.Codes[j] = col.Codes[i]
		out.Nulls[j] = col.Nulls[i]
	}
	return out
}

// FilterValues returns a new Values column containing exactly the rows
// picked by sel, in selection order.
func FilterValues(vals *Values, sel Selection) *Values {
	out := &Values{
		V:     make([]float64, len(sel)),
		Nulls: make([]bool, len(sel)),
	}
	for j, i := range sel {
		out.V[j] = vals.V[i]
		out.Nulls[j] = vals.Nulls[i]
	}
	return out
}
