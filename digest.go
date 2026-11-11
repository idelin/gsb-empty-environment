package depseal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Digest is a content digest in the form "<algorithm>:<hex-encoded-sum>".
// Only sha256 is supported today.
type Digest string

// Algorithm returns the digest algorithm prefix.
func (d Digest) Algorithm() string {
	if i := strings.IndexByte(string(d), ':'); i > 0 {
		return string(d[:i])
	}
	return ""
}

func (d Digest) validate() error {
	algo, sum, ok := strings.Cut(string(d), ":")
	if !ok || algo == "" || sum == "" {
		return fmt.Errorf("invalid digest %q, want algorithm:hex", string(d))
	}
	if algo != "sha256" {
		return fmt.Errorf("unsupported digest algorithm %q in %q", algo, string(d))
	}
	if len(sum) != hex.EncodedLen(sha256.Size) {
		return fmt.Errorf("digest %q has wrong sha256 length", string(d))
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return fmt.Errorf("digest %q is not hex encoded: %w", string(d), err)
	}
	return nil
}

// verify reports whether content matches the digest.
func (d Digest) verify(content []byte) error {
	if err := d.validate(); err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	got := Digest("sha256:" + hex.EncodeToString(sum[:]))
	if got != d {
		return &DigestMismatchError{Wanted: d, Got: got}
	}
	return nil
}

// SHA256Digest builds a Digest for content and is primarily useful in tests.
func SHA256Digest(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}
