package netsec

import "crypto/rand"

// randomRequestIDSuffix returns 16 random bytes for request IDs. It falls
// back to a panic only when the platform CSPRNG is broken, which makes the
// process unable to produce security-sensitive identifiers at all.
func randomRequestIDSuffix() []byte {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		panic(err)
	}
	return suffix
}
