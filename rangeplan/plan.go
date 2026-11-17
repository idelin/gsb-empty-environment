package rangeplan

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
)

// Default multipart framing used when Limits does not override it.
const (
	DefaultBoundary    = "rangeplan-boundary"
	DefaultContentType = "multipart/byteranges"
)

// Span is one merged upstream read. Overlapping or adjacent requested ranges
// are coalesced into a single span so upstream storage is not asked for the
// same bytes twice.
type Span struct {
	Start int64
	End   int64
}

// Length returns the number of bytes covered by the span.
func (s Span) Length() int64 { return s.End - s.Start + 1 }

// Segment is one part of the multipart response, preserving the requested
// range semantics. Two identical requests produce two separate segments even
// though both are served from the same span.
type Segment struct {
	Start int64
	End   int64
}

// Length returns the number of bytes carried by the segment.
func (s Segment) Length() int64 { return s.End - s.Start + 1 }

// Limits bounds the resources a single range request may consume. A zero
// value means "unlimited" for that dimension; NewLimits returns conservative
// non-zero defaults.
type Limits struct {
	// MaxRanges bounds the number of raw ranges accepted in the header.
	MaxRanges int

	// MaxSegments bounds the number of multipart segments emitted after
	// normalization (one per satisfiable requested range).
	MaxSegments int

	// MaxResponseBytes bounds the full response size: segment payload bytes
	// plus multipart boundary and per-segment header overhead.
	MaxResponseBytes int64

	// Boundary is the multipart boundary used in overhead accounting.
	Boundary string

	// ContentType is the multipart media type used in part headers.
	ContentType string
}

// NewLimits returns conservative default limits suitable for a public
// download gateway.
func NewLimits() Limits {
	return Limits{
		MaxRanges:        50,
		MaxSegments:      50,
		MaxResponseBytes: 16 << 20, // 16 MiB including framing
		Boundary:         DefaultBoundary,
		ContentType:      DefaultContentType,
	}
}

// Plan is the result of planning a Range request.
type Plan struct {
	// Segments preserves requested range order and multiplicity; it drives
	// the multipart/byteranges response.
	Segments []Segment

	// Reads holds the coalesced, sorted, non-overlapping upstream reads.
	Reads []Span

	// Size is the fixed object length the plan was built against.
	Size int64

	limits Limits
}

// DataBytes returns the payload bytes carried by all segments (duplicates
// counted per segment).
func (p *Plan) DataBytes() (int64, error) {
	var total int64
	var err error
	for _, seg := range p.Segments {
		total, err = add64(total, seg.Length())
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// ReadBytes returns the bytes that must actually be fetched upstream after
// overlap merging.
func (p *Plan) ReadBytes() (int64, error) {
	var total int64
	var err error
	for _, span := range p.Reads {
		total, err = add64(total, span.Length())
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// OverheadBytes returns the non-payload framing size of a multipart response:
// every part header and boundary delimiter plus the closing boundary.
func (p *Plan) OverheadBytes() (int64, error) {
	boundary := p.limits.Boundary
	contentType := p.limits.ContentType

	var total int64
	var err error
	for _, seg := range p.Segments {
		total, err = add64(total, p.partOverhead(boundary, contentType, seg))
		if err != nil {
			return 0, err
		}
	}
	// Closing boundary: "--" boundary "--" CRLF
	total, err = add64(total, int64(len("--")+len(boundary)+len("--")+2))
	if err != nil {
		return 0, err
	}
	return total, nil
}

// TotalBytes returns the full on-the-wire response size: payload plus framing.
func (p *Plan) TotalBytes() (int64, error) {
	data, err := p.DataBytes()
	if err != nil {
		return 0, err
	}
	overhead, err := p.OverheadBytes()
	if err != nil {
		return 0, err
	}
	return add64(data, overhead)
}

// partOverhead computes the framing bytes of one multipart part excluding its
// payload:
//
//	--boundary CRLF
//	Content-Type: <type> CRLF
//	Content-Range: bytes start-end/size CRLF
//	CRLF
//	<payload> CRLF
//
// The trailing CRLF after each payload is part of the multipart framing.
func (p *Plan) partOverhead(boundary, contentType string, seg Segment) int64 {
	return int64(
		len("--") + len(boundary) + 2 + // boundary delimiter line
			len("Content-Type: ") + len(contentType) + 2 +
			len("Content-Range: bytes ") +
			len(strconv.FormatInt(seg.Start, 10)) + 1 +
			len(strconv.FormatInt(seg.End, 10)) + 1 +
			len(strconv.FormatInt(p.Size, 10)) + 2 +
			2 + // blank line before the payload
			2) // CRLF terminating the part payload
}

// Plan parses header, normalizes it against the fixed object length size and
// applies limits. The returned Plan keeps response segments in request order
// while upstream reads are merged.
func PlanRange(header string, size int64, limits Limits) (*Plan, error) {
	if size < 0 {
		return nil, ErrOverflow
	}

	specs, err := Parse(header)
	if err != nil {
		return nil, err
	}
	if limits.MaxRanges > 0 && len(specs) > limits.MaxRanges {
		return nil, ErrTooManyRanges
	}

	ranges, err := NormalizeAll(specs, size)
	if err != nil {
		return nil, err
	}
	if limits.MaxSegments > 0 && len(ranges) > limits.MaxSegments {
		return nil, ErrTooManySegments
	}

	segments := make([]Segment, len(ranges))
	for i, r := range ranges {
		segments[i] = Segment{Start: r.Start, End: r.End}
	}
	reads := mergeSpans(ranges)

	plan := &Plan{
		Segments: segments,
		Reads:    reads,
		Size:     size,
		limits:   withFramingDefaults(limits),
	}

	total, err := plan.TotalBytes()
	if err != nil {
		return nil, err
	}
	if limits.MaxResponseBytes > 0 && total > limits.MaxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return plan, nil
}

// withFramingDefaults fills empty framing fields so overhead is deterministic
// even when callers supply a zero Limits.
func withFramingDefaults(limits Limits) Limits {
	if limits.Boundary == "" {
		limits.Boundary = DefaultBoundary
	}
	if limits.ContentType == "" {
		limits.ContentType = DefaultContentType
	}
	return limits
}

// mergeSpans coalesces overlapping or touching ranges into sorted, disjoint
// spans.
func mergeSpans(ranges []Range) []Span {
	if len(ranges) == 0 {
		return nil
	}
	ordered := make([]Range, len(ranges))
	copy(ordered, ranges)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start == ordered[j].Start {
			return ordered[i].End < ordered[j].End
		}
		return ordered[i].Start < ordered[j].Start
	})

	spans := []Span{{Start: ordered[0].Start, End: ordered[0].End}}
	for _, r := range ordered[1:] {
		last := &spans[len(spans)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		spans = append(spans, Span{Start: r.Start, End: r.End})
	}
	return spans
}

// HTTPStatus maps a package error onto the HTTP status a gateway should
// return. Unknown errors become 500.
func HTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrUnsatisfiable):
		return http.StatusRequestedRangeNotSatisfiable
	case errors.Is(err, ErrMalformedRange):
		return http.StatusBadRequest
	case errors.Is(err, ErrTooManyRanges),
		errors.Is(err, ErrTooManySegments),
		errors.Is(err, ErrResponseTooLarge):
		return http.StatusRequestedRangeNotSatisfiable
	case errors.Is(err, ErrOverflow):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// add64 returns a+b and ErrOverflow on int64 overflow.
func add64(a, b int64) (int64, error) {
	if b > (1<<63-1)-a {
		return 0, ErrOverflow
	}
	return a + b, nil
}
