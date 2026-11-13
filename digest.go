package netsec

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest is the content identity used by the deduplication host.  The host
// deliberately compares digests, not full page contents.  A malicious or
// broken digest implementation can therefore create a digest collision in
// which two pages with different bytes are treated as identical.
type Digest string

// DigestFunc computes a page digest.
type DigestFunc func(content []byte) Digest

// SHA256Digest is the default content digest function.
func SHA256Digest(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest(hex.EncodeToString(sum[:]))
}

// DigestOf computes a digest with the supplied function.  A nil function uses
// SHA-256.
func DigestOf(content []byte, fn DigestFunc) Digest {
	if fn == nil {
		return SHA256Digest(content)
	}
	return fn(content)
}
