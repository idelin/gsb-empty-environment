package reuseauth

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Hash is a SHA-256 digest used for all non-secret binding hashes.
type Hash [32]byte

func hashParts(domain string, parts ...[]byte) Hash {
	h := sha256.New()
	writeFramed := func(b []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(b)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(b)
	}
	writeFramed([]byte(domain))
	for _, part := range parts {
		writeFramed(part)
	}
	var sum Hash
	copy(sum[:], h.Sum(nil))
	return sum
}

func hashString(domain, value string) Hash {
	return hashParts(domain, []byte(value))
}

// ParseHash parses a lowercase or uppercase hexadecimal SHA-256 digest.
func ParseHash(value string) (Hash, error) {
	var out Hash
	raw, err := hex.DecodeString(value)
	if err != nil {
		return out, fmt.Errorf("%w: parse hash: %v", ErrInvalidArgument, err)
	}
	if len(raw) != len(out) {
		return out, fmt.Errorf("%w: hash length %d", ErrInvalidArgument, len(raw))
	}
	copy(out[:], raw)
	return out, nil
}

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// IsZero reports whether h is the all-zero digest.
func (h Hash) IsZero() bool { return h == Hash{} }

// MarshalText implements encoding.TextMarshaler.
func (h Hash) MarshalText() ([]byte, error) { return []byte(h.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := ParseHash(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

// ByteRange is a half-open byte interval [Start, End).
type ByteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// NewRange validates and returns [start, end).
func NewRange(start, end int64) (ByteRange, error) {
	r := ByteRange{Start: start, End: end}
	if err := r.Validate(); err != nil {
		return ByteRange{}, err
	}
	return r, nil
}

// MustRange is intended for tests and compile-time-known ranges.
func MustRange(start, end int64) ByteRange {
	r, err := NewRange(start, end)
	if err != nil {
		panic(err)
	}
	return r
}

// Validate checks that the range is non-negative, non-empty, and ordered.
func (r ByteRange) Validate() error {
	if r.Start < 0 || r.End <= r.Start {
		return fmt.Errorf("%w: range [%d,%d)", ErrInvalidArgument, r.Start, r.End)
	}
	return nil
}

// Length returns End-Start.
func (r ByteRange) Length() int64 { return r.End - r.Start }

// Contains reports whether r fully contains other.
func (r ByteRange) Contains(other ByteRange) bool {
	return r.Start <= other.Start && other.End <= r.End
}

// Intersection returns the common half-open interval.
func (r ByteRange) Intersection(other ByteRange) (ByteRange, bool) {
	start := max64(r.Start, other.Start)
	end := min64(r.End, other.End)
	if start >= end {
		return ByteRange{}, false
	}
	return ByteRange{Start: start, End: end}, true
}

func (r ByteRange) String() string { return fmt.Sprintf("[%d,%d)", r.Start, r.End) }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// RangeDigest binds an exact digest to an absolute byte range. The range is
// included in the digest so equal bytes at different offsets are not confused.
type RangeDigest struct {
	Range  ByteRange `json:"range"`
	Digest Hash      `json:"digest"`
}

// MakeRangeDigest digests data[Range.Start:Range.End] with range binding.
func MakeRangeDigest(data []byte, r ByteRange) (RangeDigest, error) {
	if err := r.Validate(); err != nil {
		return RangeDigest{}, err
	}
	if r.End > int64(len(data)) {
		return RangeDigest{}, fmt.Errorf("%w: range %s exceeds object length %d", ErrInvalidArgument, r, len(data))
	}
	return RangeDigest{
		Range:  r,
		Digest: hashParts("reuseauth.range-digest.v1", rangeBytes(r), data[r.Start:r.End]),
	}, nil
}

func rangeBytes(r ByteRange) []byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(r.Start))
	binary.BigEndian.PutUint64(b[8:16], uint64(r.End))
	return b[:]
}

func (rd RangeDigest) verifyData(data []byte) error {
	want, err := MakeRangeDigest(data, rd.Range)
	if err != nil {
		return err
	}
	if want.Digest != rd.Digest {
		return fmt.Errorf("%w: range %s", ErrDigestMismatch, rd.Range)
	}
	return nil
}

func canonicalRangeDigests(in []RangeDigest) []RangeDigest {
	out := append([]RangeDigest(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Range.Start != out[j].Range.Start {
			return out[i].Range.Start < out[j].Range.Start
		}
		return out[i].Range.End < out[j].Range.End
	})
	return out
}

func hashRangeDigests(domain string, ranges []RangeDigest) Hash {
	canonical := canonicalRangeDigests(ranges)
	encoded, _ := json.Marshal(canonical)
	return hashString(domain, string(encoded))
}

// EquivalentRanges returns exact absolute ranges present in both
// representations with identical range-bound digests. It never infers
// equivalence from a whole-segment digest for an unknown subrange.
func EquivalentRanges(cached, offered []RangeDigest) ([]RangeDigest, error) {
	cachedByRange := make(map[ByteRange]Hash, len(cached))
	for _, rd := range cached {
		if err := rd.Range.Validate(); err != nil {
			return nil, err
		}
		if prior, ok := cachedByRange[rd.Range]; ok && prior != rd.Digest {
			return nil, fmt.Errorf("%w: conflicting digests for %s", ErrDigestMismatch, rd.Range)
		}
		cachedByRange[rd.Range] = rd.Digest
	}

	var shared []RangeDigest
	seen := make(map[ByteRange]struct{})
	for _, rd := range canonicalRangeDigests(offered) {
		if err := rd.Range.Validate(); err != nil {
			return nil, err
		}
		cachedDigest, ok := cachedByRange[rd.Range]
		if !ok || cachedDigest != rd.Digest {
			continue
		}
		if _, dup := seen[rd.Range]; dup {
			continue
		}
		seen[rd.Range] = struct{}{}
		shared = append(shared, rd)
	}
	return shared, nil
}

func mergeRanges(ranges []ByteRange) []ByteRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := append([]ByteRange(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	merged := []ByteRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		if r.Start == last.End {
			last.End = r.End
			continue
		}
		merged = append(merged, r)
	}
	return merged
}
