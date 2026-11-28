package promqlengine

import (
	"errors"
	"time"
)

// ParseDuration parses a PromQL duration literal (for example "5m",
// "1h30m", "250ms"). Supported units are ms, s, m, h, d, w and y, matching
// the Prometheus model.Duration grammar. A leading minus sign is accepted so
// that negative offsets parse directly.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("promqlengine: empty duration")
	}
	orig := s
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}

	var total time.Duration
	lastUnit := false
	for len(s) > 0 {
		num, rest, err := scanDecimal(s)
		if err != nil {
			return 0, err
		}
		if len(rest) == 0 {
			return 0, errors.New("promqlengine: duration " + orig + " missing unit")
		}
		unit, rest2, err := scanUnit(rest)
		if err != nil {
			return 0, err
		}
		total += time.Duration(num) * unit
		s = rest2
		lastUnit = true
	}
	if !lastUnit {
		return 0, errors.New("promqlengine: invalid duration " + orig)
	}
	if negative {
		total = -total
	}
	return total, nil
}

func scanDecimal(s string) (int64, string, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, errors.New("promqlengine: expected number in duration")
	}
	var n int64
	for _, c := range s[:i] {
		n = n*10 + int64(c-'0')
	}
	return n, s[i:], nil
}

func scanUnit(s string) (time.Duration, string, error) {
	// Order matters: check two-character units before single characters.
	for _, u := range []struct {
		token string
		value time.Duration
	}{
		{"ms", time.Millisecond},
		{"s", time.Second},
		{"m", time.Minute},
		{"h", time.Hour},
		{"d", 24 * time.Hour},
		{"w", 7 * 24 * time.Hour},
		{"y", 365 * 24 * time.Hour},
	} {
		if len(s) >= len(u.token) && s[:len(u.token)] == u.token {
			return u.value, s[len(u.token):], nil
		}
	}
	return 0, s, errors.New("promqlengine: unknown duration unit in " + s)
}
