package dictcol

// Column is a dictionary-encoded column: one byte per row holding the
// dictionary ID. A 1024-row column fits in a compact 1024-byte array.
type Column struct {
	Dict  *Dictionary
	Codes []byte
}

// Encode dictionary-encodes raw string values into a compact byte column.
func Encode(values []string) *Column {
	d := NewDictionary(values)
	codes := make([]byte, len(values))
	for i, v := range values {
		codes[i] = byte(d.Intern(v))
	}
	return &Column{Dict: d, Codes: codes}
}

// Len returns the number of rows.
func (c *Column) Len() int {
	return len(c.Codes)
}
