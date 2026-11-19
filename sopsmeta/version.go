package sopsmeta

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares two dotted-numeric version strings ("3.9.0").
// It returns -1, 0, or 1, and an error if either side is unparseable.
// Missing components are treated as zero, so "3.9" equals "3.9.0".
func CompareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidVersion, a)
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidVersion, b)
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var va, vb int
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		switch {
		case va < vb:
			return -1, nil
		case va > vb:
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, fmt.Errorf("empty version")
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad component %q", p)
		}
		out[i] = n
	}
	return out, nil
}
