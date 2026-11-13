package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	maxWireIDLen    = 1024
	maxWireBlobLen  = 1 << 20
	maxMessageBytes = 4 << 20
)

var errShortWire = errors.New("netsec: truncated wire encoding")

type wireBuilder struct {
	data []byte
}

func (w *wireBuilder) u16(v uint16) {
	w.data = append(w.data, byte(v>>8), byte(v))
}

func (w *wireBuilder) u32(v uint32) {
	w.data = append(w.data, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *wireBuilder) i64(v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	w.data = append(w.data, b[:]...)
}

func (w *wireBuilder) raw(v []byte) {
	w.data = append(w.data, v...)
}

func (w *wireBuilder) blob(v []byte) {
	w.u32(uint32(len(v)))
	w.raw(v)
}

func (w *wireBuilder) text(v string) {
	w.blob([]byte(v))
}

func (w *wireBuilder) boolean(v bool) {
	if v {
		w.data = append(w.data, 1)
		return
	}
	w.data = append(w.data, 0)
}

func (w *wireBuilder) bytes() []byte {
	return append([]byte(nil), w.data...)
}

type wireReader struct {
	data []byte
	off  int
	err  error
}

func newWireReader(data []byte) *wireReader {
	return &wireReader{data: data}
}

func (r *wireReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.data)-r.off < n {
		r.err = errShortWire
		return nil
	}
	v := r.data[r.off : r.off+n]
	r.off += n
	return v
}

func (r *wireReader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}

func (r *wireReader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *wireReader) i64() int64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return int64(binary.BigEndian.Uint64(b))
}

func (r *wireReader) boolean() bool {
	b := r.take(1)
	if b == nil {
		return false
	}
	if b[0] != 0 && b[0] != 1 {
		r.err = fmt.Errorf("netsec: invalid boolean encoding")
		return false
	}
	return b[0] == 1
}

func (r *wireReader) blob(max int) []byte {
	n := int(r.u32())
	if r.err != nil {
		return nil
	}
	if n < 0 || n > max {
		r.err = fmt.Errorf("netsec: wire field too large")
		return nil
	}
	v := r.take(n)
	if v == nil {
		return nil
	}
	return append([]byte(nil), v...)
}

func (r *wireReader) fixed(n int) []byte {
	v := r.take(n)
	if v == nil {
		return nil
	}
	return append([]byte(nil), v...)
}

func (r *wireReader) text(max int) string {
	return string(r.blob(max))
}

func (r *wireReader) done() error {
	if r.err != nil {
		return r.err
	}
	if r.off != len(r.data) {
		return fmt.Errorf("netsec: trailing wire data")
	}
	return nil
}
