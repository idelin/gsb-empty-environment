package streamdiff

import (
	"bytes"
	"crypto/sha256"
	"io"
)

// BytesValue is an in-memory Value split into fixed-size chunks, with its
// strong digest precomputed. Useful for tests and for sources that already
// hold (small) values in memory.
type BytesValue struct {
	data      []byte
	chunkSize int
	sum       [sha256.Size]byte
}

// NewBytesValue wraps data as a Value whose chunks are at most chunkSize
// bytes (values <= 0 mean a single chunk).
func NewBytesValue(data []byte, chunkSize int) *BytesValue {
	if chunkSize <= 0 {
		chunkSize = len(data)
		if chunkSize == 0 {
			chunkSize = 1
		}
	}
	return &BytesValue{data: data, chunkSize: chunkSize, sum: sha256.Sum256(data)}
}

func (v *BytesValue) Open() ChunkIter { return &bytesIter{data: v.data, size: v.chunkSize} }

func (v *BytesValue) Strong() ([sha256.Size]byte, bool) { return v.sum, true }

// Bytes returns the underlying content.
func (v *BytesValue) Bytes() []byte { return v.data }

type bytesIter struct {
	data []byte
	size int
}

func (it *bytesIter) Next() ([]byte, error) {
	if len(it.data) == 0 {
		return nil, io.EOF
	}
	n := it.size
	if n > len(it.data) {
		n = len(it.data)
	}
	chunk := it.data[:n]
	it.data = it.data[n:]
	return chunk, nil
}

// chunkReader adapts a ChunkIter to an io.Reader.
type chunkReader struct {
	it  ChunkIter
	cur []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		chunk, err := r.it.Next()
		if err != nil {
			return 0, err
		}
		r.cur = chunk
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

// equalValues decides whether two values have identical content.
//
// Fast path (trust model, see package doc): if both sides advertise a
// SHA-256 digest, digest equality is accepted as content equality. Digest
// inequality is conclusive proof of difference regardless of trust.
//
// Slow path: byte-wise streaming comparison across chunk boundaries, so
// values are never required to share a chunking or to fit in memory.
func equalValues(a, b Value) (bool, error) {
	if sa, ok := a.Strong(); ok {
		if sb, ok := b.Strong(); ok {
			return sa == sb, nil
		}
	}
	return readersEqual(&chunkReader{it: a.Open()}, &chunkReader{it: b.Open()})
}

// readersEqual reports whether two readers produce identical byte streams.
// It never holds more than two buffers in memory.
func readersEqual(a, b io.Reader) (bool, error) {
	bufA := make([]byte, 32*1024)
	bufB := make([]byte, 32*1024)
	for {
		nA, errA := io.ReadFull(a, bufA)
		nB, errB := io.ReadFull(b, bufB)
		if nA != nB || !bytes.Equal(bufA[:nA], bufB[:nB]) {
			return false, nil
		}
		doneA := errA == io.EOF || errA == io.ErrUnexpectedEOF
		doneB := errB == io.EOF || errB == io.ErrUnexpectedEOF
		if doneA && doneB {
			return true, nil
		}
		if doneA != doneB {
			return false, nil
		}
		if errA != nil {
			return false, errA
		}
		if errB != nil {
			return false, errB
		}
	}
}
