package reuseauth

import "fmt"

// Segment is one downloaded segment's absolute range and representation digest.
type Segment struct {
	Index  int       `json:"index"`
	Range  ByteRange `json:"range"`
	Digest Hash      `json:"digest"`
}

// Manifest binds the ordered segment digests for the downloaded representation.
type Manifest struct {
	ObjectLength int64     `json:"object_length"`
	Segments     []Segment `json:"segments"`
}

// Validate checks segment indexes, ranges, and object-length bounds.
func (m Manifest) Validate() error {
	if m.ObjectLength < -1 {
		return fmt.Errorf("%w: object length %d", ErrInvalidArgument, m.ObjectLength)
	}
	expectedIndex := 0
	for _, segment := range m.Segments {
		if segment.Index != expectedIndex {
			return fmt.Errorf("%w: segment index %d, expected %d", ErrInvalidArgument, segment.Index, expectedIndex)
		}
		expectedIndex++
		if err := segment.Range.Validate(); err != nil {
			return err
		}
		if segment.Digest.IsZero() {
			return fmt.Errorf("%w: segment %d missing digest", ErrInvalidArgument, segment.Index)
		}
		if m.ObjectLength >= 0 && segment.Range.End > m.ObjectLength {
			return fmt.Errorf("%w: segment %s exceeds object length %d", ErrInvalidArgument, segment.Range, m.ObjectLength)
		}
	}
	return nil
}

// Digest returns the canonical manifest digest.
func (m Manifest) Digest() Hash {
	return hashString("reuseauth.manifest.v1", string(mustJSON(m)))
}

// RangeDigests converts segments to the equivalence representation.
func (m Manifest) RangeDigests() []RangeDigest {
	out := make([]RangeDigest, 0, len(m.Segments))
	for _, segment := range m.Segments {
		out = append(out, RangeDigest{Range: segment.Range, Digest: segment.Digest})
	}
	return out
}

// VerifyContent verifies the full small object against range-bound segment
// digests produced by MakeRangeDigest.
func (m Manifest) VerifyContent(data []byte) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.ObjectLength >= 0 && int64(len(data)) != m.ObjectLength {
		return fmt.Errorf("%w: content length %d, manifest %d", ErrDigestMismatch, len(data), m.ObjectLength)
	}
	for _, segment := range m.Segments {
		rd := RangeDigest{Range: segment.Range, Digest: segment.Digest}
		if err := rd.verifyData(data); err != nil {
			return err
		}
	}
	return nil
}
