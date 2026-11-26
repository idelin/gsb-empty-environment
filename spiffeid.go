package spiffefed

import (
	"fmt"
	"strings"
)

const (
	spiffeScheme      = "spiffe://"
	maxTrustDomainLen = 255
	maxIDLen          = 2048
)

// ID is a canonical SPIFFE ID. Path includes its leading slash and is empty
// only for the trust-domain root ID (for example, spiffe://example.org).
type ID struct {
	TrustDomain string
	Path        string
}

// SPIFFEID is an alias for ID.
type SPIFFEID = ID

// ParseID parses a SPIFFE ID without applying any URL decoding or
// normalization. The trust domain must already be canonical lowercase ASCII.
// Paths containing percent-encoding, dot segments, empty segments, queries or
// fragments are rejected rather than normalized.
func ParseID(raw string) (ID, error) {
	if raw == "" {
		return ID{}, fmt.Errorf("%w: empty value", ErrInvalidSPIFFEID)
	}
	if len(raw) > maxIDLen {
		return ID{}, fmt.Errorf("%w: exceeds %d bytes", ErrInvalidSPIFFEID, maxIDLen)
	}
	if !isASCII(raw) {
		return ID{}, fmt.Errorf("%w: SPIFFE IDs must be ASCII", ErrInvalidSPIFFEID)
	}
	if !strings.HasPrefix(raw, spiffeScheme) {
		return ID{}, fmt.Errorf("%w: scheme must be exactly %q", ErrInvalidSPIFFEID, "spiffe")
	}

	rest := raw[len(spiffeScheme):]
	if strings.ContainsAny(rest, "?#%\\") {
		return ID{}, fmt.Errorf("%w: query, fragment, percent-encoding, and backslash are forbidden", ErrInvalidSPIFFEID)
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] <= 0x20 || rest[i] >= 0x7f {
			return ID{}, fmt.Errorf("%w: contains whitespace, control, or non-ASCII bytes", ErrInvalidSPIFFEID)
		}
	}

	authority := rest
	path := ""
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		authority = rest[:slash]
		path = rest[slash:]
	}
	if err := ValidateTrustDomain(authority); err != nil {
		return ID{}, fmt.Errorf("%w: %w", ErrInvalidSPIFFEID, err)
	}
	if err := validatePath(path); err != nil {
		return ID{}, fmt.Errorf("%w: %v", ErrInvalidSPIFFEID, err)
	}

	return ID{TrustDomain: authority, Path: path}, nil
}

// ParseSPIFFEID is an explicit alias for ParseID.
func ParseSPIFFEID(raw string) (ID, error) {
	return ParseID(raw)
}

// String returns the canonical SPIFFE URI.
func (id ID) String() string {
	if id.TrustDomain == "" {
		return ""
	}
	return spiffeScheme + id.TrustDomain + id.Path
}

// Equal reports whether two IDs are exactly equal.
func (id ID) Equal(other ID) bool {
	return id.TrustDomain == other.TrustDomain && id.Path == other.Path
}

// IsZero reports whether the ID has not been initialized.
func (id ID) IsZero() bool {
	return id.TrustDomain == ""
}

// ValidateTrustDomain validates a canonical SPIFFE trust domain. Only
// lowercase ASCII letters, digits, hyphens, dots, and underscores are
// permitted. Empty labels (including a leading or trailing dot) are rejected.
func ValidateTrustDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("%w: empty", ErrInvalidTrustDomain)
	}
	if len(domain) > maxTrustDomainLen {
		return fmt.Errorf("%w: exceeds %d bytes", ErrInvalidTrustDomain, maxTrustDomainLen)
	}
	if !isASCII(domain) {
		return fmt.Errorf("%w: must be ASCII", ErrInvalidTrustDomain)
	}

	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("%w: contains an empty label", ErrInvalidTrustDomain)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
				continue
			}
			return fmt.Errorf("%w: character %q is not lowercase alphanumeric, '-', '.', or '_'", ErrInvalidTrustDomain, c)
		}
	}
	return nil
}

func validatePath(path string) error {
	if path == "" {
		return nil
	}
	if path[0] != '/' {
		return fmt.Errorf("path must start with '/'")
	}
	if strings.HasSuffix(path, "/") {
		return fmt.Errorf("path contains an empty trailing segment")
	}

	segments := strings.Split(path[1:], "/")
	for _, segment := range segments {
		if segment == "" {
			return fmt.Errorf("path contains an empty segment")
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("dot path segments are forbidden")
		}
		for i := 0; i < len(segment); i++ {
			if !isPathByte(segment[i]) {
				return fmt.Errorf("path character %q is not allowed", segment[i])
			}
		}
	}
	return nil
}

func isPathByte(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case '-', '.', '_', '~':
		return true
	case '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=', ':', '@':
		return true
	default:
		return false
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
