package netsec

import (
	"crypto/rand"
	"errors"
)

// Estimator computes the number of response body bytes the range response
// will occupy on the wire. Implementations must include every multipart
// boundary and header in addition to the payload bytes.
type Estimator interface {
	Estimate(size int64, segments []Segment) (int64, error)
}

// Limits caps the resources a single range request may consume. A zero field
// disables that particular limit.
type Limits struct {
	// MaxSegments caps the number of response parts after de-duplication.
	MaxSegments int64
	// MaxDataBytes caps the sum of payload bytes across response parts.
	MaxDataBytes int64
	// MaxWireBytes caps the full response size including multipart overhead.
	MaxWireBytes int64
	// MaxReads caps the number of coalesced object reads.
	MaxReads int64
}

// Planner normalizes requested intervals, de-duplicates repeated response
// parts, coalesces overlapping reads and enforces Limits.
type Planner struct {
	Limits    Limits
	Estimator Estimator
}

// Plan is the materialized result of planning a range request.
type Plan struct {
	// Size is the fixed object length the plan was computed against.
	Size int64
	// Segments are the response parts, in first-request order, duplicates
	// removed. Each maps one-to-one to a multipart part.
	Segments []Segment
	// Reads are the disjoint, coalesced spans that must be read from the
	// object. Overlapping or adjacent response parts share a single read.
	Reads []Segment
	// DataBytes is the total payload size of Segments.
	DataBytes int64
	// ReadBytes is the total payload size of Reads.
	ReadBytes int64
	// WireBytes is DataBytes plus multipart boundary and header overhead.
	WireBytes int64
	// DuplicateParts is the number of repeated request intervals removed.
	DuplicateParts int64
	// AmplificationBytes is payload requested more than once, i.e.
	// DataBytes minus ReadBytes.
	AmplificationBytes int64
}

// EstimatorFunc adapts a plain function to Estimator.
type EstimatorFunc func(size int64, segments []Segment) (int64, error)

// Estimate implements Estimator.
func (f EstimatorFunc) Estimate(size int64, segments []Segment) (int64, error) {
	return f(size, segments)
}

var (
	// ErrNoRanges indicates an empty interval list.
	ErrNoRanges = errors.New("netsec: no ranges requested")
	// ErrTooManySegments means MaxSegments was exceeded.
	ErrTooManySegments = errors.New("netsec: too many range segments")
	// ErrDataLimit means MaxDataBytes was exceeded.
	ErrDataLimit = errors.New("netsec: response data byte limit exceeded")
	// ErrWireLimit means MaxWireBytes was exceeded.
	ErrWireLimit = errors.New("netsec: response wire byte limit exceeded")
	// ErrReadLimit means MaxReads was exceeded.
	ErrReadLimit = errors.New("netsec: object read limit exceeded")
	// ErrOverflow means an integer overflow occurred while planning.
	ErrOverflow = errors.New("netsec: integer overflow while planning range")
)

// PlanRequest normalizes specs against an object of the given size and
// enforces limits with an overflow-checked budget.
//
// Every interval is resolved independently so that unsatisfiable intervals
// are reported with their request index. Response semantics are preserved:
// distinct requested ranges remain distinct response parts, while identical
// intervals are de-duplicated. Physical reads are independently coalesced.
//
// A nil estimator measures payload only; pass a MultipartEstimator to include
// boundary and part-header overhead in the wire budget.
func PlanRequest(size int64, specs []Spec, limits Limits, estimator Estimator) (*Plan, error) {
	if size < 0 {
		return nil, ErrInvalidSize
	}
	if len(specs) == 0 {
		return nil, ErrNoRanges
	}

	segments := make([]Segment, 0, len(specs))
	var duplicates int64
	for index, spec := range specs {
		segment, err := spec.Normalize(size)
		if err != nil {
			var unsatisfiable *UnsatisfiableError
			if errors.As(err, &unsatisfiable) {
				unsatisfiable.Index = index
			}
			return nil, err
		}
		if containsSeg(segments, segment) {
			duplicates++
			continue
		}
		segments = append(segments, segment)
	}

	if limits.MaxSegments > 0 && int64(len(segments)) > limits.MaxSegments {
		return nil, ErrTooManySegments
	}

	var dataBytes int64
	for _, segment := range segments {
		sum, ok := checkedAdd(dataBytes, segment.Length())
		if !ok {
			return nil, ErrOverflow
		}
		dataBytes = sum
	}
	if limits.MaxDataBytes > 0 && dataBytes > limits.MaxDataBytes {
		return nil, ErrDataLimit
	}

	var wireBytes int64
	if estimator != nil {
		wire, err := estimator.Estimate(size, segments)
		if err != nil {
			return nil, err
		}
		if wire < 0 {
			return nil, ErrOverflow
		}
		wireBytes = wire
	} else {
		wireBytes = dataBytes
	}
	if limits.MaxWireBytes > 0 && wireBytes > limits.MaxWireBytes {
		return nil, ErrWireLimit
	}

	reads, readBytes, ok := coalesce(segments)
	if !ok {
		return nil, ErrOverflow
	}
	if limits.MaxReads > 0 && int64(len(reads)) > limits.MaxReads {
		return nil, ErrReadLimit
	}

	amplification, ok := checkedSub(dataBytes, readBytes)
	if !ok {
		return nil, ErrOverflow
	}

	return &Plan{
		Size:               size,
		Segments:           segments,
		Reads:              reads,
		DataBytes:          dataBytes,
		ReadBytes:          readBytes,
		WireBytes:          wireBytes,
		DuplicateParts:     duplicates,
		AmplificationBytes: amplification,
	}, nil
}

// Plan applies the planner's own limits and estimator to specs.
func (p *Planner) Plan(size int64, specs []Spec) (*Plan, error) {
	return PlanRequest(size, specs, p.Limits, p.Estimator)
}

func containsSeg(segments []Segment, target Segment) bool {
	for _, segment := range segments {
		if segment == target {
			return true
		}
	}
	return false
}

// coalesce merges overlapping and adjacent spans into disjoint reads,
// accumulating their total length with overflow checks.
func coalesce(segments []Segment) ([]Segment, int64, bool) {
	sorted := make([]Segment, len(segments))
	copy(sorted, segments)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1].First > sorted[j].First; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}

	reads := make([]Segment, 0, len(sorted))
	var total int64
	for _, segment := range sorted {
		if len(reads) == 0 || segment.First > reads[len(reads)-1].Last+1 {
			reads = append(reads, Segment{First: segment.First, Last: segment.Last})
		} else if segment.Last > reads[len(reads)-1].Last {
			reads[len(reads)-1].Last = segment.Last
		}
	}
	for _, read := range reads {
		sum, ok := checkedAdd(total, read.Length())
		if !ok {
			return nil, 0, false
		}
		total = sum
	}
	return reads, total, true
}

func checkedAdd(a, b int64) (int64, bool) {
	sum := a + b
	if (sum < a || sum < b) || (a > 0 && b > 0 && sum < 0) {
		return 0, false
	}
	return sum, true
}

func checkedSub(a, b int64) (int64, bool) {
	if b > a {
		return 0, false
	}
	return a - b, true
}

// MultipartEstimator computes the wire size of a multipart/byteranges
// response, including every boundary delimiter, part header and the closing
// boundary.
type MultipartEstimator struct {
	Boundary string
}

// NewMultipartEstimator builds an estimator using a randomly generated
// boundary of 30 characters, matching net/http's multipart writer.
func NewMultipartEstimator() *MultipartEstimator {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	random := make([]byte, 30)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	boundary := make([]byte, 0, 30)
	for _, value := range random {
		boundary = append(boundary, alphabet[int(value)%len(alphabet)])
	}
	return &MultipartEstimator{Boundary: string(boundary)}
}

// Estimate implements Estimator.
func (m *MultipartEstimator) Estimate(size int64, segments []Segment) (int64, error) {
	if m.Boundary == "" {
		return 0, errors.New("netsec: empty multipart boundary")
	}

	var total int64
	for _, segment := range segments {
		partHeader := "\r\n--" + m.Boundary + "\r\n" +
			"Content-Range: " + segment.ContentRange(size) + "\r\n\r\n"
		headerLen, ok := checkedAdd(int64(len(partHeader)), segment.Length())
		if !ok {
			return 0, ErrOverflow
		}
		sum, ok := checkedAdd(total, headerLen)
		if !ok {
			return 0, ErrOverflow
		}
		total = sum
	}

	closing := "\r\n--" + m.Boundary + "--\r\n"
	total, ok := checkedAdd(total, int64(len(closing)))
	if !ok {
		return 0, ErrOverflow
	}
	return total, nil
}

// ContentType returns the multipart media type carrying the boundary
// parameter, suitable for an HTTP Content-Type response header.
func (m *MultipartEstimator) ContentType() string {
	return "multipart/byteranges; boundary=" + m.Boundary
}
