package netsec

import "fmt"

// SegmentType is the BGP path segment type. Only AS_SEQUENCE is accepted for
// ASPA verification.
type SegmentType uint8

const (
	SegmentASSet            SegmentType = 1
	SegmentASSequence       SegmentType = 2
	SegmentASConfedSequence SegmentType = 3
	SegmentASConfedSet      SegmentType = 4
)

func (t SegmentType) String() string {
	switch t {
	case SegmentASSet:
		return "AS_SET"
	case SegmentASSequence:
		return "AS_SEQUENCE"
	case SegmentASConfedSequence:
		return "AS_CONFED_SEQUENCE"
	case SegmentASConfedSet:
		return "AS_CONFED_SET"
	default:
		return fmt.Sprintf("segment_type_%d", uint8(t))
	}
}

// Segment is one AS_PATH segment.
type Segment struct {
	Type SegmentType
	ASNs []ASN
}

// Path is an AS_PATH in propagation order: the origin AS is first and the
// current session neighbor is last. The local AS must not appear in it.
type Path struct {
	Segments []Segment
}

// SequencePath creates a path containing one AS_SEQUENCE segment.
func SequencePath(asns ...ASN) Path {
	return Path{Segments: []Segment{{Type: SegmentASSequence, ASNs: append([]ASN(nil), asns...)}}}
}

// PrependRun is the compressed topology view of one consecutive AS run plus
// the repetition evidence observed on the wire. Count includes the first
// occurrence and is capped at the host's MaxPrepend value; Truncated reports
// that additional adjacent copies were removed from the evidence count.
type PrependRun struct {
	ASN       ASN
	Count     int
	Truncated bool
}

// NormalizedPath is the path used by route-leak verification. Adjacent copies
// of an AS are collapsed before loop and valley checks, while Count records how
// many times that AS was prepended. Count is capped by the host's MaxPrepend
// setting so untrusted input cannot create unbounded evidence.
type NormalizedPath struct {
	ASNs []ASN
	Runs []PrependRun
}

// DefaultMaxPrepend is the default number of repetitions retained per
// consecutive prepend run.
const DefaultMaxPrepend = 255

// NormalizePath checks segment types, ASNs, local-AS absence, and loops after
// adjacent prepends are compressed.
func NormalizePath(path Path, localASN ASN, maxPrepend int) (NormalizedPath, error) {
	if len(path.Segments) == 0 {
		return NormalizedPath{}, fmt.Errorf("%w: %w: no segments", ErrInvalidPath, ErrEmptyPath)
	}
	if err := ValidateASN(localASN); err != nil {
		return NormalizedPath{}, fmt.Errorf("%w: local AS: %w", ErrInvalidPath, err)
	}
	if maxPrepend <= 0 {
		maxPrepend = DefaultMaxPrepend
	}

	var raw []ASN
	for i, segment := range path.Segments {
		if segment.Type != SegmentASSequence {
			return NormalizedPath{}, fmt.Errorf("%w: %w: segment %d is %s",
				ErrInvalidPath, ErrNonSequenceSegment, i, segment.Type)
		}
		if len(segment.ASNs) == 0 {
			return NormalizedPath{}, fmt.Errorf("%w: %w: segment %d is empty", ErrInvalidPath, ErrEmptyPath, i)
		}
		raw = append(raw, segment.ASNs...)
	}
	if len(raw) == 0 {
		return NormalizedPath{}, fmt.Errorf("%w: %w: no ASNs", ErrInvalidPath, ErrEmptyPath)
	}

	for i, asn := range raw {
		if err := ValidateASN(asn); err != nil {
			return NormalizedPath{}, fmt.Errorf("%w: element %d: %w", ErrInvalidPath, i, err)
		}
		if asn == localASN {
			return NormalizedPath{}, fmt.Errorf("%w: %w: local AS %d at element %d",
				ErrInvalidPath, ErrLocalASInPath, uint32(localASN), i)
		}
	}

	normalized := NormalizedPath{}
	seen := make(map[ASN]struct{}, len(raw))
	for _, asn := range raw {
		if len(normalized.Runs) > 0 && normalized.Runs[len(normalized.Runs)-1].ASN == asn {
			run := &normalized.Runs[len(normalized.Runs)-1]
			if run.Count < maxPrepend {
				run.Count++
			} else {
				run.Truncated = true
			}
			continue
		}
		if _, duplicate := seen[asn]; duplicate {
			return NormalizedPath{}, fmt.Errorf("%w: %w: AS %d repeats after a different AS",
				ErrInvalidPath, ErrPathLoop, uint32(asn))
		}
		seen[asn] = struct{}{}
		normalized.ASNs = append(normalized.ASNs, asn)
		normalized.Runs = append(normalized.Runs, PrependRun{ASN: asn, Count: 1})
	}
	return normalized, nil
}

// Last returns the current session neighbor represented by the normalized path.
func (p NormalizedPath) Last() (ASN, bool) {
	if len(p.ASNs) == 0 {
		return 0, false
	}
	return p.ASNs[len(p.ASNs)-1], true
}
