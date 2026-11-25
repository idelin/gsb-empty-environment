package uffd

import (
	"fmt"
	"sort"
)

const defaultPageSize uintptr = 4096

// normalizeRange validates the common ioctl range encoding. A caller may use
// Start+Length, Start+End, or both; End must agree with Start+Length when both
// are supplied.
func normalizeRange(start, length, end, pageSize uintptr) (uintptr, uintptr, error) {
	if pageSize == 0 {
		pageSize = defaultPageSize
	}

	if length == 0 {
		if end == 0 {
			return 0, 0, ErrZeroLength
		}
		if end <= start {
			return 0, 0, fmt.Errorf("%w: end %#x is not after start %#x", ErrInvalidRange, end, start)
		}
		length = end - start
	}

	if length > ^uintptr(0)-start {
		return 0, 0, fmt.Errorf("%w: start %#x length %#x", ErrRangeOverflow, start, length)
	}
	computedEnd := start + length
	if end != 0 && end != computedEnd {
		return 0, 0, fmt.Errorf("%w: end %#x does not match start+length %#x", ErrInvalidRange, end, computedEnd)
	}

	if start%pageSize != 0 || length%pageSize != 0 {
		return 0, 0, fmt.Errorf("%w: start %#x length %#x page size %#x", ErrUnaligned, start, length, pageSize)
	}
	return start, computedEnd, nil
}

// checkAnonymousPrivateCoverage verifies that every byte in [start,end) is
// covered by an anonymous private mapping owned by the caller. It also
// rejects an explicitly reported hole and refuses to bridge a gap between
// mappings.
func checkAnonymousPrivateCoverage(start, end uintptr, caller Caller, mappings []Mapping) error {
	if len(mappings) == 0 {
		return fmt.Errorf("%w: no mappings supplied", ErrUnmappedHole)
	}

	sorted := append([]Mapping(nil), mappings...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start == sorted[j].Start {
			return sorted[i].End < sorted[j].End
		}
		return sorted[i].Start < sorted[j].Start
	})

	cursor := start
	for _, m := range sorted {
		if m.End <= m.Start {
			return fmt.Errorf("%w: [%#x,%#x)", ErrInvalidMapping, m.Start, m.End)
		}
		if m.End <= cursor {
			continue
		}
		if m.Start > cursor {
			return fmt.Errorf("%w: [%#x,%#x) is not mapped", ErrUnmappedHole, cursor, m.Start)
		}

		// cursor is covered by this mapping; the whole intersection must
		// have the properties required by the sandbox.
		if m.Owner != caller.Process {
			return fmt.Errorf("%w: mapping owner %d, caller %d", ErrNotOwner, m.Owner, caller.Process)
		}
		if m.Kind == Unmapped {
			return fmt.Errorf("%w: [%#x,%#x)", ErrUnmappedHole, m.Start, m.End)
		}
		if m.Kind != AnonymousPrivate {
			return fmt.Errorf("%w: mapping [%#x,%#x) kind %d", ErrNotAnonymousPrivate, m.Start, m.End, m.Kind)
		}

		cursor = m.End
		if cursor >= end {
			return nil
		}
	}
	return fmt.Errorf("%w: [%#x,%#x) is not fully mapped", ErrUnmappedHole, cursor, end)
}

// registrationCoverage checks that [start,end) is fully covered by registered
// intervals. When need is non-zero, every covering interval must contain all
// of those mode bits.
func registrationCoverage(regs []registration, start, end uintptr, need RegisterMode) error {
	if len(regs) == 0 {
		return fmt.Errorf("%w: no registrations", ErrNotRegistered)
	}

	cursor := start
	for _, r := range regs {
		if r.end <= cursor {
			continue
		}
		if r.start > cursor {
			return fmt.Errorf("%w: [%#x,%#x)", ErrNotRegistered, cursor, r.start)
		}
		if need != 0 && r.mode&need != need {
			return fmt.Errorf("%w: range [%#x,%#x) has mode %s, need %s", ErrModeMismatch, r.start, r.end, r.mode, need)
		}
		cursor = r.end
		if cursor >= end {
			return nil
		}
	}
	return fmt.Errorf("%w: [%#x,%#x)", ErrNotRegistered, cursor, end)
}

func insertRegistration(regs []registration, r registration) ([]registration, error) {
	for _, existing := range regs {
		if r.start >= existing.end || r.end <= existing.start {
			continue
		}
		if r.start == existing.start && r.end == existing.end {
			if r.mode != existing.mode {
				return regs, fmt.Errorf("%w: existing mode %s, requested %s", ErrModeConflict, existing.mode, r.mode)
			}
			return regs, nil
		}
		return regs, fmt.Errorf("%w: [%#x,%#x) intersects [%#x,%#x)", ErrPartialOverlap, r.start, r.end, existing.start, existing.end)
	}

	regs = append(regs, r)
	sort.Slice(regs, func(i, j int) bool { return regs[i].start < regs[j].start })
	return regs, nil
}

func removeRegistrations(regs []registration, start, end uintptr) []registration {
	out := make([]registration, 0, len(regs)+1)
	for _, r := range regs {
		if r.end <= start || r.start >= end {
			out = append(out, r)
			continue
		}
		if r.start < start {
			left := r
			left.end = start
			out = append(out, left)
		}
		if r.end > end {
			right := r
			right.start = end
			out = append(out, right)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}
