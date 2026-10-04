package streamdiff

import (
	"bytes"
	"io"
)

const compareBufSize = 32 * 1024

// valuesEqual decides whether two values are identical.
//
// Decision order:
//  1. Trust model: if enabled and both sides expose a strong digest, the
//     digests alone decide (both equality and inequality).
//  2. Weak negative filter: if both sides expose a weak digest and the
//     digests differ, the values provably differ. Equal weak digests are
//     NOT conclusive and never skip the byte comparison.
//  3. Exact streaming byte comparison, which is always the final authority.
func valuesEqual(a, b Value, trustStrong bool) (bool, error) {
	if trustStrong {
		ah, aok := strongHashOf(a)
		bh, bok := strongHashOf(b)
		if aok && bok {
			return bytes.Equal(ah, bh), nil
		}
	}
	aw, aok := weakHashOf(a)
	bw, bok := weakHashOf(b)
	if aok && bok && aw != bw {
		return false, nil
	}
	return streamEqual(a, b)
}

func strongHashOf(v Value) ([]byte, bool) {
	if h, ok := v.(StrongHasher); ok {
		return h.StrongHash()
	}
	return nil, false
}

func weakHashOf(v Value) (uint64, bool) {
	if h, ok := v.(WeakHasher); ok {
		return h.WeakHash()
	}
	return 0, false
}

// streamEqual compares two byte streams chunk by chunk. It is insensitive
// to how either side splits its value into chunks.
func streamEqual(a, b io.Reader) (bool, error) {
	bufA := make([]byte, compareBufSize)
	bufB := make([]byte, compareBufSize)
	for {
		na, err := readChunk(a, bufA)
		if err != nil {
			return false, err
		}
		nb, err := readChunk(b, bufB)
		if err != nil {
			return false, err
		}
		if na != nb {
			return false, nil
		}
		if na == 0 {
			return true, nil
		}
		if !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
	}
}

// readChunk fills buf as far as the reader allows. It returns (0, nil) at
// end of stream.
func readChunk(r io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if m == 0 {
			return n, nil
		}
	}
	return n, nil
}
