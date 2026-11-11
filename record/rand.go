package record

import "crypto/rand"

// readRandom fills b with cryptographically secure random bytes.
func readRandom(b []byte) (int, error) {
	return rand.Read(b)
}
