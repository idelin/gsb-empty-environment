package nvmf

import (
	"strings"
	"unicode/utf8"
)

// MaxNQNLength is the maximum permitted length, in bytes, of a host NQN as
// carried in a Connect command (NVMe Base Specification, NQN definition).
const MaxNQNLength = 223

const (
	nqnPrefix   = "nqn."
	uuidNQNKind = "nqn.2014-08.org.nvmexpress:uuid:"
)

// ValidateHostNQN validates an NQN presented by a host in a Connect command.
//
// A host NQN must be valid UTF-8, at most MaxNQNLength bytes long, and match
// one of the two forms defined by the NVMe Base Specification:
//
//   - nqn.yyyy-mm.<reverse domain name>:<user assigned suffix>
//   - nqn.2014-08.org.nvmexpress:uuid:<128-bit UUID string>
//
// The check is deliberately strict: any deviation (wrong prefix, bad date,
// malformed reverse domain, empty suffix, malformed UUID, embedded whitespace
// or control characters, ...) is rejected so that an oddly shaped NQN cannot
// be used to smuggle its way past an allowlist matcher.
func ValidateHostNQN(nqn string) error {
	if nqn == "" {
		return ErrEmptyNQN
	}
	if len(nqn) > MaxNQNLength {
		return ErrNQNTooLong
	}
	if !utf8.ValidString(nqn) {
		return ErrNQNNotUTF8
	}
	if strings.HasPrefix(nqn, uuidNQNKind) {
		if !isCanonicalUUID(nqn[len(uuidNQNKind):]) {
			return ErrMalformedUUID
		}
		return nil
	}
	return validateGenericNQN(nqn)
}

func validateGenericNQN(nqn string) error {
	rest, ok := strings.CutPrefix(nqn, nqnPrefix)
	if !ok {
		return ErrInvalidNQN
	}

	// nqn.yyyy-mm.<reverse domain>:<suffix>
	domainSuffix, _, hasSuffix := strings.Cut(rest, ":")
	if !hasSuffix || domainSuffix == "" {
		return ErrInvalidNQN
	}

	dateEnd := strings.Index(domainSuffix, ".")
	if dateEnd < 0 {
		return ErrInvalidNQN
	}
	datePart, domainPart := domainSuffix[:dateEnd], domainSuffix[dateEnd+1:]
	if !isValidYearMonth(datePart) {
		return ErrInvalidNQN
	}
	if !isValidReverseDomain(domainPart) {
		return ErrInvalidNQN
	}

	suffix := rest[len(domainSuffix)+1:]
	if !isValidSuffix(suffix) {
		return ErrInvalidNQN
	}
	return nil
}

// isValidYearMonth accepts exactly yyyy-mm, mm being 01..12.
func isValidYearMonth(s string) bool {
	if len(s) != 7 || s[4] != '-' {
		return false
	}
	for i := 0; i < 4; i++ {
		if !isASCIIDigit(s[i]) {
			return false
		}
	}
	monthHi, monthLo := s[5], s[6]
	if monthHi == '0' && monthLo >= '1' && monthLo <= '9' {
		return true
	}
	if monthHi == '1' && (monthLo == '0' || monthLo == '1' || monthLo == '2') {
		return true
	}
	return false
}

// isValidReverseDomain accepts one or more dot-separated reverse-DNS labels.
// Labels are restricted to the RFC 1035-ish NVMe character set and must be
// lowercase; no case folding is performed anywhere in admission, so an
// uppercase label can never match an allowlist entry by accident.
func isValidReverseDomain(s string) bool {
	if s == "" {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if !isValidDomainLabel(label) {
			return false
		}
	}
	return true
}

func isValidDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case isASCIILower(c) || isASCIIDigit(c) || c == '-':
		default:
			return false
		}
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	return true
}

// isValidSuffix accepts the user-assigned portion after the first colon. The
// suffix may contain any valid UTF-8 text except ASCII whitespace and control
// characters; it must be non-empty.
func isValidSuffix(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// isCanonicalUUID accepts exactly 8-4-4-4-12 lowercase or uppercase hex digits
// with the canonical hyphen positions (36 characters), e.g.
// 123e4567-e89b-12d3-a456-426614174000.
func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHexDigit(s[i]) {
				return false
			}
		}
	}
	return true
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

func isASCIILower(c byte) bool { return c >= 'a' && c <= 'z' }

func isHexDigit(c byte) bool {
	return isASCIIDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
