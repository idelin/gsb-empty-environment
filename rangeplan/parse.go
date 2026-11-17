// Package rangeplan parses and plans multi-range byte-range requests for a
// download gateway.
//
// A single HTTP Range header may request many byte ranges. Overlapping ranges
// can be merged for upstream reads while the response keeps every requested
// segment, and the total response size (including multipart framing) is
// bounded to defeat range-amplification attacks.
package rangeplan

import (
	"errors"
	"strings"
)

// Sentinel errors returned by the package.
var (
	// ErrMalformedRange means the Range header is syntactically invalid.
	ErrMalformedRange = errors.New("rangeplan: malformed byte range")

	// ErrUnsatisfiable means none of the requested ranges intersect the
	// object. Callers typically answer with HTTP 416.
	ErrUnsatisfiable = errors.New("rangeplan: no satisfiable byte range")

	// ErrTooManyRanges means the request contained more raw ranges than
	// Limits.MaxRanges permits.
	ErrTooManyRanges = errors.New("rangeplan: too many requested ranges")

	// ErrTooManySegments means more response segments remain after
	// normalization than Limits.MaxSegments permits.
	ErrTooManySegments = errors.New("rangeplan: too many response segments")

	// ErrResponseTooLarge means the planned response, including multipart
	// boundary and per-segment header overhead, exceeds Limits.MaxResponseBytes.
	ErrResponseTooLarge = errors.New("rangeplan: planned response exceeds budget")

	// ErrOverflow means an integer overflow occurred while normalizing
	// ranges or accumulating byte counts.
	ErrOverflow = errors.New("rangeplan: integer overflow")
)

// Spec is one unnormalized byte range parsed from a Range header.
//
// A suffix range ("bytes=-N") has Suffix set and Start/End unset. An
// open-ended range ("bytes=N-") has Start set and OpenEnd set with End unset.
// A closed range ("bytes=N-M") has Start and End set.
type Spec struct {
	Start   int64
	End     int64
	OpenEnd bool
	Suffix  bool
}

// IsSuffix reports whether the spec is a suffix range ("bytes=-N").
func (s Spec) IsSuffix() bool { return s.Suffix }

// IsOpenEnded reports whether the spec is open-ended ("bytes=N-").
func (s Spec) IsOpenEnded() bool { return !s.Suffix && s.OpenEnd }

// Parse parses an HTTP Range header carrying byte ranges.
//
// It accepts the byte-range syntax defined by RFC 7233: one or more
// comma-separated ranges after the case-insensitive "bytes=" prefix, in the
// forms "N-M", "N-" and "-N". Whitespace is tolerated around the prefix and
// individual ranges but not inside a range. Values must be non-negative
// integers that fit in int64.
func Parse(header string) ([]Spec, error) {
	value := strings.TrimSpace(header)
	if len(value) < len("bytes=") || !strings.EqualFold(value[:len("bytes=")], "bytes=") {
		return nil, ErrMalformedRange
	}
	body := value[len("bytes="):]

	parts := strings.Split(body, ",")
	specs := make([]Spec, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			return nil, ErrMalformedRange
		}
		dash := strings.IndexByte(token, '-')
		if dash < 0 || strings.IndexByte(token[dash+1:], '-') >= 0 {
			return nil, ErrMalformedRange
		}

		left, right := token[:dash], token[dash+1:]
		switch {
		case left == "":
			// Suffix range: bytes=-N
			n, err := parseDigits(right)
			if err != nil {
				return nil, err
			}
			specs = append(specs, Spec{End: n, Suffix: true})
		case right == "":
			// Open-ended range: bytes=N-
			n, err := parseDigits(left)
			if err != nil {
				return nil, err
			}
			specs = append(specs, Spec{Start: n, OpenEnd: true})
		default:
			// Closed range: bytes=N-M
			start, err := parseDigits(left)
			if err != nil {
				return nil, err
			}
			end, err := parseDigits(right)
			if err != nil {
				return nil, err
			}
			if end < start {
				return nil, ErrMalformedRange
			}
			specs = append(specs, Spec{Start: start, End: end})
		}
	}
	if len(specs) == 0 {
		return nil, ErrMalformedRange
	}
	return specs, nil
}

// parseDigits converts a non-empty ASCII digit string to a non-negative int64.
// Any non-digit, empty input or value overflowing int64 is rejected.
func parseDigits(s string) (int64, error) {
	if s == "" {
		return 0, ErrMalformedRange
	}
	const cutoff = (1<<63 - 1) / 10
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrMalformedRange
		}
		d := int64(c - '0')
		if n > cutoff || (n == cutoff && d > (1<<63-1)%10) {
			return 0, ErrOverflow
		}
		n = n*10 + d
	}
	return n, nil
}
