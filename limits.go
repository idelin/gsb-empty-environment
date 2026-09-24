package canonicalkey

import "unicode/utf8"

type Limits struct {
	MaxInputBytes     int
	MaxCanonicalBytes int
	MaxDepth          int
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:     1 << 20,
		MaxCanonicalBytes: 4 << 20,
		MaxDepth:          64,
	}
}

func (limits Limits) withDefaults() Limits {
	defaults := DefaultLimits()
	if limits.MaxInputBytes <= 0 {
		limits.MaxInputBytes = defaults.MaxInputBytes
	}
	if limits.MaxCanonicalBytes <= 0 {
		limits.MaxCanonicalBytes = defaults.MaxCanonicalBytes
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	return limits
}

func validateUTF8(value string) error {
	if !utf8.ValidString(value) {
		return ErrInvalidUnicode
	}
	return nil
}
