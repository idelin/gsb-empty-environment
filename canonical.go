package evidence

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"time"
)

// canonicalHasher provides unambiguous, length-delimited field encoding for
// the package's content digests.  It intentionally does not use JSON: field
// order changes or alternate encodings must never change a digest.
type canonicalHasher struct {
	h hash.Hash
}

func newCanonical(tag string) *canonicalHasher {
	h := sha256.New()
	h.Write([]byte(tag))
	h.Write([]byte{0})
	return &canonicalHasher{h: h}
}

func (c *canonicalHasher) raw(b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = c.h.Write(n[:])
	_, _ = c.h.Write(b)
}

func (c *canonicalHasher) str(s string) { c.raw([]byte(s)) }

func (c *canonicalHasher) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = c.h.Write(b[:])
}

func (c *canonicalHasher) i64(v int64) { c.u64(uint64(v)) }

func (c *canonicalHasher) boolean(v bool) {
	if v {
		c.u64(1)
	} else {
		c.u64(0)
	}
}

func (c *canonicalHasher) digest(d Digest) { _, _ = c.h.Write(d[:]) }

func (c *canonicalHasher) digestMaybe(d Digest) {
	c.boolean(!d.IsZero())
	if !d.IsZero() {
		c.digest(d)
	}
}

// timeValue encodes a time without a monotonic clock reading.  Zero is encoded
// distinctly from the Unix epoch.
func (c *canonicalHasher) timeValue(t time.Time) {
	if t.IsZero() {
		c.u64(0)
		return
	}
	c.u64(1)
	c.str(t.UTC().Format(time.RFC3339Nano))
}

func (c *canonicalHasher) span(s TimeSpan) {
	normalized, err := s.Normalize()
	if err != nil {
		normalized = TimeSpan{}
	}
	c.u64(uint64(len(normalized.Segments)))
	for _, iv := range normalized.Segments {
		c.timeValue(iv.Start)
		c.timeValue(iv.End)
	}
}

func (c *canonicalHasher) sum() Digest {
	var d Digest
	copy(d[:], c.h.Sum(nil))
	return d
}

func checkedAdd64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}
