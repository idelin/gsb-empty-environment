package dictcol

// Project materializes the selected rows as real strings. This is the only
// point where dictionary IDs are translated back to values ("decode at
// output"); all upstream operators work purely on integer codes.
func (c *Column) Project(mask Mask) []string {
	out := make([]string, 0, mask.Count())
	for i := 0; i < len(c.Codes); i++ {
		if mask.Get(i) {
			out = append(out, c.Dict.Value(int(c.Codes[i])))
		}
	}
	return out
}

// ProjectCodes returns the raw dictionary IDs of selected rows without
// decoding, for chaining further integer-domain operators.
func (c *Column) ProjectCodes(mask Mask) []byte {
	out := make([]byte, 0, mask.Count())
	for i := 0; i < len(c.Codes); i++ {
		if mask.Get(i) {
			out = append(out, c.Codes[i])
		}
	}
	return out
}
