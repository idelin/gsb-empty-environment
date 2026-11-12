package taskpkg

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math/big"
)

// seedReader is a deterministic io.Reader so ed25519.GenerateKey and every test
// scalar are reproducible without external randomness.
type seedReader struct {
	label   string
	counter uint64
	buffer  []byte
}

func newSeedReader(label string) *seedReader {
	return &seedReader{label: label}
}

func (r *seedReader) Read(p []byte) (int, error) {
	filled := 0
	for filled < len(p) {
		if len(r.buffer) == 0 {
			h := sha256.New()
			var block [8]byte
			binary.BigEndian.PutUint64(block[:], r.counter)
			h.Write(block[:])
			h.Write([]byte(r.label))
			r.buffer = h.Sum(nil)
			r.counter++
		}
		n := copy(p[filled:], r.buffer)
		r.buffer = r.buffer[n:]
		filled += n
	}
	return filled, nil
}

var _ io.Reader = (*seedReader)(nil)

// deriveScalar returns a deterministic non-zero field element.
func deriveScalar(prefix, id string, prime *big.Int) *big.Int {
	h := sha256.New()
	h.Write([]byte(prefix))
	h.Write([]byte{0})
	h.Write([]byte(id))
	scalar := new(big.Int).SetBytes(h.Sum(nil))
	scalar.Mod(scalar, prime)
	if scalar.Sign() == 0 {
		scalar.SetInt64(1)
	}
	return scalar
}
