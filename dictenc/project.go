package dictenc

// Project returns a new column containing only the rows selected by mask.
// The result stays dictionary-encoded (IDs are copied, the dictionary is
// shared) so downstream operators keep working on integers.
func (c *Column) Project(mask Mask) *Column {
	out := &Column{Dict: c.Dict, IDs: make([]byte, 0, mask.Count())}
	for i, keep := range mask {
		if keep != 0 {
			out.IDs = append(out.IDs, c.IDs[i])
		}
	}
	return out
}

// Materialize decodes the column back to real strings. This is the only
// place the dictionary is reverse-looked up per row, and it is meant to run
// exactly once, when results leave the engine.
func (c *Column) Materialize() []string {
	out := make([]string, len(c.IDs))
	for i, id := range c.IDs {
		out[i] = c.Dict.Decode(uint32(id))
	}
	return out
}
