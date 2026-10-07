package encobj

import (
	"encoding/binary"
	"fmt"
)

// Unambiguous additional-data / plaintext encoding: every element is
// length-prefixed with a uvarint, so concatenation is injective and no
// field boundary can be smuggled across elements.

type encoder struct{ buf []byte }

func (e *encoder) str(s string) {
	e.bytes([]byte(s))
}

func (e *encoder) bytes(b []byte) {
	var n [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(n[:], uint64(len(b)))
	e.buf = append(e.buf, n[:k]...)
	e.buf = append(e.buf, b...)
}

func (e *encoder) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	e.bytes(b[:])
}

type decoder struct {
	buf []byte
	err error
}

func (d *decoder) bytes() []byte {
	if d.err != nil {
		return nil
	}
	n, k := binary.Uvarint(d.buf)
	if k <= 0 || uint64(len(d.buf[k:])) < n {
		d.err = fmt.Errorf("%w: bad length-prefixed element", ErrCorrupt)
		return nil
	}
	b := d.buf[k : k+int(n)]
	d.buf = d.buf[k+int(n):]
	return b
}

func (d *decoder) str() string { return string(d.bytes()) }

func (d *decoder) u64() uint64 {
	b := d.bytes()
	if d.err != nil {
		return 0
	}
	if len(b) != 8 {
		d.err = fmt.Errorf("%w: bad u64 element", ErrCorrupt)
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (d *decoder) done() bool { return d.err == nil && len(d.buf) == 0 }
