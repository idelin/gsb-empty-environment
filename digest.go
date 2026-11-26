package archiveproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Digest is a SHA-256 content digest.  It is deliberately a fixed-size value
// so that certificates cannot accidentally carry the content it commits to.
type Digest [sha256.Size]byte

// Commitment is an opaque commitment to one or more evidence records.  A
// commitment may be disclosed when an approved timestamp signs it; the record
// identities and contents behind it need not be disclosed.
type Commitment [sha256.Size]byte

// DigestBytes returns the SHA-256 digest of b.
func DigestBytes(b []byte) Digest {
	return Digest(sha256.Sum256(b))
}

// DigestString returns the SHA-256 digest of s.
func DigestString(s string) Digest {
	return DigestBytes([]byte(s))
}

// String returns the lower-case hexadecimal form of d.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool { return d == Digest{} }

// MarshalText implements encoding.TextMarshaler.
func (d Digest) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Digest) UnmarshalText(b []byte) error {
	if len(b) != 2*sha256.Size {
		return fmt.Errorf("archiveproof: digest must be %d hex characters", 2*sha256.Size)
	}
	if _, err := hex.Decode(d[:], b); err != nil {
		return fmt.Errorf("archiveproof: invalid digest: %w", err)
	}
	return nil
}

// MarshalJSON emits a digest as a hex string.
func (d Digest) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON parses a digest hex string.
func (d *Digest) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(s))
}

// String returns the lower-case hexadecimal form of c.
func (c Commitment) String() string { return hex.EncodeToString(c[:]) }

// IsZero reports whether c is the all-zero commitment.
func (c Commitment) IsZero() bool { return c == Commitment{} }

// MarshalText implements encoding.TextMarshaler.
func (c Commitment) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *Commitment) UnmarshalText(b []byte) error {
	if len(b) != 2*sha256.Size {
		return fmt.Errorf("archiveproof: commitment must be %d hex characters", 2*sha256.Size)
	}
	if _, err := hex.Decode(c[:], b); err != nil {
		return fmt.Errorf("archiveproof: invalid commitment: %w", err)
	}
	return nil
}

// MarshalJSON emits a commitment as a hex string.
func (c Commitment) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// UnmarshalJSON parses a commitment hex string.
func (c *Commitment) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return c.UnmarshalText([]byte(s))
}

// CommitmentOf derives an opaque record commitment from a content digest and
// the permanent service identity that issued the record.  Hosts are free to
// supply explicit commitments instead, but this helper keeps the common case
// deterministic and domain-separated.
func CommitmentOf(service ServiceID, content Digest) Commitment {
	h := sha256.New()
	h.Write([]byte("archiveproof/record-commitment\x00"))
	h.Write([]byte(service))
	h.Write([]byte{0})
	h.Write(content[:])
	var c Commitment
	copy(c[:], h.Sum(nil))
	return c
}

// CommitmentOfBytes is a convenience wrapper around CommitmentOf.
func CommitmentOfBytes(service ServiceID, content []byte) Commitment {
	return CommitmentOf(service, DigestBytes(content))
}
