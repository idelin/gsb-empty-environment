package taskpkg

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// ParseHexLabel parses a DNS payload label. The first four hexadecimal digits
// are the fragment number and the remaining hexadecimal digits are data.
//
// The label must be non-empty and consist only of hexadecimal digits. The data
// part must contain a whole number of bytes. A label with exactly four digits
// is valid and carries zero data bytes.
func ParseHexLabel(label string) (index uint16, data []byte, ok bool) {
	label = strings.TrimSpace(label)
	if len(label) < HexIndexLen {
		return 0, nil, false
	}
	for i := 0; i < len(label); i++ {
		if !isHexDigit(label[i]) {
			return 0, nil, false
		}
	}
	if (len(label)-HexIndexLen)%2 != 0 {
		return 0, nil, false
	}

	n, err := strconv.ParseUint(label[:HexIndexLen], 16, 16)
	if err != nil {
		return 0, nil, false
	}
	raw := strings.ToLower(label[HexIndexLen:])
	if raw == "" {
		return uint16(n), []byte{}, true
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return 0, nil, false
	}
	return uint16(n), b, true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' ||
		c >= 'a' && c <= 'f' ||
		c >= 'A' && c <= 'F'
}

func normalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	for strings.HasSuffix(name, ".") {
		name = strings.TrimSuffix(name, ".")
	}
	return name
}

func splitName(name string) []string {
	name = normalizeName(name)
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

func hasSuffixLabels(labels, suffix []string) bool {
	if len(suffix) == 0 || len(labels) < len(suffix) {
		return false
	}
	offset := len(labels) - len(suffix)
	for i := range suffix {
		if labels[offset+i] != suffix[i] {
			return false
		}
	}
	return true
}

// payloadLabel returns the candidate payload label and the registered-domain
// grouping key for a query.
func payloadLabel(q Query) (label, registeredDomain string, ok bool) {
	explicit := strings.TrimSpace(q.Label)
	domain := normalizeName(q.domainName())
	registeredDomain = normalizeName(q.registeredDomain())

	if explicit != "" {
		if registeredDomain == "" {
			labels := splitName(domain)
			if len(labels) > 1 {
				registeredDomain = strings.Join(labels[1:], ".")
			}
		}
		return explicit, registeredDomain, true
	}
	if domain == "" {
		return "", registeredDomain, false
	}

	labels := splitName(domain)
	if len(labels) == 0 {
		return "", registeredDomain, false
	}

	if registeredDomain != "" {
		registeredLabels := splitName(registeredDomain)
		if !hasSuffixLabels(labels, registeredLabels) {
			// The probe may pass Domain as the payload label while supplying
			// RegisteredDomain separately.
			return labels[0], registeredDomain, true
		}
		if len(labels) <= len(registeredLabels) {
			return "", registeredDomain, false
		}
		return labels[len(labels)-len(registeredLabels)-1], registeredDomain, true
	}

	// Without an explicit registered domain, the left-most label is treated as
	// the payload and the remainder as the registration grouping key.
	if len(labels) < 2 {
		return labels[0], "", true
	}
	return labels[0], strings.Join(labels[1:], "."), true
}
