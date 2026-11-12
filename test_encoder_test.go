package netsec

import "example.org/netsec/q0122/hpack"

// testEncoder is a small HPACK writer used by assembler tests. It only
// emits inline-name literal fields with incremental indexing and optional
// Huffman encoding, which is enough to produce realistic header blocks.
type testEncoder struct {
	buf     []byte
	huffman bool
}

func newTestEncoder() *testEncoder { return &testEncoder{} }

func (e *testEncoder) bytes() []byte { return e.buf }

func appendTestVarint(dst []byte, value uint64, prefix uint, lead byte) []byte {
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

func (e *testEncoder) writeString(s string) {
	if !e.huffman {
		e.buf = appendTestVarint(e.buf, uint64(len(s)), 7, 0)
		e.buf = append(e.buf, s...)
		return
	}
	encoded := hpack.ExportHuffmanEncode(nil, []byte(s))
	e.buf = appendTestVarint(e.buf, uint64(len(encoded)), 7, 0x80)
	e.buf = append(e.buf, encoded...)
}

func (e *testEncoder) writeField(f hpack.Field) {
	if f.Sensitive {
		e.buf = appendTestVarint(e.buf, 0, 4, 0x10)
	} else {
		e.buf = appendTestVarint(e.buf, 0, 6, 0x40)
	}
	e.writeString(f.Name)
	e.writeString(f.Value)
}

func (e *testEncoder) writeFields(fields []hpack.Field) {
	for _, f := range fields {
		e.writeField(f)
	}
}
