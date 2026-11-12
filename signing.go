package taskpkg

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
)

// Domain separators make signatures produced by this package unusable by any
// other protocol and distinguish per-segment signatures from round digests.
const (
	segmentDomainV1 = "tasksig/segment/v1"
	roundDomainV1   = "tasksig/round/v1"
)

// putUint64 writes a fixed-width little-endian integer.
func putUint64(w io.Writer, v uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, _ = w.Write(buf[:])
}

// putBytes writes a length-prefixed byte string, so adjacent fields can never
// be confused with each other and empty values are unambiguous.
func putBytes(w io.Writer, b []byte) {
	putUint64(w, uint64(len(b)))
	_, _ = w.Write(b)
}

// putDigest writes a fixed 32-byte digest.
func putDigest(w io.Writer, d [32]byte) {
	_, _ = w.Write(d[:])
}

// RecordDigest is the canonical digest of a record's key and value.
func RecordDigest(key string, value []byte) [32]byte {
	h := sha256.New()
	putBytes(h, []byte(key))
	putBytes(h, value)
	return [32]byte(h.Sum(nil))
}

// segmentBody writes the fields shared by every segment. The final segment's
// final flag and round fields are deliberately excluded so that a segment's
// digest stays independent of the round digest it commits to. This lets the
// round digest be computed before the closing segment is marked final without
// changing any segment's digest, and prevents circularity.
func segmentBody(w io.Writer, s Segment) {
	_, _ = io.WriteString(w, segmentDomainV1)
	putUint64(w, s.Index)
	putBytes(w, []byte(s.Zone))
	putUint64(w, uint64(s.StartVersion))
	putUint64(w, uint64(s.TargetVersion))
	putDigest(w, s.PrevDigest)

	putUint64(w, uint64(len(s.Operations)))
	for _, op := range s.Operations {
		putUint64(w, uint64(op.Type))
		putBytes(w, []byte(op.Key))
		putBytes(w, op.Value)
		putDigest(w, op.Digest)
	}
}

func boolByte(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// segmentDigest hashes the canonical body. It is the value referenced by the
// next segment's PrevDigest and included in the round digest.
func segmentDigest(s Segment) [32]byte {
	h := sha256.New()
	segmentBody(h, s)
	return [32]byte(h.Sum(nil))
}

// segmentSigningInput builds the exact authenticated payload. For the final
// segment the round digest and segment count are appended inside the same
// domain-separated, length-prefixed envelope, so a final-segment signature
// cannot be transplanted onto a non-final segment or vice versa.
func segmentSigningInput(s Segment) []byte {
	h := sha256.New()
	segmentBody(h, s)
	putUint64(h, boolByte(s.Final))
	if s.Final {
		_, _ = io.WriteString(h, roundDomainV1)
		putDigest(h, s.RoundDigest)
		putUint64(h, s.RoundSize)
	}
	return h.Sum(nil)
}

// roundDigest commits to every segment of the round. Each segment contributes
// its body digest in chain order, so reordering, truncating or splicing
// segments changes the digest.
func roundDigest(zone ZoneID, start, target Version, segments []Segment) [32]byte {
	h := sha256.New()
	_, _ = io.WriteString(h, roundDomainV1)
	putBytes(h, []byte(zone))
	putUint64(h, uint64(start))
	putUint64(h, uint64(target))
	putUint64(h, uint64(len(segments)))
	for _, seg := range segments {
		putDigest(h, segmentDigest(seg))
	}
	return [32]byte(h.Sum(nil))
}
