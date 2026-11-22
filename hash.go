package disclosure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Hash is a SHA-256 digest. The zero value is used for absent tree roots or
// absent predecessor digests.
type Hash [32]byte

// HashBytes returns the SHA-256 digest of b.
func HashBytes(b []byte) Hash {
	return sha256.Sum256(b)
}

// CommitBody commits to a disclosed body. A commitment is still present when
// the body itself is withheld or legally erased.
func CommitBody(body []byte) Hash {
	h := sha256.New()
	_, _ = h.Write([]byte("audit-body/v1\x00"))
	_, _ = h.Write(body)
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether h is the all-zero value.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// MarshalText makes roots and commitments readable in JSON and text formats.
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

// UnmarshalText accepts a 64-character hexadecimal digest.
func (h *Hash) UnmarshalText(text []byte) error {
	if len(text) != 64 {
		return fmt.Errorf("disclosure: hash must contain 64 hex characters")
	}
	decoded, err := hex.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("disclosure: decode hash: %w", err)
	}
	copy(h[:], decoded)
	return nil
}

// MarshalJSON emits a hash as a hexadecimal string.
func (h Hash) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.String())
}

// UnmarshalJSON accepts a hexadecimal hash string.
func (h *Hash) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	return h.UnmarshalText([]byte(text))
}

type encoder struct {
	buf []byte
}

func (e *encoder) byte(v byte) {
	e.buf = append(e.buf, v)
}

func (e *encoder) bool(v bool) {
	if v {
		e.byte(1)
		return
	}
	e.byte(0)
}

func (e *encoder) u64(v uint64) {
	e.buf = append(e.buf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v),
	)
}

func (e *encoder) i64(v int64) {
	e.u64(uint64(v))
}

func (e *encoder) raw(b []byte) {
	e.u64(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

func (e *encoder) str(s string) {
	e.raw([]byte(s))
}

func (e *encoder) hash(h Hash) {
	e.buf = append(e.buf, h[:]...)
}

func nodeHash(left, right Hash) Hash {
	h := sha256.New()
	_, _ = h.Write([]byte{0x01})
	_, _ = h.Write(left[:])
	_, _ = h.Write(right[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func treeLeafHash(leafHash Hash) Hash {
	h := sha256.New()
	_, _ = h.Write([]byte{0x00})
	_, _ = h.Write(leafHash[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}
