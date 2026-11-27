package dra

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a parsed semantic version: MAJOR.MINOR.PATCH with an optional
// dot-separated prerelease identifier list.
type semver struct {
	major      int64
	minor      int64
	patch      int64
	prerelease []prereleasePart
}

type prereleasePart struct {
	isNum bool
	num   int64
	text  string
}

// parseSemver accepts MAJOR.MINOR.PATCH[-prerelease][+build].
func parseSemver(raw string) (semver, error) {
	v := strings.TrimSpace(raw)
	if plus := strings.IndexByte(v, '+'); plus >= 0 {
		v = v[:plus]
	}
	var pre string
	if dash := strings.IndexByte(v, '-'); dash >= 0 {
		pre = v[dash+1:]
		v = v[:dash]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("%w: %q is not MAJOR.MINOR.PATCH", ErrInvalidSpec, raw)
	}
	out := semver{}
	nums := []*int64{&out.major, &out.minor, &out.patch}
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("%w: invalid version component %q", ErrInvalidSpec, raw)
		}
		*nums[i] = n
	}
	if pre != "" {
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return semver{}, fmt.Errorf("%w: empty prerelease identifier in %q", ErrInvalidSpec, raw)
			}
			if n, err := strconv.ParseInt(id, 10, 64); err == nil {
				out.prerelease = append(out.prerelease, prereleasePart{isNum: true, num: n})
			} else {
				out.prerelease = append(out.prerelease, prereleasePart{isNum: false, text: id})
			}
		}
	}
	return out, nil
}

// compareSemver returns -1/0/1 following the SemVer 2.0 precedence rules.
func compareSemver(a, b semver) int {
	for _, pair := range [][2]int64{
		{a.major, b.major},
		{a.minor, b.minor},
		{a.patch, b.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func comparePrerelease(a, b []prereleasePart) int {
	// A version without a prerelease has higher precedence.
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		x, y := a[i], b[i]
		switch {
		case x.isNum && y.isNum:
			if x.num != y.num {
				if x.num < y.num {
					return -1
				}
				return 1
			}
		case x.isNum && !y.isNum:
			return -1
		case !x.isNum && y.isNum:
			return 1
		default:
			if x.text != y.text {
				if x.text < y.text {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// semverCompatible reports whether device satisfies requirement r under
// caret (^) semantics:
//   - r.major > 0: device in [r, r.major+1.0.0)
//   - r.major == 0, r.minor > 0: device in [r, 0.(r.minor+1).0)
//   - 0.0.x: device in [r, 0.0.(x+1))
func semverCompatible(device, r semver) bool {
	if compareSemver(device, r) < 0 {
		return false
	}
	upper := semver{major: r.major + 1}
	if r.major == 0 {
		if r.minor > 0 {
			upper = semver{minor: r.minor + 1}
		} else {
			upper = semver{patch: r.patch + 1}
		}
	}
	return compareSemver(device, upper) < 0
}
