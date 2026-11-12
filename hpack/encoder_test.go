package hpack

// encoder is a test-only HPACK writer covering every representation the
// decoder must accept. It lives in the hpack package so tests for both the
// decoder and the netsec assembler can exercise real HPACK byte streams.
type encoder struct {
	buf           []byte
	huffman       bool
	neverIndex    bool
	noIndex       bool
	dynamicMax    uint32
	dynamicFields []Field // newest first, mirroring Decoder.dyn
}

func newEncoder() *encoder {
	return &encoder{dynamicMax: 4096}
}

func (e *encoder) bytes() []byte { return e.buf }

func appendVarint(dst []byte, value uint64, prefix uint, lead byte) []byte {
	max := uint64(1)<<prefix - 1
	if value < max {
		return append(dst, lead|byte(value))
	}
	dst = append(dst, lead|byte(max))
	value -= max
	for value >= 128 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func (e *encoder) appendString(s string) {
	if !e.huffman {
		e.buf = appendVarint(e.buf, uint64(len(s)), 7, 0)
		e.buf = append(e.buf, s...)
		return
	}
	encoded := huffmanEncode(nil, []byte(s))
	e.buf = appendVarint(e.buf, uint64(len(encoded)), 7, 0x80)
	e.buf = append(e.buf, encoded...)
}

// indexed emits representation 6.1.
func (e *encoder) indexed(index uint64) {
	e.buf = appendVarint(e.buf, index, 7, 0x80)
}

// literalName emits a literal field whose name is provided inline.
func (e *encoder) literalName(name, value string) {
	switch {
	case e.neverIndex:
		e.buf = appendVarint(e.buf, 0, 4, 0x10)
	case e.noIndex:
		e.buf = appendVarint(e.buf, 0, 4, 0x00)
	default:
		e.buf = appendVarint(e.buf, 0, 6, 0x40)
		e.dynamicFields = append([]Field{{Name: name, Value: value}}, e.dynamicFields...)
	}
	e.appendString(name)
	e.appendString(value)
}

// literalIndexedName emits a literal referencing an existing table index.
func (e *encoder) literalIndexedName(index uint64, value string) {
	if e.neverIndex {
		e.buf = appendVarint(e.buf, index, 4, 0x10)
	} else if e.noIndex {
		e.buf = appendVarint(e.buf, index, 4, 0x00)
	} else {
		e.buf = appendVarint(e.buf, index, 6, 0x40)
		name, _ := e.lookup(index)
		e.dynamicFields = append([]Field{{Name: name, Value: value}}, e.dynamicFields...)
	}
	e.appendString(value)
}

func (e *encoder) lookup(index uint64) (string, bool) {
	if index == 0 {
		return "", false
	}
	if index <= uint64(len(staticTable)) {
		return staticTable[index-1].Name, true
	}
	i := int(index) - len(staticTable) - 1
	if i < 0 || i >= len(e.dynamicFields) {
		return "", false
	}
	return e.dynamicFields[i].Name, true
}

// sizeUpdate emits representation 6.3.
func (e *encoder) sizeUpdate(max uint32) {
	e.buf = appendVarint(e.buf, uint64(max), 5, 0x20)
	e.dynamicMax = max
}

// encodeFields emits one block for fields using inline names, so callers do
// not have to manage indices themselves.
func (e *encoder) encodeFields(fields ...Field) {
	for _, f := range fields {
		if f.Sensitive {
			prev := e.neverIndex
			e.neverIndex = true
			e.literalName(f.Name, f.Value)
			e.neverIndex = prev
			continue
		}
		e.literalName(f.Name, f.Value)
	}
}
