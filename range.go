package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind enumerates the interval shapes accepted in a bytes Range header.
type Kind uint8

const (
	// Closed is the inclusive interval start-end.
	Closed Kind = iota
	// OpenTail is start- and runs to the end of the object.
	OpenTail
	// Suffix is -suffix and selects the final suffix bytes.
	Suffix
)

// Spec is a single byte-range interval as requested, before it is resolved
// against the concrete length of the target object.
//
// Start and End are used for Closed and OpenTail (End is ignored by OpenTail),
// while Suffix is used for the Suffix kind.
type Spec struct {
	Kind   Kind
	Start  int64
	End    int64
	Suffix int64
}

// NewClosed builds an inclusive start-end interval.
func NewClosed(start, end int64) (Spec, error) {
	if start < 0 || end < start {
		return Spec{}, ErrMalformed
	}
	return Spec{Kind: Closed, Start: start, End: end}, nil
}

// NewOpenTail builds a start- interval running to the object end.
func NewOpenTail(start int64) (Spec, error) {
	if start < 0 {
		return Spec{}, ErrMalformed
	}
	return Spec{Kind: OpenTail, Start: start}, nil
}

// NewSuffix builds a -suffix interval selecting the final suffix bytes.
func NewSuffix(suffix int64) (Spec, error) {
	if suffix <= 0 {
		return Spec{}, ErrMalformed
	}
	return Spec{Kind: Suffix, Suffix: suffix}, nil
}

// String renders the interval in Range-header syntax, without the "bytes="
// prefix.
func (s Spec) String() string {
	switch s.Kind {
	case OpenTail:
		return strconv.FormatInt(s.Start, 10) + "-"
	case Suffix:
		return "-" + strconv.FormatInt(s.Suffix, 10)
	default:
		return strconv.FormatInt(s.Start, 10) + "-" + strconv.FormatInt(s.End, 10)
	}
}

func (s Spec) valid() bool {
	switch s.Kind {
	case Closed:
		return s.Start >= 0 && s.End >= s.Start
	case OpenTail:
		return s.Start >= 0
	case Suffix:
		return s.Suffix > 0
	default:
		return false
	}
}

// Segment is a resolved, inclusive byte range [First, Last] known to lie
// inside the object.
type Segment struct {
	First int64
	Last  int64
}

// Length reports the number of bytes covered by the segment.
func (s Segment) Length() int64 { return s.Last - s.First + 1 }

// Contains reports whether offset lies within the segment.
func (s Segment) Contains(offset int64) bool {
	return s.First <= offset && offset <= s.Last
}

// ContentRange renders the Content-Range header field value for this segment,
// for example "bytes 0-99/1000".
func (s Segment) ContentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", s.First, s.Last, size)
}

// Normalize resolves the interval against an object of the given fixed size
// and returns the concrete inclusive segment.
//
// OpenTail intervals are clipped to the object end, Closed intervals are
// clipped when they extend past the object end, and Suffix intervals are
// clamped to the object length. An interval that does not intersect the
// object yields an UnsatisfiableError wrapping ErrUnsatisfiable.
func (s Spec) Normalize(size int64) (Segment, error) {
	if size < 0 {
		return Segment{}, ErrInvalidSize
	}
	if !s.valid() {
		return Segment{}, ErrMalformed
	}
	switch s.Kind {
	case Closed:
		if s.Start >= size {
			return Segment{}, unsatisfiable(s, size)
		}
		last := s.End
		if last >= size {
			last = size - 1
		}
		return Segment{First: s.Start, Last: last}, nil
	case OpenTail:
		if s.Start >= size {
			return Segment{}, unsatisfiable(s, size)
		}
		return Segment{First: s.Start, Last: size - 1}, nil
	default: // Suffix
		if size == 0 {
			return Segment{}, unsatisfiable(s, size)
		}
		n := s.Suffix
		if n > size {
			n = size
		}
		return Segment{First: size - n, Last: size - 1}, nil
	}
}

var (
	// ErrMalformed indicates a syntactically invalid range expression.
	ErrMalformed = errors.New("netsec: malformed range header")
	// ErrUnsupportedUnit indicates a range unit other than bytes.
	ErrUnsupportedUnit = errors.New("netsec: unsupported range unit")
	// ErrUnsatisfiable wraps every UnsatisfiableError.
	ErrUnsatisfiable = errors.New("netsec: unsatisfiable range")
	// ErrInvalidSize indicates a negative object length.
	ErrInvalidSize = errors.New("netsec: invalid object size")
)

// UnsatisfiableError marks a requested interval that cannot be served because
// it does not intersect the object. It wraps ErrUnsatisfiable.
type UnsatisfiableError struct {
	// Index is the position of the offending interval in the request.
	Index int
	// Spec is the offending interval.
	Spec Spec
	// Size is the object length used during normalization.
	Size int64
}

func unsatisfiable(spec Spec, size int64) error {
	return &UnsatisfiableError{Spec: spec, Size: size}
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("netsec: range %s unsatisfiable for object of size %d", e.Spec.String(), e.Size)
}

// Is enables errors.Is(err, ErrUnsatisfiable).
func (e *UnsatisfiableError) Is(target error) bool { return target == ErrUnsatisfiable }

// Parse parses the value of an HTTP Range header for the bytes unit, for
// example "bytes=0-99,-200,500-".
//
// It accepts closed intervals (start-end), open tails (start-) and suffix
// intervals (-suffix). Whitespace is permitted around the unit and around
// comma-separated intervals, but not inside a number. A header with no
// intervals, an unknown unit, or any invalid interval is rejected.
func Parse(header string) ([]Spec, error) {
	eq := strings.IndexByte(header, '=')
	if eq < 0 {
		return nil, ErrMalformed
	}
	unit := strings.TrimSpace(header[:eq])
	if unit == "" {
		return nil, ErrMalformed
	}
	if !strings.EqualFold(unit, "bytes") {
		return nil, ErrUnsupportedUnit
	}

	body := header[eq+1:]
	tokens := strings.Split(body, ",")
	specs := make([]Spec, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			return nil, ErrMalformed
		}
		dash := strings.IndexByte(token, '-')
		if dash < 0 {
			return nil, ErrMalformed
		}
		if dash == 0 {
			suffix, err := parseNonNegative(token[1:])
			if err != nil || suffix == 0 {
				return nil, ErrMalformed
			}
			specs = append(specs, Spec{Kind: Suffix, Suffix: suffix})
			continue
		}
		start, err := parseNonNegative(token[:dash])
		if err != nil {
			return nil, ErrMalformed
		}
		rest := token[dash+1:]
		if rest == "" {
			specs = append(specs, Spec{Kind: OpenTail, Start: start})
			continue
		}
		end, err := parseNonNegative(rest)
		if err != nil || end < start {
			return nil, ErrMalformed
		}
		specs = append(specs, Spec{Kind: Closed, Start: start, End: end})
	}
	if len(specs) == 0 {
		return nil, ErrMalformed
	}
	return specs, nil
}

func parseNonNegative(s string) (int64, error) {
	if s == "" {
		return 0, ErrMalformed
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrMalformed
		}
	}
	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMalformed
	}
	return value, nil
}
