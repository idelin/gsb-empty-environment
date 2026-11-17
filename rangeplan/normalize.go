package rangeplan

// Normalize resolves one parsed spec against the fixed object length size and
// returns the intersecting closed byte range [Start, End].
//
// The object's bytes are indexed 0..size-1. Ranges that start at or beyond the
// object length, and suffix ranges of an empty object, are unsatisfiable and
// return ErrUnsatisfiable. A range extending past the end of the object is
// clipped rather than rejected, matching RFC 7233.
func Normalize(spec Spec, size int64) (Range, error) {
	if size < 0 {
		return Range{}, ErrOverflow
	}

	switch {
	case spec.IsSuffix():
		// bytes=-N: the last N bytes.
		if size == 0 || spec.End <= 0 {
			return Range{}, ErrUnsatisfiable
		}
		length := spec.End
		if length > size {
			length = size
		}
		var start int64
		if size != length {
			var ok bool
			start, ok = checkedSub(size, length)
			if !ok {
				return Range{}, ErrOverflow
			}
		}
		end, ok := checkedSub(size, 1)
		if !ok {
			return Range{}, ErrOverflow
		}
		return Range{Start: start, End: end}, nil

	case spec.IsOpenEnded():
		// bytes=N-: from N through the final byte.
		if spec.Start >= size {
			return Range{}, ErrUnsatisfiable
		}
		return Range{Start: spec.Start, End: size - 1}, nil

	default:
		// bytes=N-M
		if spec.Start >= size {
			return Range{}, ErrUnsatisfiable
		}
		end := spec.End
		if end >= size {
			end = size - 1
		}
		return Range{Start: spec.Start, End: end}, nil
	}
}

// NormalizeAll resolves every spec against size and drops unsatisfiable
// ranges. It returns ErrUnsatisfiable only when no range intersects the
// object. Input order is preserved, including duplicate ranges.
func NormalizeAll(specs []Spec, size int64) ([]Range, error) {
	ranges := make([]Range, 0, len(specs))
	for _, spec := range specs {
		r, err := Normalize(spec, size)
		if err == ErrUnsatisfiable {
			continue
		}
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}
	if len(ranges) == 0 {
		return nil, ErrUnsatisfiable
	}
	return ranges, nil
}

// Range is a normalized closed byte interval inclusive of both endpoints.
type Range struct {
	Start int64
	End   int64
}

// Length returns the number of bytes covered by the range.
func (r Range) Length() int64 { return r.End - r.Start + 1 }

// checkedSub returns a-b and false on int64 underflow.
func checkedSub(a, b int64) (int64, bool) {
	if b > a {
		return 0, false
	}
	return a - b, true
}
