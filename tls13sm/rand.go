package tls13sm

import (
	"crypto/rand"
	"io"
)

// randReader is the package-wide source of cryptographic randomness. It is a
// thin wrapper over crypto/rand so that deterministic tests can inject their
// own reader when needed.
type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	return io.ReadFull(rand.Reader, p)
}

// randomBytes fills and returns a fresh slice of n random bytes.
func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := (randReader{}).Read(out); err != nil {
		return nil, err
	}
	return out, nil
}
