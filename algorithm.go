package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Algorithm is a canonical encryption algorithm name used in local policy.
//
// Callers may also use a dotted OID in place of a named Algorithm. Known OIDs
// are normalized to one of the constants below. An OID that is not known to
// this package is retained verbatim, which allows callers to explicitly
// whitelist newer algorithms.
type Algorithm string

const (
	AES128CBC  Algorithm = "aes128-cbc"
	AES192CBC  Algorithm = "aes192-cbc"
	AES256CBC  Algorithm = "aes256-cbc"
	AES128GCM  Algorithm = "aes128-gcm"
	AES192GCM  Algorithm = "aes192-gcm"
	AES256GCM  Algorithm = "aes256-gcm"
	AES128CCM  Algorithm = "aes128-ccm"
	AES192CCM  Algorithm = "aes192-ccm"
	AES256CCM  Algorithm = "aes256-ccm"
	AES128Wrap Algorithm = "aes128-wrap"
	AES192Wrap Algorithm = "aes192-wrap"
	AES256Wrap Algorithm = "aes256-wrap"

	Camellia128CBC Algorithm = "camellia128-cbc"
	Camellia192CBC Algorithm = "camellia192-cbc"
	Camellia256CBC Algorithm = "camellia256-cbc"

	TripleDESCBC Algorithm = "3des-cbc"
	RC2CBC       Algorithm = "rc2-cbc"
	RC4          Algorithm = "rc4"
	RC5CBC       Algorithm = "rc5-cbc"
	DESCBC       Algorithm = "des-cbc"
)

var algorithmOIDs = map[string]Algorithm{
	"2.16.840.1.101.3.4.1.2":    AES128CBC,
	"2.16.840.1.101.3.4.1.22":   AES192CBC,
	"2.16.840.1.101.3.4.1.42":   AES256CBC,
	"2.16.840.1.101.3.4.1.6":    AES128GCM,
	"2.16.840.1.101.3.4.1.26":   AES192GCM,
	"2.16.840.1.101.3.4.1.46":   AES256GCM,
	"2.16.840.1.101.3.4.1.7":    AES128CCM,
	"2.16.840.1.101.3.4.1.27":   AES192CCM,
	"2.16.840.1.101.3.4.1.47":   AES256CCM,
	"2.16.840.1.101.3.4.1.5":    AES128Wrap,
	"2.16.840.1.101.3.4.1.25":   AES192Wrap,
	"2.16.840.1.101.3.4.1.45":   AES256Wrap,
	"1.2.392.200011.61.1.1.1.2": Camellia128CBC,
	"1.2.392.200011.61.1.1.1.3": Camellia192CBC,
	"1.2.392.200011.61.1.1.1.4": Camellia256CBC,
	"1.2.840.113549.3.7":        TripleDESCBC,
	"1.2.840.113549.3.2":        RC2CBC,
	"1.2.840.113549.3.4":        RC4,
	"1.2.840.113549.3.9":        RC5CBC,
	"1.3.14.3.2.7":              DESCBC,
}

var algorithmByName = map[string]Algorithm{
	"aes128cbc":      AES128CBC,
	"aes192cbc":      AES192CBC,
	"aes256cbc":      AES256CBC,
	"aes128gcm":      AES128GCM,
	"aes192gcm":      AES192GCM,
	"aes256gcm":      AES256GCM,
	"aes128ccm":      AES128CCM,
	"aes192ccm":      AES192CCM,
	"aes256ccm":      AES256CCM,
	"aes128wrap":     AES128Wrap,
	"aes192wrap":     AES192Wrap,
	"aes256wrap":     AES256Wrap,
	"camellia128cbc": Camellia128CBC,
	"camellia192cbc": Camellia192CBC,
	"camellia256cbc": Camellia256CBC,
	"3des":           TripleDESCBC,
	"3descbc":        TripleDESCBC,
	"des3":           TripleDESCBC,
	"des3cbc":        TripleDESCBC,
	"desede":         TripleDESCBC,
	"desedecbc":      TripleDESCBC,
	"desede3cbc":     TripleDESCBC,
	"tripledes":      TripleDESCBC,
	"tripledescbc":   TripleDESCBC,
	"rc2":            RC2CBC,
	"rc2cbc":         RC2CBC,
	"rc4":            RC4,
	"arcfour":        RC4,
	"rc5":            RC5CBC,
	"rc5cbc":         RC5CBC,
	"des":            DESCBC,
	"descbc":         DESCBC,
}

const (
	oidAES128CBC      = "2.16.840.1.101.3.4.1.2"
	oidAES192CBC      = "2.16.840.1.101.3.4.1.22"
	oidAES256CBC      = "2.16.840.1.101.3.4.1.42"
	oidAES128GCM      = "2.16.840.1.101.3.4.1.6"
	oidAES192GCM      = "2.16.840.1.101.3.4.1.26"
	oidAES256GCM      = "2.16.840.1.101.3.4.1.46"
	oidAES128CCM      = "2.16.840.1.101.3.4.1.7"
	oidAES192CCM      = "2.16.840.1.101.3.4.1.27"
	oidAES256CCM      = "2.16.840.1.101.3.4.1.47"
	oidAES128Wrap     = "2.16.840.1.101.3.4.1.5"
	oidAES192Wrap     = "2.16.840.1.101.3.4.1.25"
	oidAES256Wrap     = "2.16.840.1.101.3.4.1.45"
	oidCamellia128CBC = "1.2.392.200011.61.1.1.1.2"
	oidCamellia192CBC = "1.2.392.200011.61.1.1.1.3"
	oidCamellia256CBC = "1.2.392.200011.61.1.1.1.4"
	oidTripleDESCBC   = "1.2.840.113549.3.7"
	oidRC2CBC         = "1.2.840.113549.3.2"
	oidRC4            = "1.2.840.113549.3.4"
	oidRC5CBC         = "1.2.840.113549.3.9"
	oidDESCBC         = "1.3.14.3.2.7"
)

var algorithmByCanonical = map[Algorithm]string{
	AES128CBC:      oidAES128CBC,
	AES192CBC:      oidAES192CBC,
	AES256CBC:      oidAES256CBC,
	AES128GCM:      oidAES128GCM,
	AES192GCM:      oidAES192GCM,
	AES256GCM:      oidAES256GCM,
	AES128CCM:      oidAES128CCM,
	AES192CCM:      oidAES192CCM,
	AES256CCM:      oidAES256CCM,
	AES128Wrap:     oidAES128Wrap,
	AES192Wrap:     oidAES192Wrap,
	AES256Wrap:     oidAES256Wrap,
	Camellia128CBC: oidCamellia128CBC,
	Camellia192CBC: oidCamellia192CBC,
	Camellia256CBC: oidCamellia256CBC,
	TripleDESCBC:   oidTripleDESCBC,
	RC2CBC:         oidRC2CBC,
	RC4:            oidRC4,
	RC5CBC:         oidRC5CBC,
	DESCBC:         oidDESCBC,
}

// OID returns the dotted OID associated with a known algorithm. For an OID
// supplied directly by a caller it returns the normalized dotted form. It
// returns "" only for an invalid Algorithm value.
func (a Algorithm) OID() string {
	normalized, err := normalizeAlgorithm(a)
	if err != nil {
		return ""
	}
	if oid, ok := normalizeDottedOID(string(normalized)); ok {
		return oid
	}
	return algorithmByCanonical[normalized]
}

// IsWeak reports whether the algorithm is a known legacy cipher that should
// generally not be admitted into an encryption whitelist. The store itself
// does not silently override caller policy; this predicate is intended for
// constructing that policy.
func (a Algorithm) IsWeak() bool {
	normalized, err := normalizeAlgorithm(a)
	if err != nil {
		return false
	}
	switch normalized {
	case TripleDESCBC, RC2CBC, RC4, RC5CBC, DESCBC:
		return true
	default:
		return false
	}
}

func normalizeAlgorithm(a Algorithm) (Algorithm, error) {
	raw := strings.TrimSpace(string(a))
	if raw == "" {
		return "", fmt.Errorf("%w: empty value", ErrUnknownAlgorithm)
	}
	if oid, ok := normalizeDottedOID(raw); ok {
		if known, found := algorithmOIDs[oid]; found {
			return known, nil
		}
		return Algorithm(oid), nil
	}
	if known, found := algorithmByName[aliasKey(raw)]; found {
		return known, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAlgorithm, raw)
}

func algorithmFromOID(oid string) Algorithm {
	if known, found := algorithmOIDs[oid]; found {
		return known
	}
	return Algorithm(oid)
}

func aliasKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeDottedOID(raw string) (string, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return "", false
	}
	arcs := make([]uint64, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		if len(part) > 1 && part[0] == '0' {
			return "", false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return "", false
		}
		arcs = append(arcs, n)
	}
	if arcs[0] > 2 || (arcs[0] < 2 && arcs[1] > 39) {
		return "", false
	}
	var b strings.Builder
	for i, arc := range arcs {
		if i != 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(arc, 10))
	}
	return b.String(), true
}
