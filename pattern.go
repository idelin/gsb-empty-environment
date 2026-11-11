package netsec

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

// HMACFunc computes a keyed hash over a hostname (or [host]:port) string.
// It is injected so callers control the hash algorithm used to verify
// hashed hostname patterns.
type HMACFunc func(salt, msg []byte) []byte

// Pattern is a single parsed known-hosts style host pattern.
//
// Supported forms:
//   - "host"              exact or wildcard hostname, matches any port
//   - "[host]:port"       hostname restricted to a single port
//   - "!pattern"          negation of any of the above
//   - "|1|salt|hash"      HMAC-hashed hostname, verified via an injected HMACFunc
//
// Host may contain the wildcards '*' (any sequence) and '?' (one byte).
// Matching is case-insensitive.
type Pattern struct {
	Negated bool
	Hashed  bool
	Salt    []byte
	Hash    []byte
	Host    string
	Port    int // 0 matches any port
}

// ParsePattern parses one pattern token.
func ParsePattern(s string) (Pattern, error) {
	var p Pattern
	if s == "" {
		return p, fmt.Errorf("netsec: empty pattern")
	}
	if strings.HasPrefix(s, "!") {
		p.Negated = true
		s = s[1:]
		if s == "" {
			return p, fmt.Errorf("netsec: empty negated pattern")
		}
	}
	if strings.HasPrefix(s, "|1|") {
		rest := s[len("|1|"):]
		salt64, hash64, ok := strings.Cut(rest, "|")
		if !ok {
			return p, fmt.Errorf("netsec: malformed hashed pattern %q", s)
		}
		salt, err := base64.StdEncoding.DecodeString(salt64)
		if err != nil {
			return p, fmt.Errorf("netsec: bad hashed pattern salt: %w", err)
		}
		hash, err := base64.StdEncoding.DecodeString(hash64)
		if err != nil {
			return p, fmt.Errorf("netsec: bad hashed pattern hash: %w", err)
		}
		p.Hashed = true
		p.Salt = salt
		p.Hash = hash
		return p, nil
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]:")
		if end < 0 {
			return p, fmt.Errorf("netsec: malformed [host]:port pattern %q", s)
		}
		p.Host = strings.ToLower(s[1:end])
		var port int
		if _, err := fmt.Sscanf(s[end+2:], "%d", &port); err != nil || port <= 0 || port > 65535 {
			return p, fmt.Errorf("netsec: bad port in pattern %q", s)
		}
		p.Port = port
		return p, nil
	}
	if strings.ContainsAny(s, "[]:") {
		return p, fmt.Errorf("netsec: malformed pattern %q", s)
	}
	p.Host = strings.ToLower(s)
	return p, nil
}

// ParsePatternList parses a comma-separated list of patterns.
func ParsePatternList(s string) ([]Pattern, error) {
	fields := strings.Split(s, ",")
	out := make([]Pattern, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p, err := ParsePattern(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("netsec: no patterns in list")
	}
	return out, nil
}

// String renders the pattern back to its canonical textual form.
func (p Pattern) String() string {
	var b strings.Builder
	if p.Negated {
		b.WriteByte('!')
	}
	if p.Hashed {
		b.WriteString("|1|")
		b.WriteString(base64.StdEncoding.EncodeToString(p.Salt))
		b.WriteByte('|')
		b.WriteString(base64.StdEncoding.EncodeToString(p.Hash))
		return b.String()
	}
	if p.Port != 0 {
		fmt.Fprintf(&b, "[%s]:%d", p.Host, p.Port)
	} else {
		b.WriteString(p.Host)
	}
	return b.String()
}

// addrForHash returns the string covered by hashed patterns: the bare
// hostname for the default SSH port (or when no port is given), and
// "[host]:port" otherwise.
func addrForHash(host string, port int) string {
	if port == 0 || port == 22 {
		return host
	}
	return fmt.Sprintf("[%s]:%d", host, port)
}

// Matches reports whether the pattern matches host/port. Negation is a
// property of the pattern itself and does not affect the match result;
// callers combine negated and positive patterns via MatchAny.
func (p Pattern) Matches(host string, port int, hmac HMACFunc) bool {
	host = strings.ToLower(host)
	if p.Hashed {
		if hmac == nil {
			return false
		}
		sum := hmac(p.Salt, []byte(addrForHash(host, port)))
		return subtle.ConstantTimeCompare(sum, p.Hash) == 1
	}
	if p.Port != 0 && p.Port != port {
		return false
	}
	return matchWildcard(p.Host, host)
}

// MatchAny applies known-hosts pattern-list semantics: at least one
// positive pattern must match and no negated pattern may match.
func MatchAny(patterns []Pattern, host string, port int, hmac HMACFunc) bool {
	matched := false
	for _, p := range patterns {
		if !p.Matches(host, port, hmac) {
			continue
		}
		if p.Negated {
			return false
		}
		matched = true
	}
	return matched
}

// matchWildcard implements glob matching with '*' and '?'.
func matchWildcard(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 1 && pattern[1] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchWildcard(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern, s = pattern[1:], s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern, s = pattern[1:], s[1:]
		}
	}
	return len(s) == 0
}

// HashPattern builds a hashed hostname pattern for the given host/port,
// mirroring what Matches expects. It is a helper for tooling and tests.
func HashPattern(salt []byte, host string, port int, hmac HMACFunc) Pattern {
	return Pattern{
		Hashed: true,
		Salt:   bytes.Clone(salt),
		Hash:   hmac(salt, []byte(addrForHash(host, port))),
	}
}
