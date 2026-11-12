package modselect

import (
	"fmt"
	"strings"
)

// Retraction is an inclusive version interval [Low, High] carrying the
// rationale recorded in the retract directive. A single-version retraction
// has Low == High.
type Retraction struct {
	Low       string
	High      string
	Rationale string
}

// ParseRetraction parses a retract directive: either a single version
// ("v1.2.3") or a closed interval ("[v1.0.0, v1.0.5]").
func ParseRetraction(spec, rationale string) (Retraction, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "[") {
		if !strings.HasSuffix(spec, "]") {
			return Retraction{}, fmt.Errorf("modselect: malformed retraction interval %q", spec)
		}
		parts := strings.Split(spec[1:len(spec)-1], ",")
		if len(parts) != 2 {
			return Retraction{}, fmt.Errorf("modselect: malformed retraction interval %q", spec)
		}
		low := strings.TrimSpace(parts[0])
		high := strings.TrimSpace(parts[1])
		lo, err := Parse(low)
		if err != nil {
			return Retraction{}, err
		}
		hi, err := Parse(high)
		if err != nil {
			return Retraction{}, err
		}
		if Compare(lo, hi) > 0 {
			return Retraction{}, fmt.Errorf("modselect: retraction interval %q has low > high", spec)
		}
		return Retraction{Low: low, High: high, Rationale: rationale}, nil
	}
	if _, err := Parse(spec); err != nil {
		return Retraction{}, err
	}
	return Retraction{Low: spec, High: spec, Rationale: rationale}, nil
}

// Contains reports whether version v falls inside the retraction interval.
// Comparison ignores build metadata, so retracting v2.0.0 also covers
// v2.0.0+incompatible.
func (r Retraction) Contains(v string) bool {
	vv, err := Parse(v)
	if err != nil {
		return false
	}
	lo, err := Parse(r.Low)
	if err != nil {
		return false
	}
	hi, err := Parse(r.High)
	if err != nil {
		return false
	}
	return Compare(vv, lo) >= 0 && Compare(vv, hi) <= 0
}
