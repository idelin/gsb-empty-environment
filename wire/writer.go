package wire

import "sync"

// Writer is a resettable append-only encoder backed by a pooled buffer.
// Like Reader, it is designed for reuse across many encode calls so the
// hot path performs no allocations once the pool is warm.
type Writer struct {
	buf []byte
}

var writerPool = sync.Pool{New: func() any { return &Writer{} }}

// AcquireWriter returns a pooled, empty Writer.
func AcquireWriter() *Writer {
	w := writerPool.Get().(*Writer)
	w.buf = w.buf[:0]
	return w
}

// Release resets the Writer and returns it to the pool. Bytes() must not
// be used after Release.
func (w *Writer) Release() {
	if cap(w.buf) > 1<<20 {
		w.buf = nil // don't pin oversized buffers in the pool
	} else {
		w.buf = w.buf[:0]
	}
	writerPool.Put(w)
}

// Bytes returns the encoded output. The slice is owned by the Writer and
// is invalidated by Release or further writes.
func (w *Writer) Bytes() []byte { return w.buf }

// Len reports the number of bytes written so far.
func (w *Writer) Len() int { return len(w.buf) }

// WriteVarint appends v in varint encoding.
func (w *Writer) WriteVarint(v uint64) { w.buf = AppendVarint(w.buf, v) }

// WriteZigZag64 appends v as a zigzag-encoded varint (sint64).
func (w *Writer) WriteZigZag64(v int64) { w.WriteVarint(EncodeZigZag64(v)) }

// WriteZigZag32 appends v as a zigzag-encoded varint (sint32).
func (w *Writer) WriteZigZag32(v int32) { w.WriteVarint(uint64(EncodeZigZag32(v))) }

// WriteFixed32 appends v little-endian.
func (w *Writer) WriteFixed32(v uint32) { w.buf = AppendFixed32(w.buf, v) }

// WriteFixed64 appends v little-endian.
func (w *Writer) WriteFixed64(v uint64) { w.buf = AppendFixed64(w.buf, v) }

// WriteTag appends a field tag.
func (w *Writer) WriteTag(fieldNum, wireType int) {
	w.buf = AppendTag(w.buf, fieldNum, wireType)
}

// WriteBytes appends raw bytes without a length prefix.
func (w *Writer) WriteBytes(b []byte) { w.buf = append(w.buf, b...) }

// WriteLengthDelimited appends a varint length prefix followed by b.
func (w *Writer) WriteLengthDelimited(b []byte) {
	w.WriteVarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}
