package huffsync

// EncodePlain produces a raw Huffman bit stream with no framing or sync
// words. It exists as the baseline "ordinary decoder" counterpart: any
// bit error destroys phase and garbles everything downstream.
func EncodePlain(msg []byte, c *HuffCode) *BitString {
	b := &BitString{}
	for _, s := range msg {
		c.encodeTo(b, int(s))
	}
	return b
}

// DecodePlain decodes a raw stream until the bits run out. It has no way
// to detect or recover from corruption.
func DecodePlain(b *BitString, c *HuffCode) []byte {
	var out []byte
	pos := 0
	for pos < b.Len() {
		sym, next, ok := c.decode(b, pos, b.Len())
		if !ok || sym == SymInvalid {
			break
		}
		out = append(out, byte(sym))
		pos = next
	}
	return out
}
