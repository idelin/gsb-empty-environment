package dictagg

// DictColumn is a dictionary-encoded string column with a null bitmap.
// Codes[i] indexes Dict; it is meaningless for rows where Nulls is set.
type DictColumn struct {
	Dict  []string
	Codes []int32
	Nulls *Bitmap
}

// NewDictColumn builds a column from a dictionary and codes; nullRows are NULL.
func NewDictColumn(dict []string, codes []int32, nullRows ...int) *DictColumn {
	return &DictColumn{Dict: dict, Codes: codes, Nulls: NewBitmap(len(codes), nullRows...)}
}

// Len returns the number of rows.
func (c *DictColumn) Len() int { return len(c.Codes) }

// ValueAt returns the decoded key and whether row i is valid (non-null).
func (c *DictColumn) ValueAt(i int) (string, bool) {
	if c.Nulls.IsNull(i) {
		return "", false
	}
	return c.Dict[c.Codes[i]], true
}

// IntColumn is an int64 column with a null bitmap.
type IntColumn struct {
	Values []int64
	Nulls  *Bitmap
}

// NewIntColumn builds an int64 column; nullRows are NULL.
func NewIntColumn(values []int64, nullRows ...int) *IntColumn {
	return &IntColumn{Values: values, Nulls: NewBitmap(len(values), nullRows...)}
}

// Len returns the number of rows.
func (c *IntColumn) Len() int { return len(c.Values) }

// ValueAt returns the value and whether row i is valid (non-null).
func (c *IntColumn) ValueAt(i int) (int64, bool) {
	if c.Nulls.IsNull(i) {
		return 0, false
	}
	return c.Values[i], true
}
