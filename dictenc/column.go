package dictenc

// Column is a dictionary-encoded column: a shared dictionary plus a compact
// vector of 8-bit dictionary IDs (one byte per row).
type Column struct {
	Dict *Dictionary
	IDs  []byte
}

// NewColumn encodes values into a dictionary-encoded column.
func NewColumn(dict *Dictionary, values []string) *Column {
	ids := make([]byte, len(values))
	for i, v := range values {
		ids[i] = byte(dict.Intern(v))
	}
	return &Column{Dict: dict, IDs: ids}
}

// Len returns the number of rows.
func (c *Column) Len() int { return len(c.IDs) }

// ValueAt decodes the value of row i. Intended for output/final
// materialization only, never for per-row filtering work.
func (c *Column) ValueAt(i int) string { return c.Dict.Decode(uint32(c.IDs[i])) }
